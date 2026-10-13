package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	deploy "github.com/miclle/deployd"
	deployconfig "github.com/qiniu/ci-runner/internal/deployment/config"
	deployruntime "github.com/qiniu/ci-runner/internal/deployment/runtime"
	"github.com/qiniu/ci-runner/internal/github"
	"github.com/qiniu/ci-runner/internal/state"
)

const (
	defaultDeploymentTTL = time.Hour
	minDeploymentTTL     = 5 * time.Minute
	maxDeploymentTTL     = 24 * time.Hour
	maxDeploymentBody    = 1 << 20
)

type createDeploymentRequest struct {
	InstallationID     int64  `json:"installation_id"`
	RepositoryFullName string `json:"repository_full_name"`
	Ref                string `json:"ref"`
	Source             string `json:"source"`
	ConfigPath         string `json:"config_path"`
	TTLSeconds         int64  `json:"ttl_seconds"`
	TemplateID         string `json:"template_id"`
}

type deploymentResponse struct {
	ID                   string    `json:"id"`
	ScopeType            string    `json:"scope_type"`
	ScopeID              int64     `json:"scope_id"`
	GitHubInstallationID int64     `json:"github_installation_id"`
	RepositoryID         int64     `json:"repository_id"`
	RepositoryFullName   string    `json:"repository_full_name"`
	Ref                  string    `json:"ref,omitempty"`
	Source               string    `json:"source,omitempty"`
	CommitSHA            string    `json:"commit_sha"`
	ConfigPath           string    `json:"config_path"`
	SnapshotVersion      int       `json:"snapshot_version"`
	SpecHash             string    `json:"spec_hash"`
	Status               string    `json:"status"`
	TemplateID           string    `json:"template_id,omitempty"`
	SandboxID            string    `json:"sandbox_id,omitempty"`
	SandboxConfigSource  string    `json:"sandbox_config_source,omitempty"`
	SandboxRegion        string    `json:"sandbox_region,omitempty"`
	Endpoint             string    `json:"endpoint,omitempty"`
	EndpointAccess       string    `json:"endpoint_access,omitempty"`
	FailureStage         string    `json:"failure_stage,omitempty"`
	FailureReason        string    `json:"failure_reason,omitempty"`
	ExpiresAt            time.Time `json:"expires_at"`
	CreatedAt            time.Time `json:"created_at"`
	PlanningAt           time.Time `json:"planning_at,omitempty"`
	CreatingAt           time.Time `json:"creating_at,omitempty"`
	RunningAt            time.Time `json:"running_at,omitempty"`
	StoppingAt           time.Time `json:"stopping_at,omitempty"`
	CompletedAt          time.Time `json:"completed_at,omitempty"`
	FailedAt             time.Time `json:"failed_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
	Version              int64     `json:"version"`
}

func (s *Server) handleUserCreateDeployment(w http.ResponseWriter, r *http.Request) {
	session, account, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	var input createDeploymentRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeploymentBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_deployment", "invalid deployment payload")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeErrorCode(w, http.StatusBadRequest, "invalid_deployment", "invalid deployment payload")
		return
	}
	if err := validateCreateDeploymentRequest(input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_deployment", err.Error())
		return
	}
	input.Ref = normalizeDeploymentRef(input.Ref, input.Source)
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.RepositoryFullName = strings.TrimSpace(input.RepositoryFullName)
	input.ConfigPath = normalizeDeploymentConfigPath(input.ConfigPath)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 128 || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		writeErrorCode(w, http.StatusBadRequest, "invalid_deployment", "Idempotency-Key is invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	installation, found, err := s.githubInstallationForAccount(account.ID, input.InstallationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load GitHub installation")
		return
	}
	if !found {
		writeErrorCode(w, http.StatusNotFound, "github_installation_not_found", "GitHub installation is not linked to this account")
		return
	}
	if s.gh == nil {
		writeError(w, http.StatusBadGateway, "GitHub client is not configured")
		return
	}

	// A deployment is a Sandbox mutation. Organization installations remain
	// owner-managed even when the repository is visible through OAuth access.
	scope, ok := s.authorizedDeploymentScope(w, r, account.ID, installation)
	if !ok {
		return
	}

	token, err := s.githubUserAccessToken(account.ID)
	if err != nil {
		s.writeUserRepositoryAuthorizationError(w, err)
		return
	}
	repositories, err := s.gh.ListUserInstallationRepositoryDetails(r.Context(), token, installation.InstallationID)
	if err != nil {
		s.writeUserRepositoryAuthorizationError(w, err)
		return
	}
	repository, ok := deploymentRepositoryAccess(repositories, input.RepositoryFullName, installation)
	if !ok {
		writeErrorCode(w, http.StatusForbidden, "repository_access_forbidden", "repository is not accessible through this GitHub installation and user session")
		return
	}
	// Replay the original immutable plan only after current authorization. A
	// moving branch must not be resolved again for the same request key.
	deploymentID := newID()
	if key != "" {
		digest := sha256.Sum256(fmt.Appendf(nil, "deployment-request/v1:%d:%s", account.ID, key))
		deploymentID = hex.EncodeToString(digest[:])
		if s.writeDeploymentReplay(w, deploymentID, account.ID, installation.InstallationID, repository.ID, input) {
			return
		}
	}

	templateID := input.TemplateID
	if templateID == "" {
		templateID = strings.TrimSpace(s.cfg.DeploymentTemplateID)
	}
	if templateID == "" {
		writeErrorCode(w, http.StatusBadRequest, "deployment_template_required", "Select a Sandbox template before deploying")
		return
	}
	service, sandboxSnapshot, snapshotErr := s.sandboxServiceForDeployment(r.Context(), scope)
	if snapshotErr != nil {
		writeErrorCode(w, http.StatusConflict, "deployment_sandbox_not_configured", "Configure Sandbox credentials in this account or organization's Preferences before deploying")
		return
	}
	if err := service.ValidateTemplate(r.Context(), templateID); err != nil {
		writeErrorCode(w, http.StatusConflict, "deployment_template_unavailable", "The selected Sandbox template is unavailable or has no runnable default build")
		return
	}

	commitSHA, err := s.gh.ResolveCommit(r.Context(), repository.FullName, input.Ref)
	if err != nil {
		writeErrorCode(w, http.StatusBadGateway, "commit_resolution_failed", "failed to resolve repository ref")
		return
	}
	configData, err := s.gh.ReadRepositoryFile(r.Context(), repository.FullName, input.ConfigPath, commitSHA, deployconfig.MaxBytes)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "deployment_config_invalid", "failed to read deployment configuration")
		return
	}
	spec, err := deployconfig.Parse(configData)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "deployment_config_invalid", "invalid deployment configuration")
		return
	}
	plan, err := deploy.NewPlan(deploy.ResolvedSource{
		SourceID:  githubDeploymentSourceURL(repository.FullName),
		CommitSHA: commitSHA,
	}, spec)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "deployment_config_invalid", "invalid deployment execution plan")
		return
	}
	specJSON, err := json.Marshal(plan.Spec())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal deployment plan")
		return
	}
	encryptedSpec, err := encryptSecret(string(specJSON), s.cfg.AuthEncryptionKey.Value())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt deployment plan")
		return
	}

	now := time.Now().UTC()
	ttl := defaultDeploymentTTL
	if input.TTLSeconds > 0 {
		ttl = time.Duration(input.TTLSeconds) * time.Second
	}
	deployment := state.Deployment{
		ID:                     deploymentID,
		AccountID:              account.ID,
		ScopeType:              scope.Type,
		ScopeID:                scope.ID,
		GitHubInstallationID:   installation.InstallationID,
		RepositoryID:           repository.ID,
		RepositoryFullName:     repository.FullName,
		Ref:                    input.Ref,
		CommitSHA:              commitSHA,
		ConfigPath:             input.ConfigPath,
		SnapshotVersion:        plan.Snapshot().Version,
		SpecHash:               plan.Snapshot().SpecHash,
		SpecJSONEncrypted:      encryptedSpec,
		Status:                 state.DeploymentStatusQueued,
		SandboxAPIURL:          sandboxSnapshot.APIURL,
		SandboxAPIKeyEncrypted: sandboxSnapshot.EncryptedAPIKey,
		SandboxConfigSource:    sandboxSnapshot.Source,
		SandboxRegion:          s.sandboxRegionForAPIURL(sandboxSnapshot.APIURL),
		DeploymentTemplateID:   templateID,
		ExpiresAt:              now.Add(ttl),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	auditPayload := map[string]any{
		"installation_id":      installation.InstallationID,
		"repository_id":        repository.ID,
		"repository_full_name": repository.FullName,
		"ref":                  input.Ref,
		"commit_sha":           commitSHA,
		"config_path":          input.ConfigPath,
		"snapshot_version":     plan.Snapshot().Version,
		"spec_hash":            plan.Snapshot().SpecHash,
		"expires_at":           deployment.ExpiresAt,
		"template_id":          templateID,
	}
	err = s.applyMutationWithAudit("github:"+session.Subject, "deployment.create", "deployment", deployment.ID, auditPayload, func(tx state.Store) error {
		if _, lookupErr := tx.GetDeployment(deployment.ID); lookupErr == nil {
			return state.ErrConflict
		} else if !errors.Is(lookupErr, state.ErrNotFound) {
			return lookupErr
		}
		if _, createErr := tx.CreateDeploymentWithScopeLimit(deployment, s.cfg.DeploymentMaxConcurrent); createErr != nil {
			return createErr
		}
		return tx.AppendDeploymentEvent(state.DeploymentEvent{DeploymentID: deployment.ID, EventType: "lifecycle", Stage: "queued", Message: "Deployment queued for execution."})
	})
	if err != nil {
		if errors.Is(err, state.ErrDeploymentQuotaExceeded) {
			w.Header().Set("Retry-After", "60")
			writeErrorCode(w, http.StatusTooManyRequests, "deployment_quota_exceeded", "deployment scope concurrency limit reached")
			return
		}
		if key != "" && !errors.Is(err, state.ErrAuditEventPersistence) && s.writeDeploymentReplay(w, deploymentID, account.ID, installation.InstallationID, repository.ID, input) {
			return
		}
		writeError(w, http.StatusInternalServerError, "persist deployment")
		return
	}
	s.signalDeployments()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, deploymentResponseFromState(deployment))
}

func githubDeploymentSourceURL(repository string) string {
	return "https://github.com/" + strings.TrimSuffix(strings.TrimSpace(repository), ".git") + ".git"
}

func (s *Server) handleUserListDeployments(w http.ResponseWriter, r *http.Request) {
	_, account, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	limit, offset, err := parsePagination(r, 100, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, total, err := s.store.ListDeploymentsForAccount(account.ID, state.DeploymentListOptions{Limit: limit, Offset: offset})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list deployments")
		return
	}
	responses := make([]deploymentResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, deploymentResponseFromState(item))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": responses, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleUserGetDeployment(w http.ResponseWriter, r *http.Request) {
	_, account, ok := s.requireUserSession(w, r)
	if !ok {
		return
	}
	deployment, err := s.store.GetDeployment(strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, state.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load deployment")
		return
	}
	if deployment.AccountID != account.ID {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	access, err := s.deploymentAccessForAccount(r.Context(), account.ID)
	if err != nil {
		s.writeUserRepositoryAuthorizationError(w, err)
		return
	}
	if !hasDeploymentAccess(access, deployment) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deploymentResponseFromState(deployment))
}

func (s *Server) loadAuthorizedDeployment(w http.ResponseWriter, r *http.Request) (adminSession, state.Account, state.Deployment, bool) {
	session, account, ok := s.requireUserSession(w, r)
	if !ok {
		return adminSession{}, state.Account{}, state.Deployment{}, false
	}
	deployment, err := s.store.GetDeployment(strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, state.ErrNotFound) || (err == nil && deployment.AccountID != account.ID) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return adminSession{}, state.Account{}, state.Deployment{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load deployment")
		return adminSession{}, state.Account{}, state.Deployment{}, false
	}
	access, err := s.deploymentAccessForAccount(r.Context(), account.ID)
	if err != nil {
		s.writeUserRepositoryAuthorizationError(w, err)
		return adminSession{}, state.Account{}, state.Deployment{}, false
	}
	if !hasDeploymentAccess(access, deployment) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return adminSession{}, state.Account{}, state.Deployment{}, false
	}
	return session, account, deployment, true
}

func (s *Server) handleUserListDeploymentEvents(w http.ResponseWriter, r *http.Request) {
	_, _, deployment, ok := s.loadAuthorizedDeployment(w, r)
	if !ok {
		return
	}
	limit := 200
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = min(parsed, 500)
	}
	cursors := [2]int64{}
	for i, key := range []string{"before_id", "after_id"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed <= 0 {
				writeError(w, http.StatusBadRequest, "invalid "+key)
				return
			}
			cursors[i] = parsed
		}
	}
	if cursors[0] > 0 && cursors[1] > 0 {
		writeError(w, http.StatusBadRequest, "before_id and after_id are mutually exclusive")
		return
	}
	var events []state.DeploymentEvent
	var hasMore bool
	var err error
	if cursors[1] > 0 {
		events, hasMore, err = s.store.ListDeploymentEventsAfter(deployment.ID, cursors[1], limit)
	} else {
		events, hasMore, err = s.store.ListDeploymentEvents(deployment.ID, cursors[0], limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list deployment events")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "has_more": hasMore})
}

func (s *Server) handleUserStopDeployment(w http.ResponseWriter, r *http.Request) {
	session, _, deployment, ok := s.loadAuthorizedDeployment(w, r)
	if !ok {
		return
	}
	if s.deploymentWorker == nil && deployment.SandboxID != "" {
		writeError(w, http.StatusServiceUnavailable, "deployment worker is not configured")
		return
	}
	terminal := deployment.Status == state.DeploymentStatusCompleted || deployment.Status == state.DeploymentStatusExpired || deployment.Status == state.DeploymentStatusFailed
	if !(terminal && deployment.SandboxID == "") {
		next := deployment
		next.Status = state.DeploymentStatusStopping
		if deployment.Status == state.DeploymentStatusFailed {
			next.Status = state.DeploymentStatusFailed
		}
		if deployment.Status == state.DeploymentStatusQueued || (s.deploymentWorker == nil && deployment.SandboxID == "") {
			next.Status = state.DeploymentStatusCompleted
		}
		err := s.applyMutationWithAudit("github:"+session.Subject, "deployment.stop", "deployment", deployment.ID, map[string]any{"status": next.Status}, func(tx state.Store) error { return tx.UpdateDeployment(next) })
		if err != nil {
			writeError(w, http.StatusConflict, "deployment changed while stopping")
			return
		}
	}
	if s.deploymentWorker != nil {
		if err := s.deploymentWorker.Stop(r.Context(), deployment.ID, false); err != nil {
			if errors.Is(err, deployruntime.ErrCreatePending) {
				updated, _ := s.store.GetDeployment(deployment.ID)
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, http.StatusAccepted, deploymentResponseFromState(updated))
				return
			}
			writeError(w, http.StatusBadGateway, "deployment cleanup is incomplete")
			return
		}
	}
	updated, err := s.store.GetDeployment(deployment.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load deployment")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deploymentResponseFromState(updated))
}

func (s *Server) handleUserRetryDeployment(w http.ResponseWriter, r *http.Request) {
	session, _, deployment, ok := s.loadAuthorizedDeployment(w, r)
	if !ok {
		return
	}
	if deployment.Status != state.DeploymentStatusFailed && deployment.Status != state.DeploymentStatusCompleted && deployment.Status != state.DeploymentStatusExpired {
		writeError(w, http.StatusConflict, "deployment retry is not allowed in the current state")
		return
	}
	if deployment.SandboxID != "" {
		if s.deploymentWorker == nil {
			writeError(w, http.StatusServiceUnavailable, "deployment worker is not configured")
			return
		}
		if err := s.deploymentWorker.Stop(r.Context(), deployment.ID, false); err != nil {
			writeError(w, http.StatusBadGateway, "deployment cleanup is incomplete")
			return
		}
		deployment, _ = s.store.GetDeployment(deployment.ID)
	}
	if strings.TrimSpace(deployment.DeploymentTemplateID) == "" {
		writeErrorCode(w, http.StatusConflict, "deployment_template_required", "Create a new deployment with an available Sandbox template")
		return
	}
	service, snapshot, snapshotErr := s.sandboxServiceForDeployment(r.Context(), accountPreferenceScope{Type: deployment.ScopeType, ID: deployment.ScopeID})
	if snapshotErr != nil {
		writeErrorCode(w, http.StatusConflict, "deployment_sandbox_not_configured", "Deploy requires configured scoped Sandbox credentials")
		return
	}
	if err := service.ValidateTemplate(r.Context(), deployment.DeploymentTemplateID); err != nil {
		writeErrorCode(w, http.StatusConflict, "deployment_template_unavailable", "The selected Sandbox template is unavailable or has no runnable default build")
		return
	}
	deployment.SandboxAPIURL, deployment.SandboxAPIKeyEncrypted, deployment.SandboxConfigSource = snapshot.APIURL, snapshot.EncryptedAPIKey, snapshot.Source
	deployment.SandboxRegion = s.sandboxRegionForAPIURL(snapshot.APIURL)
	expiresAt := time.Now().UTC().Add(defaultDeploymentTTL)
	err := s.applyMutationWithAudit("github:"+session.Subject, "deployment.retry", "deployment", deployment.ID, map[string]any{"expires_at": expiresAt}, func(tx state.Store) error {
		if err := tx.RetryDeploymentWithScopeLimit(deployment, expiresAt, s.cfg.DeploymentMaxConcurrent); err != nil {
			return err
		}
		return tx.AppendDeploymentEvent(state.DeploymentEvent{DeploymentID: deployment.ID, Attempt: deployment.Attempt, EventType: "lifecycle", Stage: "queued", Message: "Deployment queued for retry."})
	})
	if errors.Is(err, state.ErrDeploymentQuotaExceeded) {
		w.Header().Set("Retry-After", "60")
		writeErrorCode(w, http.StatusTooManyRequests, "deployment_quota_exceeded", "deployment scope concurrency limit reached")
		return
	}
	if errors.Is(err, state.ErrRetryNotAllowed) {
		writeError(w, http.StatusConflict, "deployment retry is not allowed in the current state")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		writeError(w, http.StatusConflict, "deployment changed while retrying")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "retry deployment")
		return
	}
	updated, err := s.store.GetDeployment(deployment.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load deployment")
		return
	}
	s.signalDeployments()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, deploymentResponseFromState(updated))
}

func (s *Server) writeDeploymentReplay(w http.ResponseWriter, id string, accountID, installationID, repositoryID int64, input createDeploymentRequest) bool {
	saved, err := s.store.GetDeployment(id)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load deployment")
		return true
	}
	ttl := defaultDeploymentTTL
	if input.TTLSeconds != 0 {
		ttl = time.Duration(input.TTLSeconds) * time.Second
	}
	if saved.AccountID != accountID || saved.GitHubInstallationID != installationID || saved.RepositoryID != repositoryID || saved.Ref != input.Ref || saved.ConfigPath != input.ConfigPath || saved.ExpiresAt.Sub(saved.CreatedAt) != ttl || (input.TemplateID != "" && input.TemplateID != saved.DeploymentTemplateID) {
		writeErrorCode(w, http.StatusConflict, "deployment_request_conflict", "Idempotency-Key was already used for a different deployment request")
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deploymentResponseFromState(saved))
	return true
}

func (s *Server) deploymentAccessForAccount(ctx context.Context, accountID int64) ([]state.DeploymentRepositoryAccess, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	token, err := s.githubUserAccessToken(accountID)
	if err != nil {
		return nil, err
	}
	if s.gh == nil {
		return nil, errors.New("GitHub client is not configured")
	}
	installations, err := s.store.ListGitHubInstallations(accountID)
	if err != nil {
		return nil, err
	}
	owners := make([]state.GitHubInstallationAccount, len(installations))
	for i, installation := range installations {
		owners[i] = state.GitHubInstallationAccount{GitHubAccountID: installation.GitHubAccountID, AccountType: installation.AccountType, AccountLogin: installation.AccountLogin}
	}
	manageable, err := s.githubInstallationAccountsManageable(ctx, accountID, owners)
	if err != nil {
		return nil, err
	}
	access := make([]state.DeploymentRepositoryAccess, 0)
	for i, installation := range installations {
		if !manageable[i] {
			continue
		}
		repositories, err := s.gh.ListUserInstallationRepositoryDetails(ctx, token, installation.InstallationID)
		if err != nil {
			status, known := github.ErrorStatus(err)
			if known && (status == http.StatusNotFound || (status == http.StatusForbidden && !github.IsRateLimitError(err))) {
				continue
			}
			return nil, err
		}
		for _, repository := range repositories {
			if _, ok := deploymentRepositoryAccess([]github.Repository{repository}, repository.FullName, installation); ok {
				access = append(access, state.DeploymentRepositoryAccess{InstallationID: installation.InstallationID, RepositoryID: repository.ID, RepositoryFullName: repository.FullName})
			}
		}
	}
	return access, nil
}

func hasDeploymentAccess(access []state.DeploymentRepositoryAccess, deployment state.Deployment) bool {
	for _, item := range access {
		if item.InstallationID == deployment.GitHubInstallationID && item.RepositoryID == deployment.RepositoryID && item.RepositoryFullName == deployment.RepositoryFullName {
			return true
		}
	}
	return false
}

func validateCreateDeploymentRequest(input createDeploymentRequest) error {
	if input.Source != "" && input.Source != "branch" && input.Source != "tag" {
		return errors.New("source must be branch or tag")
	}
	if len(input.TemplateID) > 256 || strings.IndexFunc(input.TemplateID, unicode.IsControl) >= 0 {
		return errors.New("template_id is invalid")
	}
	if input.InstallationID <= 0 {
		return errors.New("installation_id is required")
	}
	parts := strings.Split(strings.TrimSpace(input.RepositoryFullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(input.RepositoryFullName, "\\?#%") || strings.IndexFunc(input.RepositoryFullName, unicode.IsSpace) >= 0 || strings.IndexFunc(input.RepositoryFullName, unicode.IsControl) >= 0 {
		return errors.New("repository_full_name must be owner/repository")
	}
	if strings.TrimSpace(input.Ref) == "" || len(normalizeDeploymentRef(input.Ref, input.Source)) > 256 || strings.IndexFunc(input.Ref, unicode.IsControl) >= 0 {
		return errors.New("ref must be non-empty and at most 256 bytes including its source prefix")
	}
	configPath := normalizeDeploymentConfigPath(input.ConfigPath)
	if configPath == "" {
		return errors.New("config_path is invalid")
	}
	if input.TTLSeconds < 0 || (input.TTLSeconds > 0 && (input.TTLSeconds < int64(minDeploymentTTL/time.Second) || input.TTLSeconds > int64(maxDeploymentTTL/time.Second))) {
		return fmt.Errorf("ttl_seconds must be between %d and %d", int(minDeploymentTTL/time.Second), int(maxDeploymentTTL/time.Second))
	}
	return nil
}

func normalizeDeploymentRef(ref, source string) string {
	ref = strings.TrimSpace(ref)
	switch source {
	case "branch":
		return "refs/heads/" + ref
	case "tag":
		return "refs/tags/" + ref
	default:
		return ref
	}
}

func normalizeDeploymentConfigPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return deployconfig.DefaultPath
	}
	if path.IsAbs(value) || len(value) > 1024 || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.Contains(value, "\\") {
		return ""
	}
	value = path.Clean(value)
	if value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return ""
	}
	return value
}

func deploymentRepositoryAccess(repositories []github.Repository, requested string, installation state.GitHubInstallation) (github.Repository, bool) {
	requested = strings.TrimSpace(requested)
	for _, repository := range repositories {
		if !strings.EqualFold(strings.TrimSpace(repository.FullName), requested) || repository.ID <= 0 {
			continue
		}
		if owner := strings.TrimSpace(installation.AccountLogin); owner != "" && !strings.EqualFold(owner, strings.TrimSpace(repository.Owner.Login)) {
			continue
		}
		if installation.GitHubAccountID > 0 && repository.Owner.ID > 0 && installation.GitHubAccountID != repository.Owner.ID {
			continue
		}
		return repository, true
	}
	return github.Repository{}, false
}

func deploymentResponseFromState(item state.Deployment) deploymentResponse {
	source, ref := "", item.Ref
	if strings.HasPrefix(ref, "refs/heads/") {
		source, ref = "branch", strings.TrimPrefix(ref, "refs/heads/")
	} else if strings.HasPrefix(ref, "refs/tags/") {
		source, ref = "tag", strings.TrimPrefix(ref, "refs/tags/")
	}
	return deploymentResponse{
		ID: item.ID, ScopeType: item.ScopeType, ScopeID: item.ScopeID,
		GitHubInstallationID: item.GitHubInstallationID, RepositoryID: item.RepositoryID,
		RepositoryFullName: item.RepositoryFullName, Ref: ref, Source: source, CommitSHA: item.CommitSHA,
		ConfigPath: item.ConfigPath, SnapshotVersion: item.SnapshotVersion, SpecHash: item.SpecHash,
		Status: item.Status, TemplateID: item.DeploymentTemplateID, SandboxID: item.SandboxID, SandboxConfigSource: item.SandboxConfigSource,
		SandboxRegion: item.SandboxRegion,
		Endpoint:      item.Endpoint, EndpointAccess: deploymentEndpointAccess(item.Endpoint), FailureStage: item.FailureStage, FailureReason: item.FailureReason,
		ExpiresAt: item.ExpiresAt, CreatedAt: item.CreatedAt, PlanningAt: item.PlanningAt,
		CreatingAt: item.CreatingAt, RunningAt: item.RunningAt, StoppingAt: item.StoppingAt,
		CompletedAt: item.CompletedAt, FailedAt: item.FailedAt, UpdatedAt: item.UpdatedAt,
		Version: item.Version,
	}
}

func deploymentEndpointAccess(endpoint string) string {
	if strings.TrimSpace(endpoint) == "" {
		return ""
	}
	return "public"
}
