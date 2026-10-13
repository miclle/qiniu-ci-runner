package state

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxDeploymentEventMessage = 64 << 10
	maxDeploymentEvents       = 2000
	maxDeploymentEventBytes   = 8 << 20
)

var deploymentRepositoryPattern = regexp.MustCompile(`^[^/\s]+/[^/\s]+$`)

func (s *DBStore) CreateDeployment(deployment Deployment) (Deployment, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return Deployment{}, err
	}
	return s.createDeployment(db, deployment)
}

// CreateDeploymentWithScopeLimit inserts a deployment while holding the
// durable scope anchor row. This keeps concurrent admissions serialized across
// runnerd processes that share a database.
func (s *DBStore) CreateDeploymentWithScopeLimit(deployment Deployment, maxConcurrent int) (Deployment, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return Deployment{}, err
	}
	var created Deployment
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := checkDeploymentQuota(tx, deployment, maxConcurrent, ""); err != nil {
			return err
		}
		var createErr error
		created, createErr = s.createDeployment(tx, deployment)
		return createErr
	})
	return created, err
}

func (s *DBStore) createDeployment(db *gorm.DB, deployment Deployment) (Deployment, error) {
	deployment, err := normalizeDeployment(deployment)
	if err != nil {
		return Deployment{}, err
	}
	now := time.Now().UTC()
	if deployment.CreatedAt.IsZero() {
		deployment.CreatedAt = now
	}
	if deployment.UpdatedAt.IsZero() {
		deployment.UpdatedAt = deployment.CreatedAt
	}
	if deployment.Status == "" {
		deployment.Status = DeploymentStatusQueued
	}
	record := deploymentRecord{
		ID:                     deployment.ID,
		AccountID:              deployment.AccountID,
		ScopeType:              deployment.ScopeType,
		ScopeID:                deployment.ScopeID,
		GitHubInstallationID:   deployment.GitHubInstallationID,
		RepositoryID:           deployment.RepositoryID,
		RepositoryFullName:     deployment.RepositoryFullName,
		Ref:                    deployment.Ref,
		CommitSHA:              deployment.CommitSHA,
		ConfigPath:             deployment.ConfigPath,
		SnapshotVersion:        deployment.SnapshotVersion,
		SpecHash:               deployment.SpecHash,
		SpecJSONEncrypted:      deployment.SpecJSONEncrypted,
		Status:                 deployment.Status,
		SandboxID:              deployment.SandboxID,
		Attempt:                deployment.Attempt,
		RuntimeID:              deployment.RuntimeID,
		ProcessID:              deployment.ProcessID,
		ProcessTag:             deployment.ProcessTag,
		SandboxAPIURL:          deployment.SandboxAPIURL,
		SandboxAPIKeyEncrypted: deployment.SandboxAPIKeyEncrypted,
		SandboxConfigSource:    deployment.SandboxConfigSource,
		SandboxRegion:          deployment.SandboxRegion,
		DeploymentTemplateID:   deployment.DeploymentTemplateID,
		Endpoint:               deployment.Endpoint,
		FailureStage:           deployment.FailureStage,
		FailureReason:          deployment.FailureReason,
		Error:                  deployment.Error,
		ExpiresAt:              deployment.ExpiresAt,
		CreatedAt:              deployment.CreatedAt,
		UpdatedAt:              deployment.UpdatedAt,
		Version:                deployment.Version,
	}
	applyDeploymentTimestamps(&record, now)
	if err := db.Create(&record).Error; err != nil {
		return Deployment{}, err
	}
	return deploymentFromRecord(record), nil
}

// checkDeploymentQuota uses current locking reads, including under MySQL's
// repeatable-read isolation after an earlier idempotency lookup.
func checkDeploymentQuota(db *gorm.DB, deployment Deployment, maxConcurrent int, excludeID string) error {
	if maxConcurrent < 1 {
		maxConcurrent = 3
	}
	if err := lockDeploymentScope(db, deployment); err != nil {
		return err
	}
	query := db.Model(&deploymentRecord{}).Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope_type = ? AND scope_id = ? AND (status IN ? OR (status = ? AND sandbox_id <> ''))", deployment.ScopeType, deployment.ScopeID, []string{DeploymentStatusQueued, DeploymentStatusPlanning, DeploymentStatusCreating, DeploymentStatusRunning, DeploymentStatusStopping}, DeploymentStatusFailed)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var active []deploymentRecord
	if err := query.Limit(maxConcurrent).Find(&active).Error; err != nil {
		return err
	}
	if len(active) >= maxConcurrent {
		return ErrDeploymentQuotaExceeded
	}
	return nil
}

