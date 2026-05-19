package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jimyag/e2b-github-runner/internal/config"
	"github.com/jimyag/e2b-github-runner/internal/github"
	"github.com/jimyag/e2b-github-runner/internal/sandboxrunner"
	"github.com/jimyag/e2b-github-runner/internal/state"
)

type Server struct {
	cfg     config.Config
	store   state.Store
	gh      *github.Client
	sandbox sandboxrunner.Service
	logger  *slog.Logger
	mux     *http.ServeMux
	slots   chan struct{}

	admissionMu sync.Mutex
	locks       [64]sync.Mutex
}

type manualCreateRequest struct {
	ID     string   `json:"id"`
	Labels []string `json:"labels"`
}

//go:embed admin/*
var adminAssets embed.FS

func New(cfg config.Config, store state.Store, gh *github.Client, sandbox sandboxrunner.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, store: store, gh: gh, sandbox: sandbox, logger: logger, mux: http.NewServeMux(), slots: make(chan struct{}, cfg.MaxConcurrentRunners)}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) Recover(ctx context.Context) error {
	states, err := s.store.ListStates()
	if err != nil {
		return err
	}
	for _, st := range states {
		if !isActiveStatus(st.Status) {
			continue
		}
		if err := s.recoverRunner(ctx, st.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /admin", s.handleAdminRedirect)
	s.mux.HandleFunc("GET /admin/", s.handleAdmin)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /webhooks/github", s.handleGitHubWebhook)
	s.mux.HandleFunc("POST /runners", s.handleCreateRunner)
	s.mux.HandleFunc("GET /runners", s.handleListRunners)
	s.mux.HandleFunc("GET /runners/{id}", s.handleGetRunner)
	s.mux.HandleFunc("GET /runners/{id}/logs/{name}", s.handleGetRunnerLog)
	s.mux.HandleFunc("DELETE /runners/{id}", s.handleDeleteRunner)
}

func (s *Server) handleAdminRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if name == "" {
		name = "index.html"
	}
	name = path.Clean("/" + name)
	if strings.HasPrefix(name, "/..") {
		http.NotFound(w, r)
		return
	}
	data, err := adminAssets.ReadFile("admin" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if !github.VerifyWebhookSignature(s.cfg.GitHubWebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	if r.Header.Get("X-GitHub-Event") != "workflow_job" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
		return
	}
	var event github.WorkflowJobEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workflow_job payload")
		return
	}
	id := strconv.FormatInt(event.WorkflowJob.ID, 10)
	switch event.Action {
	case "queued":
		if !github.LabelsMatch(event.WorkflowJob.Labels, s.cfg.RunnerLabels) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
			return
		}
		req := state.RunnerRequest{
			ID:         id,
			Source:     "github_webhook",
			JobID:      event.WorkflowJob.ID,
			Labels:     s.cfg.RunnerLabels,
			RunnerName: "e2b-" + id,
		}
		s.createAndStart(w, r, req, body)
	case "completed":
		if !strings.HasPrefix(event.WorkflowJob.RunnerName, "e2b-") {
			s.logger.Info("completed workflow job has no managed runner", "job_id", id, "runner_name", event.WorkflowJob.RunnerName)
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
			return
		}
		stopID := strings.TrimPrefix(event.WorkflowJob.RunnerName, "e2b-")
		s.stopIfExists(context.Background(), stopID, event.WorkflowJob)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
	}
}

func (s *Server) handleCreateRunner(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	var input manualCreateRequest
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input)
	}
	id := input.ID
	if id == "" {
		id = newID()
	}
	labels := input.Labels
	if len(labels) == 0 {
		labels = s.cfg.RunnerLabels
	}
	req := state.RunnerRequest{
		ID:         id,
		Source:     "manual_api",
		Labels:     labels,
		RunnerName: "e2b-" + id,
	}
	s.createAndStart(w, r, req, nil)
}

