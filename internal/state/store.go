package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	StatusQueued    = "queued"
	StatusCreating  = "creating"
	StatusRunning   = "running"
	StatusStopping  = "stopping"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

const (
	BackendSQLite   = "sqlite"
	BackendPostgres = "postgres"
)

var ErrConflict = errors.New("state conflict")

type RunnerRequest struct {
	ID                 string    `json:"id"`
	Source             string    `json:"source"`
	JobID              int64     `json:"job_id,omitempty"`
	RepositoryFullName string    `json:"repository_full_name,omitempty"`
	Labels             []string  `json:"labels"`
	ProfileName        string    `json:"profile_name,omitempty"`
	RunnerGroup        string    `json:"runner_group,omitempty"`
	RunnerName         string    `json:"runner_name"`
	CreatedAt          time.Time `json:"created_at"`
}

type RunnerState struct {
	ID                 string    `json:"id"`
	Status             string    `json:"status"`
	RepositoryFullName string    `json:"repository_full_name,omitempty"`
	ProfileName        string    `json:"profile_name,omitempty"`
	RunnerGroup        string    `json:"runner_group,omitempty"`
	RunnerName         string    `json:"runner_name"`
	SandboxID          string    `json:"sandbox_id,omitempty"`
	ProcessPID         uint32    `json:"process_pid,omitempty"`
	AssignedJobID      int64     `json:"assigned_job_id,omitempty"`
	AssignedJobName    string    `json:"assigned_job_name,omitempty"`
	Error              string    `json:"error,omitempty"`
	FailureStage       string    `json:"failure_stage,omitempty"`
	FailureReason      string    `json:"failure_reason,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
	CreatedAt          time.Time `json:"created_at"`
	CreatingAt         time.Time `json:"creating_at,omitempty"`
	RunningAt          time.Time `json:"running_at,omitempty"`
	StoppingAt         time.Time `json:"stopping_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
	FailedAt           time.Time `json:"failed_at,omitempty"`
	Version            int64     `json:"version"`
}

type RunnerProfile struct {
	Name           string    `json:"name"`
	Labels         []string  `json:"labels"`
	TemplateID     string    `json:"template_id"`
	RunnerGroup    string    `json:"runner_group,omitempty"`
	MaxConcurrency int       `json:"max_concurrency"`
	MinIdle        int       `json:"min_idle"`
	Priority       int       `json:"priority"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type RepositoryPolicy struct {
	ID                 int64     `json:"id"`
	RepositoryFullName string    `json:"repository_full_name"`
	ProfileName        string    `json:"profile_name"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
}

type ProfileMatch struct {
	RepositoryFullName string         `json:"repository_full_name"`
	Labels             []string       `json:"labels"`
	Profile            *RunnerProfile `json:"profile,omitempty"`
	Reason             string         `json:"reason,omitempty"`
}

type Store interface {
	Ensure() error
	CreateRequest(req RunnerRequest, payload []byte) (bool, RunnerState, error)
	ReadRequest(id string) (RunnerRequest, error)
	ReadState(id string) (RunnerState, error)
	WriteState(st RunnerState) error
	ListStates() ([]RunnerState, error)
	ActiveCount() (int, error)
	AppendLog(id, name string, data []byte)
	ReadLog(id, name string, maxBytes int64) ([]byte, error)
	ListProfiles() ([]RunnerProfile, error)
	GetProfile(name string) (RunnerProfile, error)
	UpsertProfile(profile RunnerProfile) (RunnerProfile, error)
	DeleteProfile(name string) error
	ListRepositoryPolicies() ([]RepositoryPolicy, error)
	UpsertRepositoryPolicy(policy RepositoryPolicy) (RepositoryPolicy, error)
	DeleteRepositoryPolicy(id int64) error
	MatchProfile(repositoryFullName string, labels []string) (ProfileMatch, error)
}

type Options struct {
	Backend        string
	DatabaseURL    string
	MigrateOnStart bool
}

type DBStore struct {
	opts     Options
	mu       sync.Mutex
	db       *gorm.DB
	migrated bool
}