func lockDeploymentScope(db *gorm.DB, deployment Deployment) error {
	var err error
	switch deployment.ScopeType {
	case AccountScopeTypeAccount:
		var account accountRecord
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, "id = ?", deployment.ScopeID).Error
	case AccountScopeTypeGitHubInstall:
		var installation githubInstallationRecord
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&installation, "installation_id = ?", deployment.ScopeID).Error
	default:
		return fmt.Errorf("invalid deployment scope")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *DBStore) GetDeployment(id string) (Deployment, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return Deployment{}, err
	}
	var record deploymentRecord
	if err := db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, err
	}
	return deploymentFromRecord(record), nil
}

func (s *DBStore) ListDeploymentsForAccount(accountID int64, options DeploymentListOptions) ([]Deployment, int64, error) {
	return s.listDeploymentsForAccount(accountID, nil, false, options)
}

func (s *DBStore) ListAuthorizedDeploymentsForAccount(accountID int64, access []DeploymentRepositoryAccess, options DeploymentListOptions) ([]Deployment, int64, error) {
	return s.listDeploymentsForAccount(accountID, access, true, options)
}

func (s *DBStore) listDeploymentsForAccount(accountID int64, access []DeploymentRepositoryAccess, authorized bool, options DeploymentListOptions) ([]Deployment, int64, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, 0, err
	}
	if accountID <= 0 {
		return nil, 0, fmt.Errorf("account id must be positive")
	}
	limit, offset, err := normalizeDeploymentPagination(options)
	if err != nil {
		return nil, 0, err
	}
	query := db.Model(&deploymentRecord{}).Where("account_id = ?", accountID)
	if authorized {
		if len(access) == 0 {
			return []Deployment{}, 0, nil
		}
		allowed := db.Where("1 = 0")
		for _, item := range access {
			if item.InstallationID > 0 && item.RepositoryID > 0 && strings.TrimSpace(item.RepositoryFullName) != "" {
				allowed = allowed.Or("github_installation_id = ? AND repository_id = ? AND repository_full_name = ?", item.InstallationID, item.RepositoryID, strings.TrimSpace(item.RepositoryFullName))
			}
		}
		query = query.Where(allowed)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []deploymentRecord
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	deployments := make([]Deployment, 0, len(records))
	for _, record := range records {
		deployments = append(deployments, deploymentFromRecord(record))
	}
	return deployments, total, nil
}

func (s *DBStore) ListActiveDeployments() ([]Deployment, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, err
	}
	var records []deploymentRecord
	if err := db.Where("status IN ? OR (status = ? AND sandbox_id <> '')", []string{
		DeploymentStatusQueued,
		DeploymentStatusPlanning,
		DeploymentStatusCreating,
		DeploymentStatusRunning,
		DeploymentStatusStopping,
	}, DeploymentStatusFailed).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	deployments := make([]Deployment, 0, len(records))
	for _, record := range records {
		deployments = append(deployments, deploymentFromRecord(record))
	}
	return deployments, nil
}

