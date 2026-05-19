package state

import (
	"errors"
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
		t.Fatal("expected duplicate request to reuse existing row")
	}
	if st.ID != "123" {
		t.Fatalf("unexpected state id: %q", st.ID)
	}
}

func TestWriteStateUsesVersionCAS(t *testing.T) {
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

	st.Status = StatusRunning
	st.ProcessPID = 42
	if err := store.WriteState(st); err != nil {
		t.Fatal(err)
	}

	st.Status = StatusFailed
	err = store.WriteState(st)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
}

func TestListStatesAndActiveCount(t *testing.T) {
	store := New(t.TempDir())
	if _, _, err := store.CreateRequest(RunnerRequest{
		ID:         "active",
		Source:     "test",
		Labels:     []string{"self-hosted"},
		RunnerName: "e2b-active",
	}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, st, err := store.CreateRequest(RunnerRequest{
		ID:         "done",
		Source:     "test",
		Labels:     []string{"self-hosted"},
		RunnerName: "e2b-done",
	}, nil); err != nil {
		t.Fatal(err)
	} else {
		st.Status = StatusCompleted
		if err := store.WriteState(st); err != nil {
			t.Fatal(err)
		}
	}

	states, err := store.ListStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	if states[0].ID != "done" {
		t.Fatalf("expected newest state first, got %q", states[0].ID)
	}
	active, err := store.ActiveCount()
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("expected 1 active runner, got %d", active)
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
	store.AppendLog("123", "control.log", []byte("line-1\n"))
	store.AppendLog("123", "control.log", []byte("line-2\n"))
	data, err := store.ReadLog("123", "control.log", 7)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "line-2\n" {
		t.Fatalf("unexpected log tail: %q", string(data))
	}
}

func TestProfilesAndPoliciesCanBeMatched(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.UpsertProfile(RunnerProfile{
		Name:           "default",
		Labels:         []string{"self-hosted", "e2b"},
		TemplateID:     "base",
		MaxConcurrency: 10,
		Enabled:        true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertProfile(RunnerProfile{
		Name:           "large",
		Labels:         []string{"self-hosted", "e2b", "large"},
		TemplateID:     "large",
		MaxConcurrency: 2,
		Priority:       10,
		Enabled:        true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRepositoryPolicy(RepositoryPolicy{
		RepositoryFullName: "owner/repo",
		ProfileName:        "default",
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRepositoryPolicy(RepositoryPolicy{
		RepositoryFullName: "owner/repo",
		ProfileName:        "large",
		Enabled:            true,
	}); err != nil {
		t.Fatal(err)
	}

	match, err := store.MatchProfile("owner/repo", []string{"self-hosted", "e2b", "large"})
	if err != nil {
		t.Fatal(err)
	}
	if match.Profile == nil || match.Profile.Name != "large" {
		t.Fatalf("expected large profile match, got %#v", match.Profile)
	}
}

func TestMatchProfileReturnsReasonWhenPolicyMissing(t *testing.T) {
	store := New(t.TempDir())
	match, err := store.MatchProfile("owner/repo", []string{"self-hosted", "e2b"})
	if err != nil {
		t.Fatal(err)
	}
	if match.Profile != nil || match.Reason != "profile_not_allowed" {
		t.Fatalf("unexpected match result: %#v", match)
	}
}
