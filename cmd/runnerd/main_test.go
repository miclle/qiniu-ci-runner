package main

import (
	"testing"

	"github.com/qiniu/ci-runner/internal/state"
)

func TestBootstrapAdminAccount(t *testing.T) {
	store := state.New(t.TempDir())
	if err := bootstrapAdminAccount(store, "github:12345"); err != nil {
		t.Fatal(err)
	}
	account, _, err := store.GetAccountByOAuthIdentity("github", "12345")
	if err != nil {
		t.Fatal(err)
	}
	if account.Role != "admin" {
		t.Fatalf("expected admin role, got %#v", account)
	}
}

func TestBootstrapAdminAccountDefaultsToGitHub(t *testing.T) {
	store := state.New(t.TempDir())
	if err := bootstrapAdminAccount(store, "12345"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetAccountByOAuthIdentity("github", "12345"); err != nil {
		t.Fatal(err)
	}
}