func (s *DBStore) UpdateDeployment(deployment Deployment) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	deployment, err = normalizeDeployment(deployment)
	if err != nil {
		return err
	}
	if deployment.Status == "" {
		return fmt.Errorf("deployment status is required")
	}
	if deployment.Version < 0 {
		return ErrConflict
	}
	now := time.Now().UTC()
	deployment.UpdatedAt = now
	timestampRecord := deploymentRecord{
		Status:      deployment.Status,
		PlanningAt:  zeroTimeToPointer(deployment.PlanningAt),
		CreatingAt:  zeroTimeToPointer(deployment.CreatingAt),
		RunningAt:   zeroTimeToPointer(deployment.RunningAt),
		StoppingAt:  zeroTimeToPointer(deployment.StoppingAt),
		CompletedAt: zeroTimeToPointer(deployment.CompletedAt),
		FailedAt:    zeroTimeToPointer(deployment.FailedAt),
	}
	applyDeploymentTimestamps(&timestampRecord, now)
	updates := map[string]any{
		"status":                    deployment.Status,
		"sandbox_id":                strings.TrimSpace(deployment.SandboxID),
		"attempt":                   deployment.Attempt,
		"runtime_id":                strings.TrimSpace(deployment.RuntimeID),
		"process_id":                strings.TrimSpace(deployment.ProcessID),
		"process_tag":               strings.TrimSpace(deployment.ProcessTag),
		"sandbox_api_url":           strings.TrimSpace(deployment.SandboxAPIURL),
		"sandbox_api_key_encrypted": strings.TrimSpace(deployment.SandboxAPIKeyEncrypted),
		"sandbox_config_source":     strings.TrimSpace(deployment.SandboxConfigSource),
		"sandbox_region":            strings.TrimSpace(deployment.SandboxRegion),
		"deployment_template_id":    strings.TrimSpace(deployment.DeploymentTemplateID),
		"endpoint":                  strings.TrimSpace(deployment.Endpoint),
		"failure_stage":             strings.TrimSpace(deployment.FailureStage),
		"failure_reason":            strings.TrimSpace(deployment.FailureReason),
		"error":                     deployment.Error,
		"expires_at":                deployment.ExpiresAt,
		"planning_at":               timestampRecord.PlanningAt,
		"creating_at":               timestampRecord.CreatingAt,
		"running_at":                timestampRecord.RunningAt,
		"stopping_at":               timestampRecord.StoppingAt,
		"completed_at":              timestampRecord.CompletedAt,
		"failed_at":                 timestampRecord.FailedAt,
		"updated_at":                deployment.UpdatedAt,
		"version":                   deployment.Version + 1,
	}
	result := db.Model(&deploymentRecord{}).Where("id = ? AND version = ?", deployment.ID, deployment.Version).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

// RetryDeployment requeues a terminal deployment while preserving its
// immutable source and plan evidence. Any persisted process/Sandbox evidence
// must already have been cleaned before this method is called.
func (s *DBStore) RetryDeployment(deployment Deployment, expiresAt time.Time) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	return retryDeployment(db, deployment, expiresAt)
}

// RetryDeploymentWithScopeLimit applies the same admission limit used for a
// fresh deployment before re-queueing a terminal record.
func (s *DBStore) RetryDeploymentWithScopeLimit(deployment Deployment, expiresAt time.Time, maxConcurrent int) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := checkDeploymentQuota(tx, deployment, maxConcurrent, deployment.ID); err != nil {
			return err
		}
		return retryDeployment(tx, deployment, expiresAt)
	})
}