type runnerRequestRecord struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	Source             string     `gorm:"column:source"`
	WorkflowJobID      *int64     `gorm:"column:workflow_job_id"`
	RepositoryFullName string     `gorm:"column:repository_full_name"`
	LabelsJSON         string     `gorm:"column:labels_json"`
	ProfileName        string     `gorm:"column:profile_name"`
	RunnerGroup        string     `gorm:"column:runner_group"`
	RunnerName         string     `gorm:"column:runner_name"`
	Status             string     `gorm:"column:status"`
	FailureStage       string     `gorm:"column:failure_stage"`
	FailureReason      string     `gorm:"column:failure_reason"`
	SandboxID          string     `gorm:"column:sandbox_id"`
	ProcessPID         uint32     `gorm:"column:process_pid"`
	AssignedJobID      int64      `gorm:"column:assigned_job_id"`
	AssignedJobName    string     `gorm:"column:assigned_job_name"`
	Error              string     `gorm:"column:error"`
	GitHubPayloadJSON  string     `gorm:"column:github_payload_json"`
	QueuedAt           time.Time  `gorm:"column:queued_at"`
	CreatingAt         *time.Time `gorm:"column:creating_at"`
	RunningAt          *time.Time `gorm:"column:running_at"`
	StoppingAt         *time.Time `gorm:"column:stopping_at"`
	CompletedAt        *time.Time `gorm:"column:completed_at"`
	FailedAt           *time.Time `gorm:"column:failed_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
	Version            int64      `gorm:"column:version"`
}

func (runnerRequestRecord) TableName() string { return "runner_requests" }

type runnerEventRecord struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement"`
	RequestID   string    `gorm:"column:request_id"`
	EventType   string    `gorm:"column:event_type"`
	Stage       string    `gorm:"column:stage"`
	Message     string    `gorm:"column:message"`
	PayloadJSON string    `gorm:"column:payload_json"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (runnerEventRecord) TableName() string { return "runner_events" }

type runnerProfileRecord struct {
	Name           string    `gorm:"column:name;primaryKey"`
	LabelsJSON     string    `gorm:"column:labels_json"`
	TemplateID     string    `gorm:"column:template_id"`
	RunnerGroup    string    `gorm:"column:runner_group"`
	MaxConcurrency int       `gorm:"column:max_concurrency"`
	MinIdle        int       `gorm:"column:min_idle"`
	Priority       int       `gorm:"column:priority"`
	Enabled        bool      `gorm:"column:enabled"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (runnerProfileRecord) TableName() string { return "runner_profiles" }

type repositoryPolicyRecord struct {
	ID                 int64     `gorm:"column:id;primaryKey;autoIncrement"`
	RepositoryFullName string    `gorm:"column:repository_full_name"`
	ProfileName        string    `gorm:"column:profile_name"`
	Enabled            bool      `gorm:"column:enabled"`
	CreatedAt          time.Time `gorm:"column:created_at"`
}

func (repositoryPolicyRecord) TableName() string { return "repository_policies" }

func New(dir string) Store {
	return NewWithOptions(Options{
		Backend:        BackendSQLite,
		DatabaseURL:    filepath.Join(dir, "runnerd.db"),
		MigrateOnStart: true,
	})
}

func NewWithOptions(opts Options) Store {
	backend := strings.ToLower(strings.TrimSpace(opts.Backend))
	if backend == "" {
		backend = BackendSQLite
	}
	if opts.DatabaseURL == "" && backend == BackendSQLite {
		opts.DatabaseURL = filepath.Join(".", "var", "runnerd.db")
	}
	opts.Backend = backend
	return &DBStore{opts: opts}
}

func (s *DBStore) Ensure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return nil
	}

	db, err := s.open()
	if err != nil {
		return err
	}
	if s.opts.MigrateOnStart && !s.migrated {
		if err := s.migrate(db); err != nil {
			return err
		}
		s.migrated = true
	}
	s.db = db
	return nil
}

