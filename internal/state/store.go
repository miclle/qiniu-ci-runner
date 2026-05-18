package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	StatusQueued   = "queued"
	StatusCreating = "creating"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusStopped  = "stopped"
	StatusFailed   = "failed"
)

type RunnerRequest struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	JobID      int64     `json:"job_id,omitempty"`
	Labels     []string  `json:"labels"`
	RunnerName string    `json:"runner_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type RunnerState struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	RunnerName string    `json:"runner_name"`
	SandboxID  string    `json:"sandbox_id,omitempty"`
	ProcessPID uint32    `json:"process_pid,omitempty"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	CreatedAt  time.Time `json:"created_at"`
	StoppedAt  time.Time `json:"stopped_at,omitempty"`
}

type Store struct {
	dir string
}

func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Ensure() error {
	return os.MkdirAll(s.dir, 0o755)
}

func (s *Store) CreateRequest(req RunnerRequest, payload []byte) (bool, RunnerState, error) {
	if err := s.Ensure(); err != nil {
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
	path := s.RequestDir(req.ID)
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			st, readErr := s.ReadState(req.ID)
			return false, st, readErr
		}
		return false, RunnerState{}, err
	}
	if err := writeJSON(filepath.Join(path, "request.json"), req); err != nil {
		return false, RunnerState{}, err
	}
	if payload != nil {
		if err := os.WriteFile(filepath.Join(path, "github_payload.json"), payload, 0o600); err != nil {
			return false, RunnerState{}, err
		}
	}
	st := RunnerState{
		ID:         req.ID,
		Status:     StatusQueued,
		RunnerName: req.RunnerName,
		CreatedAt:  req.CreatedAt,
		UpdatedAt:  now,
	}
	if err := s.WriteState(st); err != nil {
		return false, RunnerState{}, err
	}
	return true, st, nil
}

func (s *Store) RequestDir(id string) string {
	return filepath.Join(s.dir, sanitizeID(id))
}

func (s *Store) ReadRequest(id string) (RunnerRequest, error) {
	var req RunnerRequest
	err := readJSON(filepath.Join(s.RequestDir(id), "request.json"), &req)
	return req, err
}

func (s *Store) ReadState(id string) (RunnerState, error) {
	var st RunnerState
	err := readJSON(filepath.Join(s.RequestDir(id), "state.json"), &st)
	return st, err
}

func (s *Store) WriteState(st RunnerState) error {
	st.ID = sanitizeID(st.ID)
	st.UpdatedAt = time.Now().UTC()
	return writeJSON(filepath.Join(s.RequestDir(st.ID), "state.json"), st)
}

func (s *Store) ListStates() ([]RunnerState, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	states := make([]RunnerState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		st, err := s.ReadState(entry.Name())
		if err != nil {
			return nil, err
		}
		states = append(states, st)
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].CreatedAt.After(states[j].CreatedAt)
	})
	return states, nil
}

func (s *Store) ActiveCount() (int, error) {
	states, err := s.ListStates()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, st := range states {
		switch st.Status {
		case StatusQueued, StatusCreating, StatusRunning, StatusStopping:
			count++
		}
	}
	return count, nil
}

func (s *Store) AppendLog(id, name string, data []byte) {
	if len(data) == 0 {
		return
	}
	path := filepath.Join(s.RequestDir(id), name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
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
