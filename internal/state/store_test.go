package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCreateRequestIsIdempotent(t *testing.T) {
	store := New(t.TempDir())
	req := RunnerRequest{ID: "123", Source: "test", Labels: []string{"self-hosted"}, RunnerName: "e2b-123"}
	created, st, err := store.CreateRequest(req, []byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !created || st.Status != StatusQueued {
		t.Fatalf("unexpected first create: created=%v state=%#v", created, st)
	}

	created, st, err = store.CreateRequest(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected duplicate request to reuse existing directory")
	}
	if st.ID != "123" {
		t.Fatalf("unexpected state id: %q", st.ID)
	}
	if _, err := os.Stat(filepath.Join(store.RequestDir("123"), "github_payload.json")); err != nil {
		t.Fatal(err)
	}
}

func TestListStatesReturnsCorruptJSONError(t *testing.T) {
	store := New(t.TempDir())
	if err := os.MkdirAll(store.RequestDir("bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.RequestDir("bad"), "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListStates(); err == nil {
		t.Fatal("expected corrupt json error")
	}
}

func TestCreateRequestRepairsMissingStateInExistingDirectory(t *testing.T) {
	store := New(t.TempDir())
	dir := store.RequestDir("partial")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.json"), []byte(`{"id":"partial","source":"test","labels":["self-hosted"],"runner_name":"e2b-partial","created_at":"2026-05-18T10:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created, st, err := store.CreateRequest(RunnerRequest{ID: "partial", Source: "test", Labels: []string{"self-hosted"}, RunnerName: "e2b-partial"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected existing partial directory to be repaired, not created")
	}
	if st.Status != StatusQueued || st.ID != "partial" {
		t.Fatalf("unexpected repaired state: %#v", st)
	}
	if _, err := store.ReadState("partial"); err != nil {
		t.Fatal(err)
	}
}

func TestListStatesSkipsAtomicCreateTempDirectories(t *testing.T) {
	store := New(t.TempDir())
	if err := os.MkdirAll(filepath.Join(store.dir, ".123.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	states, err := store.ListStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("expected temp directories to be ignored, got %d states", len(states))
	}
}

func TestWriteStateUsesUniqueTempFiles(t *testing.T) {
	store := New(t.TempDir())
	_, st, err := store.CreateRequest(RunnerRequest{
		ID:         "123",
		Source:     "test",
		Labels:     []string{"self-hosted"},
		RunnerName: "e2b-123",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next := st
			next.Status = StatusRunning
			next.ProcessPID = uint32(i + 1)
			next.CreatedAt = time.Now().UTC()
			errs <- store.WriteState(next)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ReadState("123"); err != nil {
		t.Fatal(err)
	}
}

func TestReadLogCanReturnTail(t *testing.T) {
	store := New(t.TempDir())
	if _, _, err := store.CreateRequest(RunnerRequest{
		ID:         "123",
		Source:     "test",
		Labels:     []string{"self-hosted"},
		RunnerName: "e2b-123",
	}, nil); err != nil {
		t.Fatal(err)
	}
	store.AppendLog("123", "control.log", []byte("line-1\nline-2\n"))
	data, err := store.ReadLog("123", "control.log", 7)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "line-2\n" {
		t.Fatalf("unexpected log tail: %q", string(data))
	}
}