func (s *DBStore) CreateRequest(req RunnerRequest, payload []byte) (bool, RunnerState, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return false, RunnerState{}, err
	}

	req.ID = sanitizeID(req.ID)
	if req.ID == "" {
		return false, RunnerState{}, fmt.Errorf("request id is empty")
	}
	req.RunnerName = sanitizeRunnerName(req.RunnerName)
	if req.RunnerName == "" {
		req.RunnerName = "e2b-" + req.ID
	}
	now := time.Now().UTC()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}

	labelsJSON, err := json.Marshal(req.Labels)
	if err != nil {
		return false, RunnerState{}, err
	}
	record := runnerRequestRecord{
		ID:                 req.ID,
		Source:             req.Source,
		RepositoryFullName: req.RepositoryFullName,
		LabelsJSON:         string(labelsJSON),
		ProfileName:        req.ProfileName,
		RunnerGroup:        req.RunnerGroup,
		RunnerName:         req.RunnerName,
		Status:             StatusQueued,
		GitHubPayloadJSON:  string(payload),
		QueuedAt:           req.CreatedAt,
		UpdatedAt:          now,
		Version:            0,
	}
	if req.JobID != 0 {
		record.WorkflowJobID = &req.JobID
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return false, RunnerState{}, result.Error
	}
	if result.RowsAffected == 0 {
		st, err := s.ReadState(req.ID)
		return false, st, err
	}
	return true, recordToState(record), nil
}

func (s *DBStore) ReadRequest(id string) (RunnerRequest, error) {
	record, err := s.readRecord(id)
	if err != nil {
		return RunnerRequest{}, err
	}
	return recordToRequest(record)
}

func (s *DBStore) ReadState(id string) (RunnerState, error) {
	record, err := s.readRecord(id)
	if err != nil {
		return RunnerState{}, err
	}
	return recordToState(record), nil
}

func (s *DBStore) WriteState(st RunnerState) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	st.ID = sanitizeID(st.ID)
	if st.ID == "" {
		return fmt.Errorf("request id is empty")
	}
	now := time.Now().UTC()
	st.UpdatedAt = now
	applyStateTimestamps(&st, now)

	updates := map[string]any{
		"status":               st.Status,
		"repository_full_name": st.RepositoryFullName,
		"profile_name":         st.ProfileName,
		"runner_group":         st.RunnerGroup,
		"runner_name":          sanitizeRunnerName(st.RunnerName),
		"sandbox_id":           st.SandboxID,
		"process_pid":          st.ProcessPID,
		"assigned_job_id":      st.AssignedJobID,
		"assigned_job_name":    st.AssignedJobName,
		"error":                st.Error,
		"failure_stage":        st.FailureStage,
		"failure_reason":       st.FailureReason,
		"updated_at":           st.UpdatedAt,
		"version":              st.Version + 1,
		"creating_at":          zeroTimeToPointer(st.CreatingAt),
		"running_at":           zeroTimeToPointer(st.RunningAt),
		"stopping_at":          zeroTimeToPointer(st.StoppingAt),
		"completed_at":         zeroTimeToPointer(st.CompletedAt),
		"failed_at":            zeroTimeToPointer(st.FailedAt),
	}
	result := db.Model(&runnerRequestRecord{}).
		Where("id = ? AND version = ?", st.ID, st.Version).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

func (s *DBStore) ListStates() ([]RunnerState, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, err
	}
	var records []runnerRequestRecord
	if err := db.Order("queued_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	states := make([]RunnerState, 0, len(records))
	for _, record := range records {
		states = append(states, recordToState(record))
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].CreatedAt.After(states[j].CreatedAt)
	})
	return states, nil
}

func (s *DBStore) ActiveCount() (int, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return 0, err
	}
	var count int64
	if err := db.Model(&runnerRequestRecord{}).
		Where("status IN ?", []string{StatusQueued, StatusCreating, StatusRunning, StatusStopping}).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *DBStore) AppendLog(id, name string, data []byte) {
	if len(data) == 0 {
		return
	}
	db, err := s.dbOrEnsure()
	if err != nil {
		return
	}
	eventType, err := logEventType(name)
	if err != nil {
		return
	}
	_ = db.Create(&runnerEventRecord{
		RequestID: id,
		EventType: eventType,
		Message:   string(data),
		CreatedAt: time.Now().UTC(),
	}).Error
}

