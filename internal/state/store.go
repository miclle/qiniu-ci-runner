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
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	JobID      int64     `json:"job_id,omitempty"`
	Labels     []string  `json:"labels"`
	RunnerName string    `json:"runner_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type RunnerState struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	RunnerName      string    `json:"runner_name"`
	SandboxID       string    `json:"sandbox_id,omitempty"`
	ProcessPID      uint32    `json:"process_pid,omitempty"`
	AssignedJobID   int64     `json:"assigned_job_id,omitempty"`
	AssignedJobName string    `json:"assigned_job_name,omitempty"`
	Error           string    `json:"error,omitempty"`
	FailureStage    string    `json:"failure_stage,omitempty"`
	FailureReason   string    `json:"failure_reason,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	CreatedAt       time.Time `json:"created_at"`
	CreatingAt      time.Time `json:"creating_at,omitempty"`
	RunningAt       time.Time `json:"running_at,omitempty"`
	StoppingAt      time.Time `json:"stopping_at,omitempty"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	FailedAt        time.Time `json:"failed_at,omitempty"`
	Version         int64     `json:"version"`
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
	ID                string     `gorm:"column:id;primaryKey"`
	Source            string     `gorm:"column:source"`
	WorkflowJobID     *int64     `gorm:"column:workflow_job_id"`
	LabelsJSON        string     `gorm:"column:labels_json"`
	RunnerName        string     `gorm:"column:runner_name"`
	Status            string     `gorm:"column:status"`
	FailureStage      string     `gorm:"column:failure_stage"`
	FailureReason     string     `gorm:"column:failure_reason"`
	SandboxID         string     `gorm:"column:sandbox_id"`
	ProcessPID        uint32     `gorm:"column:process_pid"`
	AssignedJobID     int64      `gorm:"column:assigned_job_id"`
	AssignedJobName   string     `gorm:"column:assigned_job_name"`
	Error             string     `gorm:"column:error"`
	GitHubPayloadJSON string     `gorm:"column:github_payload_json"`
	QueuedAt          time.Time  `gorm:"column:queued_at"`
	CreatingAt        *time.Time `gorm:"column:creating_at"`
	RunningAt         *time.Time `gorm:"column:running_at"`
	StoppingAt        *time.Time `gorm:"column:stopping_at"`
	CompletedAt       *time.Time `gorm:"column:completed_at"`
	FailedAt          *time.Time `gorm:"column:failed_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
	Version           int64      `gorm:"column:version"`
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
		ID:                req.ID,
		Source:            req.Source,
		LabelsJSON:        string(labelsJSON),
		RunnerName:        req.RunnerName,
		Status:            StatusQueued,
		GitHubPayloadJSON: string(payload),
		QueuedAt:          req.CreatedAt,
		UpdatedAt:         now,
		Version:           0,
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
		"status":            st.Status,
		"runner_name":       sanitizeRunnerName(st.RunnerName),
		"sandbox_id":        st.SandboxID,
		"process_pid":       st.ProcessPID,
		"assigned_job_id":   st.AssignedJobID,
		"assigned_job_name": st.AssignedJobName,
		"error":             st.Error,
		"failure_stage":     st.FailureStage,
		"failure_reason":    st.FailureReason,
		"updated_at":        st.UpdatedAt,
		"version":           st.Version + 1,
		"creating_at":       zeroTimeToPointer(st.CreatingAt),
		"running_at":        zeroTimeToPointer(st.RunningAt),
		"stopping_at":       zeroTimeToPointer(st.StoppingAt),
		"completed_at":      zeroTimeToPointer(st.CompletedAt),
		"failed_at":         zeroTimeToPointer(st.FailedAt),
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
		ID:         record.ID,
		Source:     record.Source,
		JobID:      pointerToInt64(record.WorkflowJobID),
		Labels:     labels,
		RunnerName: record.RunnerName,
		CreatedAt:  record.QueuedAt,
	}, nil
}

func recordToState(record runnerRequestRecord) RunnerState {
	return RunnerState{
		ID:              record.ID,
		Status:          record.Status,
		RunnerName:      record.RunnerName,
		SandboxID:       record.SandboxID,
		ProcessPID:      record.ProcessPID,
		AssignedJobID:   record.AssignedJobID,
		AssignedJobName: record.AssignedJobName,
		Error:           record.Error,
		FailureStage:    record.FailureStage,
		FailureReason:   record.FailureReason,
		UpdatedAt:       record.UpdatedAt,
		CreatedAt:       record.QueuedAt,
		CreatingAt:      pointerToTime(record.CreatingAt),
		RunningAt:       pointerToTime(record.RunningAt),
		StoppingAt:      pointerToTime(record.StoppingAt),
		CompletedAt:     pointerToTime(record.CompletedAt),
		FailedAt:        pointerToTime(record.FailedAt),
		Version:         record.Version,
	}
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
		labels_json TEXT NOT NULL,
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
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_runner_requests_workflow_job_id ON runner_requests(workflow_job_id);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_requests_status_updated ON runner_requests(status, updated_at, queued_at);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_events_request_created ON runner_events(request_id, created_at);`,
}

var postgresMigrations = []string{
	`CREATE TABLE IF NOT EXISTS runner_requests (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL,
		workflow_job_id BIGINT,
		labels_json TEXT NOT NULL,
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
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_runner_requests_workflow_job_id ON runner_requests(workflow_job_id);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_requests_status_updated ON runner_requests(status, updated_at, queued_at);`,
	`CREATE INDEX IF NOT EXISTS idx_runner_events_request_created ON runner_events(request_id, created_at);`,
}