func (s *Server) handleListRunners(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	states, err := s.store.ListStates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, states)
}

func (s *Server) handleGetRunner(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	st, err := s.store.ReadState(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "runner not found")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleGetRunnerLog(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	name := r.PathValue("name")
	switch name {
	case "control.log", "stdout.log", "stderr.log":
	default:
		writeError(w, http.StatusBadRequest, "unsupported log name")
		return
	}
	data, err := s.store.ReadLog(r.PathValue("id"), name, 256<<10)
	if err != nil {
		writeError(w, http.StatusNotFound, "log not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleDeleteRunner(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	id := r.PathValue("id")
	st, err := s.stopRunner(context.Background(), id, github.WorkflowJob{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) createAndStart(w http.ResponseWriter, r *http.Request, req state.RunnerRequest, payload []byte) {
	s.admissionMu.Lock()
	if st, err := s.store.ReadState(req.ID); err == nil {
		s.admissionMu.Unlock()
		s.logger.Info("runner request already exists", "id", req.ID, "status", st.Status)
		writeJSON(w, http.StatusOK, st)
		return
	}
	active, err := s.store.ActiveCount()
	if err != nil {
		s.admissionMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if active >= s.cfg.MaxConcurrentRunners {
		s.admissionMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "runner concurrency limit reached")
		return
	}
	created, st, err := s.store.CreateRequest(req, payload)
	s.admissionMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if created {
		s.logger.Info("runner request created", "id", req.ID, "source", req.Source, "labels", req.Labels)
		s.store.AppendLog(req.ID, "control.log", []byte("runner request created\n"))
		go s.startRunnerWhenSlotAvailable(context.Background(), req.ID)
		writeJSON(w, http.StatusAccepted, st)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) startRunnerWhenSlotAvailable(ctx context.Context, id string) {
	s.store.AppendLog(id, "control.log", []byte("waiting for runner slot\n"))
	s.slots <- struct{}{}
	defer func() { <-s.slots }()
	s.store.AppendLog(id, "control.log", []byte("runner slot acquired\n"))
	s.startRunner(ctx, id)
}

func (s *Server) startRunner(ctx context.Context, id string) {
	unlock := s.lockRunner(id)
	req, err := s.store.ReadRequest(id)
	if err != nil {
		unlock()
		s.logger.Error("read runner request", "id", id, "error", err)
		return
	}
	st, err := s.store.ReadState(id)
	if err != nil {
		unlock()
		s.logger.Error("read runner state", "id", id, "error", err)
		return
	}
	if st.Status == state.StatusCompleted || st.Status == state.StatusStopping {
		unlock()
		s.logger.Info("runner start skipped because request is stopped", "id", id, "status", st.Status)
		s.store.AppendLog(id, "control.log", []byte("runner start skipped because request is stopped\n"))
		return
	}
	st.Status = state.StatusCreating
	if err := s.store.WriteState(st); err != nil {
		unlock()
		s.logger.Error("write creating state", "id", id, "error", err)
		return
	}
	unlock()

	s.logger.Info("creating github registration token", "id", id)
	s.store.AppendLog(id, "control.log", []byte("creating github registration token\n"))
	token, err := s.gh.CreateRegistrationToken(ctx)
	if err != nil {
		s.failState(id, st, err)
		return
	}
	s.logger.Info("starting sandbox runner", "id", id, "runner_name", req.RunnerName)
	s.store.AppendLog(id, "control.log", []byte("starting sandbox runner\n"))
	exitCh := make(chan struct{})
	createCtx, cancel := context.WithTimeout(ctx, s.cfg.SandboxCreateTimeout)
	result, err := s.sandbox.StartRunner(createCtx, sandboxrunner.StartInput{
		RequestID:         req.ID,
		RunnerName:        req.RunnerName,
		RepositoryURL:     s.gh.RunnerURL(),
		RegistrationToken: token.Token,
		Labels:            req.Labels,
		TemplateID:        s.cfg.SandboxTemplateID,
		Timeout:           s.cfg.SandboxTimeout,
		OnStdout:          func(data []byte) { s.store.AppendLog(id, "stdout.log", data) },
		OnStderr:          func(data []byte) { s.store.AppendLog(id, "stderr.log", data) },
		OnExit: func(result sandboxrunner.ExitResult, err error) {
			defer close(exitCh)
			s.runnerExited(id, result, err)
		},
	})
	cancel()
	if err != nil {
		s.failState(id, st, err)
		return
	}

	unlock = s.lockRunner(id)
	current, err := s.store.ReadState(id)
	if err != nil {
		unlock()
		s.logger.Error("read state before running update", "id", id, "error", err)
		s.cleanupStartedSandbox(id, result)
		return
	}
	if current.Status == state.StatusFailed || current.Status == state.StatusCompleted || current.Status == state.StatusStopping {
		unlock()
		s.logger.Info("runner exited before running update", "id", id, "status", current.Status)
		s.cleanupStartedSandbox(id, result)
		return
	}
	st = current
	st.Status = state.StatusRunning
	st.SandboxID = result.SandboxID
	st.ProcessPID = result.PID
	st.Error = ""
	if err := s.store.WriteState(st); err != nil {
		unlock()
		s.logger.Error("write running state", "id", id, "error", err)
		s.store.AppendLog(id, "control.log", []byte("write running state failed: "+err.Error()+"\n"))
		s.cleanupStartedSandbox(id, result)
		return
	}
	unlock()
	s.logger.Info("sandbox runner started", "id", id, "sandbox_id", result.SandboxID, "pid", result.PID)
	s.store.AppendLog(id, "control.log", []byte(fmt.Sprintf("sandbox runner started sandbox_id=%s pid=%d\n", result.SandboxID, result.PID)))
	<-exitCh
}

func (s *Server) failState(id string, st state.RunnerState, err error) {
	unlock := s.lockRunner(id)
	defer unlock()

	current, readErr := s.store.ReadState(id)
	if readErr == nil {
		if current.Status == state.StatusCompleted || current.Status == state.StatusStopping {
			s.logger.Info("runner failure ignored because request is stopped", "id", id, "status", current.Status, "error", err)
			return
		}
		st = current
	}
	st.Status = state.StatusFailed
	st.Error = err.Error()
	s.logger.Error("runner failed", "id", id, "error", err)
	s.store.AppendLog(id, "control.log", []byte("runner failed: "+err.Error()+"\n"))
	if writeErr := s.store.WriteState(st); writeErr != nil {
		s.logger.Error("write failed state", "id", id, "error", writeErr)
	}
}

func (s *Server) runnerExited(id string, result sandboxrunner.ExitResult, err error) {
	unlock := s.lockRunner(id)
	defer unlock()

	st, readErr := s.store.ReadState(id)
	if readErr != nil {
		s.logger.Error("read state after runner exit", "id", id, "error", readErr)
		return
	}
	if st.Status == state.StatusCompleted || st.Status == state.StatusStopping {
		return
	}
	if err != nil {
		st.Status = state.StatusFailed
		st.Error = err.Error()
		s.logger.Error("runner process exited with error", "id", id, "error", err)
		s.store.AppendLog(id, "control.log", []byte("runner process exited with error: "+err.Error()+"\n"))
		if cleanupErr := s.cleanupSandboxAfterExit(id, st); cleanupErr != nil {
			st.Error = st.Error + "; cleanup sandbox: " + cleanupErr.Error()
		}
		s.writeStateOrLog(id, st, "write failed exit state")
		return
	}
	if result.ExitCode == 0 {
		s.logger.Info("runner process exited", "id", id, "exit_code", result.ExitCode)
		s.store.AppendLog(id, "control.log", []byte("runner process exited cleanly\n"))
	} else {
		st.Status = state.StatusFailed
		st.Error = fmt.Sprintf("runner process exited with code %d: %s", result.ExitCode, result.Stderr)
		s.logger.Error("runner process exited non-zero", "id", id, "exit_code", result.ExitCode, "stderr", result.Stderr)
		s.store.AppendLog(id, "control.log", []byte(st.Error+"\n"))
	}
	if cleanupErr := s.cleanupSandboxAfterExit(id, st); cleanupErr != nil {
		st.Status = state.StatusFailed
		st.Error = "cleanup sandbox after runner exit: " + cleanupErr.Error()
	} else if st.Status != state.StatusFailed {
		st.Status = state.StatusCompleted
		st.CompletedAt = time.Now().UTC()
	}
	s.writeStateOrLog(id, st, "write exited state")
}

func (s *Server) cleanupSandboxAfterExit(id string, st state.RunnerState) error {
	if st.SandboxID == "" {
		return nil
	}
	if err := s.stopSandboxWithTimeout(context.Background(), st.SandboxID, st.ProcessPID); err != nil {
		if isSandboxGone(err) {
			s.logger.Info("sandbox already gone after runner exit", "id", id, "sandbox_id", st.SandboxID, "error", err)
			s.store.AppendLog(id, "control.log", []byte("sandbox already gone after runner exit: "+err.Error()+"\n"))
			return nil
		}
		s.logger.Error("cleanup sandbox after runner exit", "id", id, "sandbox_id", st.SandboxID, "error", err)
		s.store.AppendLog(id, "control.log", []byte("cleanup sandbox after runner exit failed: "+err.Error()+"\n"))
		return err
	}
	s.logger.Info("sandbox cleaned after runner exit", "id", id, "sandbox_id", st.SandboxID)
	s.store.AppendLog(id, "control.log", []byte("sandbox cleaned after runner exit\n"))
	return nil
}

func (s *Server) recoverRunner(ctx context.Context, id string) error {
	unlock := s.lockRunner(id)
	defer unlock()

	st, err := s.store.ReadState(id)
	if err != nil {
		return err
	}
	if !isActiveStatus(st.Status) {
		return nil
	}
	s.logger.Info("recovering runner after restart", "id", id, "status", st.Status, "sandbox_id", st.SandboxID)
	s.store.AppendLog(id, "control.log", []byte("recovering runner after service restart\n"))
	if st.SandboxID != "" {
		if err := s.stopSandboxWithTimeout(ctx, st.SandboxID, st.ProcessPID); err != nil {
			if isSandboxGone(err) {
				s.logger.Info("sandbox already gone during recovery", "id", id, "sandbox_id", st.SandboxID, "error", err)
				s.store.AppendLog(id, "control.log", []byte("sandbox already gone during recovery: "+err.Error()+"\n"))
			} else {
				st.Status = state.StatusFailed
				st.Error = "recover cleanup sandbox: " + err.Error()
				return s.store.WriteState(st)
			}
		}
	}
	st.Status = state.StatusCompleted
	st.CompletedAt = time.Now().UTC()
	st.Error = ""
	if err := s.store.WriteState(st); err != nil {
		return err
	}
	s.store.AppendLog(id, "control.log", []byte("runner marked completed during recovery\n"))
	return nil
}

func (s *Server) stopIfExists(ctx context.Context, id string, job github.WorkflowJob) {
	if _, err := s.store.ReadState(id); err != nil {
		return
	}
	if _, err := s.stopRunner(ctx, id, job); err != nil {
		s.logger.Error("stop runner", "id", id, "error", err)
	}
}

func (s *Server) stopRunner(ctx context.Context, id string, job github.WorkflowJob) (state.RunnerState, error) {
	unlock := s.lockRunner(id)
	defer unlock()

	st, err := s.store.ReadState(id)
	if err != nil {
		return state.RunnerState{}, err
	}
	if st.Status == state.StatusCompleted {
		if shouldRecordAssignedJob(st, job) {
			st.AssignedJobID = job.ID
			st.AssignedJobName = job.Name
			if err := s.store.WriteState(st); err != nil {
				return state.RunnerState{}, fmt.Errorf("write completed job assignment: %w", err)
			}
		}
		return st, nil
	}
	if shouldRecordAssignedJob(st, job) {
		st.AssignedJobID = job.ID
		st.AssignedJobName = job.Name
	}
	st.Status = state.StatusStopping
	if err := s.store.WriteState(st); err != nil {
		return state.RunnerState{}, fmt.Errorf("write stopping state: %w", err)
	}
	st.Version++
	if st.SandboxID != "" {
		if err := s.stopSandboxWithTimeout(ctx, st.SandboxID, st.ProcessPID); err != nil {
			if isSandboxGone(err) {
				s.logger.Info("sandbox already gone", "id", id, "sandbox_id", st.SandboxID, "error", err)
				s.store.AppendLog(id, "control.log", []byte("sandbox already gone: "+err.Error()+"\n"))
			} else {
				st.Status = state.StatusFailed
				st.Error = err.Error()
				if writeErr := s.store.WriteState(st); writeErr != nil {
					return state.RunnerState{}, fmt.Errorf("stop sandbox: %v; write failed state: %w", err, writeErr)
				}
				return st, err
			}
		}
	}
	st.Status = state.StatusCompleted
	st.CompletedAt = time.Now().UTC()
	st.Error = ""
	if err := s.store.WriteState(st); err != nil {
		return state.RunnerState{}, err
	}
	return st, nil
}

func (s *Server) writeStateOrLog(id string, st state.RunnerState, msg string) {
	if err := s.store.WriteState(st); err != nil {
		s.logger.Error(msg, "id", id, "error", err)
		s.store.AppendLog(id, "control.log", []byte(msg+": "+err.Error()+"\n"))
	}
}

func shouldRecordAssignedJob(st state.RunnerState, job github.WorkflowJob) bool {
	if job.ID == 0 {
		return false
	}
	return st.AssignedJobID != job.ID || st.AssignedJobName != job.Name
}

func isActiveStatus(status string) bool {
	switch status {
	case state.StatusQueued, state.StatusCreating, state.StatusRunning, state.StatusStopping:
		return true
	default:
		return false
	}
}

func (s *Server) lockRunner(id string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	mu := &s.locks[int(h.Sum32())%len(s.locks)]
	mu.Lock()
	return func() {
		mu.Unlock()
	}
}

func (s *Server) cleanupStartedSandbox(id string, result sandboxrunner.StartResult) {
	if result.SandboxID == "" {
		return
	}
	if err := s.stopSandboxWithTimeout(context.Background(), result.SandboxID, result.PID); err != nil && !isSandboxGone(err) {
		s.logger.Error("cleanup started sandbox", "id", id, "sandbox_id", result.SandboxID, "error", err)
		s.store.AppendLog(id, "control.log", []byte("cleanup started sandbox failed: "+err.Error()+"\n"))
		return
	}
	s.logger.Info("cleaned started sandbox", "id", id, "sandbox_id", result.SandboxID)
	s.store.AppendLog(id, "control.log", []byte("cleaned started sandbox\n"))
}

func (s *Server) stopSandboxWithTimeout(ctx context.Context, sandboxID string, pid uint32) error {
	if ctx == nil {
		ctx = context.Background()
	}
	stopCtx, cancel := context.WithTimeout(ctx, s.cfg.SandboxStopTimeout)
	defer cancel()
	return s.sandbox.StopRunner(stopCtx, sandboxID, pid)
}

func (s *Server) requireAdminAuth(w http.ResponseWriter, r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	token := ""
	if strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) == 1 {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="runnerd"`)
	writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
	return false
}

func isSandboxGone(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status 404") ||
		strings.Contains(msg, "Sandbox can't be resumed") ||
		strings.Contains(msg, "SandboxNotFound")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return strings.ToLower(hex.EncodeToString(b[:]))
}