func (s *DBStore) ReadLog(id, name string, maxBytes int64) ([]byte, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, err
	}
	eventType, err := logEventType(name)
	if err != nil {
		return nil, err
	}
	var events []runnerEventRecord
	if err := db.Where("request_id = ? AND event_type = ?", sanitizeID(id), eventType).
		Order("id ASC").
		Find(&events).Error; err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, os.ErrNotExist
	}
	var buf bytes.Buffer
	for _, event := range events {
		buf.WriteString(event.Message)
	}
	data := buf.Bytes()
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		data = data[len(data)-int(maxBytes):]
	}
	return append([]byte(nil), data...), nil
}

func (s *DBStore) ListProfiles() ([]RunnerProfile, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, err
	}
	var records []runnerProfileRecord
	if err := db.Order("priority DESC, name ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	profiles := make([]RunnerProfile, 0, len(records))
	for _, record := range records {
		profile, err := recordToProfile(record)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *DBStore) GetProfile(name string) (RunnerProfile, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return RunnerProfile{}, err
	}
	var record runnerProfileRecord
	if err := db.First(&record, "name = ?", strings.TrimSpace(name)).Error; err != nil {
		return RunnerProfile{}, err
	}
	return recordToProfile(record)
}

func (s *DBStore) UpsertProfile(profile RunnerProfile) (RunnerProfile, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return RunnerProfile{}, err
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return RunnerProfile{}, fmt.Errorf("profile name is required")
	}
	labelsJSON, err := json.Marshal(profile.Labels)
	if err != nil {
		return RunnerProfile{}, err
	}
	now := time.Now().UTC()
	record := runnerProfileRecord{
		Name:           profile.Name,
		LabelsJSON:     string(labelsJSON),
		TemplateID:     profile.TemplateID,
		RunnerGroup:    profile.RunnerGroup,
		MaxConcurrency: profile.MaxConcurrency,
		MinIdle:        profile.MinIdle,
		Priority:       profile.Priority,
		Enabled:        profile.Enabled,
		CreatedAt:      profile.CreatedAt,
		UpdatedAt:      now,
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.Assignments(map[string]any{"labels_json": record.LabelsJSON, "template_id": record.TemplateID, "runner_group": record.RunnerGroup, "max_concurrency": record.MaxConcurrency, "min_idle": record.MinIdle, "priority": record.Priority, "enabled": record.Enabled, "updated_at": record.UpdatedAt}),
	}).Create(&record).Error; err != nil {
		return RunnerProfile{}, err
	}
	return s.GetProfile(record.Name)
}

func (s *DBStore) DeleteProfile(name string) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	return db.Delete(&runnerProfileRecord{}, "name = ?", strings.TrimSpace(name)).Error
}

func (s *DBStore) ListRepositoryPolicies() ([]RepositoryPolicy, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, err
	}
	var records []repositoryPolicyRecord
	if err := db.Order("repository_full_name ASC, profile_name ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	policies := make([]RepositoryPolicy, 0, len(records))
	for _, record := range records {
		policies = append(policies, RepositoryPolicy{
			ID:                 record.ID,
			RepositoryFullName: record.RepositoryFullName,
			ProfileName:        record.ProfileName,
			Enabled:            record.Enabled,
			CreatedAt:          record.CreatedAt,
		})
	}
	return policies, nil
}