func retryDeployment(db *gorm.DB, deployment Deployment, expiresAt time.Time) error {
	if deployment.Status != DeploymentStatusFailed && deployment.Status != DeploymentStatusCompleted && deployment.Status != DeploymentStatusExpired {
		return ErrRetryNotAllowed
	}
	if strings.TrimSpace(deployment.SandboxID) != "" || strings.TrimSpace(deployment.RuntimeID) != "" || strings.TrimSpace(deployment.ProcessID) != "" || strings.TrimSpace(deployment.ProcessTag) != "" {
		return ErrConflict
	}
	if !deployment.ExpiresAt.IsZero() && expiresAt.IsZero() {
		return fmt.Errorf("deployment expiry is required")
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(time.Hour)
	}
	result := db.Model(&deploymentRecord{}).Where("id = ? AND version = ? AND status IN ?", deployment.ID, deployment.Version, []string{DeploymentStatusFailed, DeploymentStatusCompleted, DeploymentStatusExpired}).Updates(map[string]any{
		"sandbox_api_url":           deployment.SandboxAPIURL,
		"sandbox_api_key_encrypted": deployment.SandboxAPIKeyEncrypted,
		"sandbox_config_source":     deployment.SandboxConfigSource,
		"sandbox_region":            deployment.SandboxRegion,
		"status":                    DeploymentStatusQueued,
		"sandbox_id":                "",
		"runtime_id":                "",
		"process_id":                "",
		"process_tag":               "",
		"endpoint":                  "",
		"failure_stage":             "",
		"failure_reason":            "",
		"error":                     "",
		"expires_at":                expiresAt,
		"planning_at":               nil,
		"creating_at":               nil,
		"running_at":                nil,
		"stopping_at":               nil,
		"completed_at":              nil,
		"failed_at":                 nil,
		"updated_at":                time.Now().UTC(),
		"version":                   deployment.Version + 1,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

// PruneDeployments removes only terminal records whose Sandbox evidence has
// already been cleared and whose retention window has elapsed. Audit events
// are intentionally retained as the long-lived control-plane history.
func (s *DBStore) PruneDeployments(before time.Time, limit int) (int, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, fmt.Errorf("deployment retention cutoff is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var records []deploymentRecord
	if err := db.Where("status IN ? AND sandbox_id = '' AND updated_at < ?", []string{DeploymentStatusCompleted, DeploymentStatusExpired, DeploymentStatusFailed}, before.UTC()).Order("updated_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	pruned := 0
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, record := range records {
			result := tx.Where("id = ? AND version = ? AND status IN ? AND sandbox_id = '' AND updated_at < ?", record.ID, record.Version, []string{DeploymentStatusCompleted, DeploymentStatusExpired, DeploymentStatusFailed}, before.UTC()).Delete(&deploymentRecord{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			if err := tx.Where("deployment_id = ?", record.ID).Delete(&deploymentEventRecord{}).Error; err != nil {
				return err
			}
			pruned++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pruned, nil
}

func (s *DBStore) AppendDeploymentEvent(event DeploymentEvent) error {
	db, err := s.dbOrEnsure()
	if err != nil {
		return err
	}
	event.DeploymentID = strings.TrimSpace(event.DeploymentID)
	event.EventType = strings.TrimSpace(event.EventType)
	event.Stage = strings.TrimSpace(event.Stage)
	event.Stream = strings.TrimSpace(event.Stream)
	if event.DeploymentID == "" || event.EventType == "" || len(event.DeploymentID) > 128 || len(event.EventType) > 64 || len(event.Stage) > 64 || len(event.Stream) > 32 {
		return fmt.Errorf("deployment event identity is invalid")
	}
	if strings.IndexFunc(event.DeploymentID, unicode.IsControl) >= 0 || strings.IndexFunc(event.EventType, unicode.IsControl) >= 0 || strings.IndexFunc(event.Stage, unicode.IsControl) >= 0 || strings.IndexFunc(event.Stream, unicode.IsControl) >= 0 {
		return fmt.Errorf("deployment event identity is invalid")
	}
	if len(event.Message) > maxDeploymentEventMessage {
		event.Message = event.Message[:maxDeploymentEventMessage]
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	record := deploymentEventRecord{
		DeploymentID: event.DeploymentID,
		Attempt:      event.Attempt,
		EventType:    event.EventType,
		Stage:        event.Stage,
		Stream:       event.Stream,
		Message:      event.Message,
		CreatedAt:    event.CreatedAt,
	}
	return db.Transaction(func(tx *gorm.DB) error {
		// Serialize retention for one deployment so concurrent output callbacks
		// cannot both observe the same budget and grow the timeline past its cap.
		var owner deploymentRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.DeploymentID).First(&owner).Error; err != nil {
			return err
		}
		for {
			var count int64
			if err := tx.Model(&deploymentEventRecord{}).Where("deployment_id = ?", event.DeploymentID).Count(&count).Error; err != nil {
				return err
			}
			var bytes int64
			if err := tx.Model(&deploymentEventRecord{}).Where("deployment_id = ?", event.DeploymentID).Select("COALESCE(SUM(LENGTH(message)), 0)").Scan(&bytes).Error; err != nil {
				return err
			}
			if count < maxDeploymentEvents && bytes+int64(len(event.Message)) <= maxDeploymentEventBytes {
				break
			}
			var oldest deploymentEventRecord
			if err := tx.Where("deployment_id = ?", event.DeploymentID).Order("id ASC").First(&oldest).Error; err != nil {
				return err
			}
			if err := tx.Delete(&oldest).Error; err != nil {
				return err
			}
		}
		return tx.Create(&record).Error
	})
}

func (s *DBStore) ListDeploymentEvents(id string, beforeID int64, limit int) ([]DeploymentEvent, bool, error) {
	return s.listDeploymentEvents(id, beforeID, 0, limit)
}

func (s *DBStore) ListDeploymentEventsAfter(id string, afterID int64, limit int) ([]DeploymentEvent, bool, error) {
	return s.listDeploymentEvents(id, 0, afterID, limit)
}

func (s *DBStore) listDeploymentEvents(id string, beforeID, afterID int64, limit int) ([]DeploymentEvent, bool, error) {
	db, err := s.dbOrEnsure()
	if err != nil {
		return nil, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return nil, false, fmt.Errorf("deployment id is invalid")
	}
	limit = min(max(limit, 1), 500)
	query := db.Where("deployment_id = ?", id)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	order := "id DESC"
	if afterID > 0 {
		query = query.Where("id > ?", afterID)
		order = "id ASC"
	}
	var records []deploymentEventRecord
	if err := query.Order(order).Limit(limit + 1).Find(&records).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	events := make([]DeploymentEvent, len(records))
	for i := range records {
		index := i
		if afterID == 0 {
			index = len(records) - 1 - i
		}
		record := records[index]
		events[i] = DeploymentEvent(record)
	}
	return events, hasMore, nil
}

func normalizeDeployment(deployment Deployment) (Deployment, error) {
	deployment.ID = strings.TrimSpace(deployment.ID)
	if deployment.ID == "" || len(deployment.ID) > 128 || strings.IndexFunc(deployment.ID, unicode.IsControl) >= 0 {
		return Deployment{}, fmt.Errorf("deployment id is invalid")
	}
	if deployment.AccountID <= 0 || deployment.ScopeID <= 0 || deployment.GitHubInstallationID <= 0 || deployment.RepositoryID <= 0 {
		return Deployment{}, fmt.Errorf("deployment ownership is invalid")
	}
	deployment.ScopeType = strings.TrimSpace(deployment.ScopeType)
	if deployment.ScopeType != AccountScopeTypeAccount && deployment.ScopeType != AccountScopeTypeGitHubInstall {
		return Deployment{}, fmt.Errorf("deployment scope is invalid")
	}
	deployment.RepositoryFullName = strings.TrimSpace(deployment.RepositoryFullName)
	if !deploymentRepositoryPattern.MatchString(deployment.RepositoryFullName) || strings.IndexFunc(deployment.RepositoryFullName, unicode.IsControl) >= 0 {
		return Deployment{}, fmt.Errorf("deployment repository is invalid")
	}
	deployment.Ref = strings.TrimSpace(deployment.Ref)
	if deployment.Ref == "" || len(deployment.Ref) > 256 || strings.IndexFunc(deployment.Ref, unicode.IsControl) >= 0 {
		return Deployment{}, fmt.Errorf("deployment ref is invalid")
	}
	deployment.CommitSHA = strings.TrimSpace(deployment.CommitSHA)
	if !validDeploymentCommit(deployment.CommitSHA) {
		return Deployment{}, fmt.Errorf("deployment commit is invalid")
	}
	deployment.ConfigPath = path.Clean(strings.TrimSpace(deployment.ConfigPath))
	if deployment.ConfigPath == "." || len(deployment.ConfigPath) > 1024 || strings.IndexFunc(deployment.ConfigPath, unicode.IsControl) >= 0 || path.IsAbs(deployment.ConfigPath) || strings.HasPrefix(deployment.ConfigPath, "../") || deployment.ConfigPath == ".." || strings.Contains(deployment.ConfigPath, "\\") {
		return Deployment{}, fmt.Errorf("deployment config path is invalid")
	}
	if deployment.SnapshotVersion <= 0 || strings.TrimSpace(deployment.SpecHash) == "" || strings.TrimSpace(deployment.SpecJSONEncrypted) == "" {
		return Deployment{}, fmt.Errorf("deployment plan evidence is incomplete")
	}
	if !validDeploymentStatus(deployment.Status) && deployment.Status != "" {
		return Deployment{}, fmt.Errorf("deployment status is invalid")
	}
	if deployment.ExpiresAt.IsZero() {
		return Deployment{}, fmt.Errorf("deployment expiry is required")
	}
	return deployment, nil
}

func normalizeDeploymentPagination(options DeploymentListOptions) (int, int, error) {
	limit := options.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 0 || limit > 500 || options.Offset < 0 {
		return 0, 0, fmt.Errorf("deployment pagination is invalid")
	}
	return limit, options.Offset, nil
}

func validDeploymentStatus(status string) bool {
	switch status {
	case DeploymentStatusQueued, DeploymentStatusPlanning, DeploymentStatusCreating, DeploymentStatusRunning, DeploymentStatusStopping, DeploymentStatusCompleted, DeploymentStatusExpired, DeploymentStatusFailed:
		return true
	default:
		return false
	}
}

func validDeploymentCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) > 0 && value == strings.ToLower(value)
}

func applyDeploymentTimestamps(record *deploymentRecord, now time.Time) {
	set := func(value **time.Time) {
		if *value == nil {
			*value = &now
		}
	}
	switch record.Status {
	case DeploymentStatusPlanning:
		set(&record.PlanningAt)
	case DeploymentStatusCreating:
		set(&record.PlanningAt)
		set(&record.CreatingAt)
	case DeploymentStatusRunning:
		set(&record.PlanningAt)
		set(&record.CreatingAt)
		set(&record.RunningAt)
	case DeploymentStatusStopping:
		set(&record.StoppingAt)
	case DeploymentStatusCompleted, DeploymentStatusExpired:
		if record.CompletedAt == nil {
			record.CompletedAt = &now
		}
	case DeploymentStatusFailed:
		if record.FailedAt == nil {
			record.FailedAt = &now
		}
	}
}

func deploymentFromRecord(record deploymentRecord) Deployment {
	return Deployment{
		ID:                     record.ID,
		AccountID:              record.AccountID,
		ScopeType:              record.ScopeType,
		ScopeID:                record.ScopeID,
		GitHubInstallationID:   record.GitHubInstallationID,
		RepositoryID:           record.RepositoryID,
		RepositoryFullName:     record.RepositoryFullName,
		Ref:                    record.Ref,
		CommitSHA:              record.CommitSHA,
		ConfigPath:             record.ConfigPath,
		SnapshotVersion:        record.SnapshotVersion,
		SpecHash:               record.SpecHash,
		SpecJSONEncrypted:      record.SpecJSONEncrypted,
		Status:                 record.Status,
		SandboxID:              record.SandboxID,
		Attempt:                record.Attempt,
		RuntimeID:              record.RuntimeID,
		ProcessID:              record.ProcessID,
		ProcessTag:             record.ProcessTag,
		SandboxAPIURL:          record.SandboxAPIURL,
		SandboxAPIKeyEncrypted: record.SandboxAPIKeyEncrypted,
		SandboxConfigSource:    record.SandboxConfigSource,
		SandboxRegion:          record.SandboxRegion,
		DeploymentTemplateID:   record.DeploymentTemplateID,
		Endpoint:               record.Endpoint,
		FailureStage:           record.FailureStage,
		FailureReason:          record.FailureReason,
		Error:                  record.Error,
		ExpiresAt:              record.ExpiresAt,
		CreatedAt:              record.CreatedAt,
		PlanningAt:             pointerToTime(record.PlanningAt),
		CreatingAt:             pointerToTime(record.CreatingAt),
		RunningAt:              pointerToTime(record.RunningAt),
		StoppingAt:             pointerToTime(record.StoppingAt),
		CompletedAt:            pointerToTime(record.CompletedAt),
		FailedAt:               pointerToTime(record.FailedAt),
		UpdatedAt:              record.UpdatedAt,
		Version:                record.Version,
	}
}
