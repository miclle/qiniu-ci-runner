package state

import (
	"os"
	"path/filepath"
	"testing"
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