func (s *DBStore) UpsertRepositoryPolicy(policy RepositoryPolicy) (RepositoryPolicy, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return RepositoryPolicy{}, err
	}
	policy.RepositoryFullName = strings.TrimSpace(policy.RepositoryFullName)
	policy.ProfileName = strings.TrimSpace(policy.ProfileName)
	if policy.RepositoryFullName == "" || policy.ProfileName == "" {
		return RepositoryPolicy{}, fmt.Errorf("repository_full_name and profile_name are required")
	}
	now := time.Now().UTC()
	if policy.ID == 0 {
		record := repositoryPolicyRecord{
			RepositoryFullName: policy.RepositoryFullName,
			ProfileName:        policy.ProfileName,
			Enabled:            policy.Enabled,
			CreatedAt:          now,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "repository_full_name"}, {Name: "profile_name"}},
			DoUpdates: clause.Assignments(map[string]any{"enabled": record.Enabled}),
		}).Create(&record).Error; err != nil {
			return RepositoryPolicy{}, err
		}
		var saved repositoryPolicyRecord
		if err := db.First(&saved, "repository_full_name = ? AND profile_name = ?", record.RepositoryFullName, record.ProfileName).Error; err != nil {
			return RepositoryPolicy{}, err
		}
		return RepositoryPolicy{ID: saved.ID, RepositoryFullName: saved.RepositoryFullName, ProfileName: saved.ProfileName, Enabled: saved.Enabled, CreatedAt: saved.CreatedAt}, nil
	}
	updates := map[string]any{"repository_full_name": policy.RepositoryFullName, "profile_name": policy.ProfileName, "enabled": policy.Enabled}
	if err := db.Model(&repositoryPolicyRecord{}).Where("id = ?", policy.ID).Updates(updates).Error; err != nil {
		return RepositoryPolicy{}, err
	}
	var saved repositoryPolicyRecord
	if err := db.First(&saved, "id = ?", policy.ID).Error; err != nil {
		return RepositoryPolicy{}, err
	}
	return RepositoryPolicy{ID: saved.ID, RepositoryFullName: saved.RepositoryFullName, ProfileName: saved.ProfileName, Enabled: saved.Enabled, CreatedAt: saved.CreatedAt}, nil
}

func (s *DBStore) DeleteRepositoryPolicy(id int64) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	return db.Delete(&repositoryPolicyRecord{}, "id = ?", id).Error
}

func (s *DBStore) MatchProfile(repositoryFullName string, labels []string) (ProfileMatch, error) {
	policies, err := s.ListRepositoryPolicies()
	if err != nil {
		return ProfileMatch{}, err
	}
	profiles, err := s.ListProfiles()
	if err != nil {
		return ProfileMatch{}, err
	}
	match := ProfileMatch{
		RepositoryFullName: repositoryFullName,
		Labels:             append([]string(nil), labels...),
	}
	allowed := map[string]bool{}
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		if repositoryMatches(policy.RepositoryFullName, repositoryFullName) {
			allowed[policy.ProfileName] = true
		}
	}
	if len(allowed) == 0 {
		match.Reason = "profile_not_allowed"
		return match, nil
	}
	var candidates []RunnerProfile
	for _, profile := range profiles {
		if !profile.Enabled || !allowed[profile.Name] {
			continue
		}
		if labelsMatch(labels, profile.Labels) {
			candidates = append(candidates, profile)
		}
	}
	if len(candidates) == 0 {
		match.Reason = "profile_labels_not_matched"
		return match, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		if len(candidates[i].Labels) != len(candidates[j].Labels) {
			return len(candidates[i].Labels) > len(candidates[j].Labels)
		}
		return candidates[i].Name < candidates[j].Name
	})
	match.Profile = &candidates[0]
	return match, nil
}

func (s *DBStore) dbOrEnsure() (*gorm.DB, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db, nil
}

func (s *DBStore) readRecord(id string) (runnerRequestRecord, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return runnerRequestRecord{}, err
	}
	var record runnerRequestRecord
	if err := db.First(&record, "id = ?", sanitizeID(id)).Error; err != nil {
		return runnerRequestRecord{}, err
	}
	return record, nil
}

func (s *DBStore) open() (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}
	switch s.opts.Backend {
	case BackendSQLite:
		dir := filepath.Dir(s.opts.DatabaseURL)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		return gorm.Open(sqlite.Open(s.opts.DatabaseURL), cfg)
	case BackendPostgres:
		return gorm.Open(postgres.Open(s.opts.DatabaseURL), cfg)
	default:
		return nil, fmt.Errorf("unsupported state backend: %s", s.opts.Backend)
	}
}

func (s *DBStore) migrate(db *gorm.DB) error {
	statements := sqliteMigrations
	if s.opts.Backend == BackendPostgres {
		statements = postgresMigrations
	}
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	for column, columnType := range map[string]string{
		"repository_full_name": "TEXT",
		"profile_name":         "TEXT",
		"runner_group":         "TEXT",
	} {
		if !db.Migrator().HasColumn(&runnerRequestRecord{}, column) {
			if err := db.Exec(fmt.Sprintf("ALTER TABLE runner_requests ADD COLUMN %s %s", column, columnType)).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func applyStateTimestamps(st *RunnerState, now time.Time) {
	switch st.Status {
	case StatusCreating:
		if st.CreatingAt.IsZero() {
			st.CreatingAt = now
		}
	case StatusRunning:
		if st.CreatingAt.IsZero() {
			st.CreatingAt = now
		}
		if st.RunningAt.IsZero() {
			st.RunningAt = now
		}
	case StatusStopping:
		if st.StoppingAt.IsZero() {
			st.StoppingAt = now
		}
	case StatusCompleted:
		if st.StoppingAt.IsZero() {
			st.StoppingAt = now
		}
		if st.CompletedAt.IsZero() {
			st.CompletedAt = now
		}
	case StatusFailed:
		if st.FailedAt.IsZero() {
			st.FailedAt = now
		}
	}
}

func recordToRequest(record runnerRequestRecord) (RunnerRequest, error) {
	var labels []string
	if err := json.Unmarshal([]byte(record.LabelsJSON), &labels); err != nil {
		return RunnerRequest{}, err
	}
	return RunnerRequest{
		ID:                 record.ID,
		Source:             record.Source,
		JobID:              pointerToInt64(record.WorkflowJobID),
		RepositoryFullName: record.RepositoryFullName,
		Labels:             labels,
		ProfileName:        record.ProfileName,
		RunnerGroup:        record.RunnerGroup,
		RunnerName:         record.RunnerName,
		CreatedAt:          record.QueuedAt,
	}, nil
}

func recordToState(record runnerRequestRecord) RunnerState {
	return RunnerState{
		ID:                 record.ID,
		Status:             record.Status,
		RepositoryFullName: record.RepositoryFullName,
		ProfileName:        record.ProfileName,
		RunnerGroup:        record.RunnerGroup,
		RunnerName:         record.RunnerName,
		SandboxID:          record.SandboxID,
		ProcessPID:         record.ProcessPID,
		AssignedJobID:      record.AssignedJobID,
		AssignedJobName:    record.AssignedJobName,
		Error:              record.Error,
		FailureStage:       record.FailureStage,
		FailureReason:      record.FailureReason,
		UpdatedAt:          record.UpdatedAt,
		CreatedAt:          record.QueuedAt,
		CreatingAt:         pointerToTime(record.CreatingAt),
		RunningAt:          pointerToTime(record.RunningAt),
		StoppingAt:         pointerToTime(record.StoppingAt),
		CompletedAt:        pointerToTime(record.CompletedAt),
		FailedAt:           pointerToTime(record.FailedAt),
		Version:            record.Version,
	}
}

func recordToProfile(record runnerProfileRecord) (RunnerProfile, error) {
	var labels []string
	if err := json.Unmarshal([]byte(record.LabelsJSON), &labels); err != nil {
		return RunnerProfile{}, err
	}
	return RunnerProfile{
		Name:           record.Name,
		Labels:         labels,
		TemplateID:     record.TemplateID,
		RunnerGroup:    record.RunnerGroup,
		MaxConcurrency: record.MaxConcurrency,
		MinIdle:        record.MinIdle,
		Priority:       record.Priority,
		Enabled:        record.Enabled,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}, nil
}

func zeroTimeToPointer(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	tt := t.UTC()
	return &tt
}

func pointerToTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func pointerToInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func repositoryMatches(pattern, repository string) bool {
	pattern = strings.TrimSpace(pattern)
	repository = strings.TrimSpace(repository)
	if pattern == "" || repository == "" {
		return false
	}
	if pattern == repository {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(repository, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func labelsMatch(jobLabels, required []string) bool {
	if len(required) == 0 {
		return true
	}
	available := map[string]bool{}
	for _, label := range jobLabels {
		available[strings.ToLower(strings.TrimSpace(label))] = true
	}
	for _, label := range required {
		if !available[strings.ToLower(strings.TrimSpace(label))] {
			return false
		}
	}
	return true
}

func logEventType(name string) (string, error) {
	switch filepath.Base(name) {
	case "control.log":
		return "control_log", nil
	case "stdout.log":
		return "stdout_log", nil
	case "stderr.log":
		return "stderr_log", nil
	default:
		return "", fmt.Errorf("unsupported log name")
	}
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.ReplaceAll(id, "\\", "-")
	id = strings.ReplaceAll(id, "..", "-")
	return id
}

func sanitizeRunnerName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	return name
}

var sqliteMigrations = []string{
	`CREATE TABLE IF NOT EXISTS runner_requests (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL,
		workflow_job_id INTEGER,
		repository_full_name TEXT,
		labels_json TEXT NOT NULL,
		profile_name TEXT,
		runner_group TEXT,
		runner_name TEXT NOT NULL,
		status TEXT NOT NULL,
		failure_stage TEXT,
		failure_reason TEXT,
		sandbox_id TEXT,
		process_pid INTEGER,
		assigned_job_id INTEGER,
		assigned_job_name TEXT,
		error TEXT,
		github_payload_json TEXT,
		queued_at TIMESTAMP NOT NULL,
		creating_at TIMESTAMP,
		running_at TIMESTAMP,
		stopping_at TIMESTAMP,
		completed_at TIMESTAMP,
		failed_at TIMESTAMP,
		updated_at TIMESTAMP NOT NULL,
		version INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS runner_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		stage TEXT,
		message TEXT,
		payload_json TEXT,
		created_at TIMESTAMP NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS runner_profiles (
		name TEXT PRIMARY KEY,
		labels_json TEXT NOT NULL,
		template_id TEXT NOT NULL,
		runner_group TEXT,
		max_concurrency INTEGER NOT NULL,
		min_idle INTEGER NOT NULL DEFAULT 0,
		priority INTEGER NOT NULL DEFAULT 0,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS repository_policies (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repository_full_name TEXT NOT NULL,
		profile_name TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMP NOT NULL
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_runner_requests_workflow_job_id ON runner_requests(workflow_job_id);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_requests_status_updated ON runner_requests(status, updated_at, queued_at);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_events_request_created ON runner_events(request_id, created_at);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_repository_policies_repository_profile ON repository_policies(repository_full_name, profile_name);`,
}

var postgresMigrations = []string{
	`CREATE TABLE IF NOT EXISTS runner_requests (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL,
		workflow_job_id BIGINT,
		repository_full_name TEXT,
		labels_json TEXT NOT NULL,
		profile_name TEXT,
		runner_group TEXT,
		runner_name TEXT NOT NULL,
		status TEXT NOT NULL,
		failure_stage TEXT,
		failure_reason TEXT,
		sandbox_id TEXT,
		process_pid BIGINT,
		assigned_job_id BIGINT,
		assigned_job_name TEXT,
		error TEXT,
		github_payload_json TEXT,
		queued_at TIMESTAMP NOT NULL,
		creating_at TIMESTAMP,
		running_at TIMESTAMP,
		stopping_at TIMESTAMP,
		completed_at TIMESTAMP,
		failed_at TIMESTAMP,
		updated_at TIMESTAMP NOT NULL,
		version BIGINT NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS runner_events (
		id BIGSERIAL PRIMARY KEY,
		request_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		stage TEXT,
		message TEXT,
		payload_json TEXT,
		created_at TIMESTAMP NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS runner_profiles (
		name TEXT PRIMARY KEY,
		labels_json TEXT NOT NULL,
		template_id TEXT NOT NULL,
		runner_group TEXT,
		max_concurrency INTEGER NOT NULL,
		min_idle INTEGER NOT NULL DEFAULT 0,
		priority INTEGER NOT NULL DEFAULT 0,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS repository_policies (
		id BIGSERIAL PRIMARY KEY,
		repository_full_name TEXT NOT NULL,
		profile_name TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMP NOT NULL
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_runner_requests_workflow_job_id ON runner_requests(workflow_job_id);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_requests_status_updated ON runner_requests(status, updated_at, queued_at);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_events_request_created ON runner_events(request_id, created_at);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_repository_policies_repository_profile ON repository_policies(repository_full_name, profile_name);`,
}
