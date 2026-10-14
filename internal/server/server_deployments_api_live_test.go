package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qiniu/ci-runner/internal/config"
	"github.com/qiniu/ci-runner/internal/state"
)

// This opt-in check targets an already running local runnerd. It signs a short
// session for an existing account, keeping the cookie in memory. GitHub admission,
// the queue, deployd and provider cleanup all run in the actual server process.
// It does not test OAuth login, private repositories, or a second GitHub user.
func TestDeploymentLivePublicAPI(t *testing.T) {
	if os.Getenv("RUNNERD_DEPLOY_LIVE_API") != "1" {
		t.Skip("manual authenticated local runnerd acceptance")
	}
	base, err := url.Parse(os.Getenv("RUNNERD_DEPLOY_LIVE_BASE_URL"))
	if err != nil || base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "::1") || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		t.Fatal("RUNNERD_DEPLOY_LIVE_BASE_URL must be a loopback HTTP origin")
	}
	configPath := os.Getenv("RUNNERD_DEPLOY_LIVE_CONFIG")
	if !filepath.IsAbs(configPath) {
		t.Fatal("RUNNERD_DEPLOY_LIVE_CONFIG must be absolute")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal("local config unavailable")
	}
	if cfg.DeploymentMaxConcurrent < 1 || cfg.DeploymentMaxConcurrent > 3 {
		t.Fatal("live quota check requires a configured per-scope limit of 1 to 3")
	}
	accountID, err := strconv.ParseInt(os.Getenv("RUNNERD_DEPLOY_LIVE_ACCOUNT_ID"), 10, 64)
	if err != nil || accountID <= 0 {
		t.Fatal("configured account ID required")
	}
	store := state.NewWithOptions(state.Options{Backend: cfg.StateBackend, DatabaseDSN: cfg.StateDatabaseDSN.Value()})
	identity, err := store.GetOAuthIdentityForAccount(accountID, "github")
	if err != nil {
		t.Fatal("existing GitHub identity unavailable")
	}
	installations, err := store.ListGitHubInstallations(accountID)
	if err != nil {
		t.Fatal("installation links unavailable")
	}
	var installation state.GitHubInstallation
	for _, candidate := range installations {
		if strings.EqualFold(candidate.AccountLogin, "qiniu") {
			installation = candidate
			break
		}
	}
	if installation.ID == 0 {
		t.Fatal("qiniu installation must be linked to the configured account")
	}
	active, err := store.ListActiveDeployments()
	if err != nil {
		t.Fatal("active deployments unavailable")
	}
	for _, d := range active {
		if d.ScopeType == state.AccountScopeTypeGitHubInstall && d.ScopeID == installation.InstallationID {
			t.Fatal("quota acceptance requires no existing active deployment in the qiniu scope")
		}
	}
	probe := New(cfg, store, nil, nil, nil)
	defer probe.Close()
	session, err := probe.encodeAdminSession(adminSession{Provider: "github", Subject: identity.OAuthSubject, Login: identity.OAuthLogin, Role: "user", ExpiresAt: time.Now().Add(15 * time.Minute).Unix()})
	if err != nil {
		t.Fatal("temporary session creation failed")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("unexpected API redirect") }}
	defer transport.CloseIdleConnections()
	call := func(ctx context.Context, method, path, key string, payload []byte) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, base.String()+path, bytes.NewReader(payload))
		if err != nil {
			return 0, nil, err
		}
		req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: session})
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		if err != nil {
			return 0, nil, errors.New("local API request failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, body, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	status, body, err := call(ctx, "GET", fmt.Sprintf("/user/deployment-templates?installation_id=%d&repository_full_name=qiniu/deployment", installation.ID), "", nil)
	if err != nil || status != 200 {
		t.Fatalf("template discovery status=%d", status)
	}
	var catalog struct {
		Templates []struct {
			ID      string   `json:"template_id"`
			Names   []string `json:"names"`
			Aliases []string `json:"aliases"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatal("invalid template response")
	}
	template := ""
	for _, item := range catalog.Templates {
		for _, name := range append(item.Names, item.Aliases...) {
			if name == "base" {
				template = item.ID
			}
		}
	}
	if template == "" {
		t.Fatal("runnable public base template not available")
	}
	payload, _ := json.Marshal(createDeploymentRequest{InstallationID: installation.ID, RepositoryFullName: "qiniu/deployment", Ref: "example/vite", Source: "branch", TemplateID: template, TTLSeconds: 600})
	ids := []string{}
	// Always stop only this test's submissions, including failures before an HTTP
	// response arrives. Keep history for inspection; DELETE is a lifecycle stop.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, id := range ids {
			status, _, err := call(cleanup, "DELETE", "/user/deployments/"+id, "", nil)
			if err != nil || (status != 200 && status != 202 && status != 404) {
				t.Errorf("cleanup deployment=%s status=%d", id, status)
			}
		}
	}()
	key := "live125-" + newID()
	id := newDeploymentID(accountID, key)
	ids = append(ids, id)
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].status, results[i].body, results[i].err = call(ctx, "POST", "/user/deployments", key, payload)
		}()
	}
	wg.Wait()
	created := 0
	for _, result := range results {
		if result.err != nil || (result.status != 201 && result.status != 200) {
			t.Fatalf("idempotent admission status=%d", result.status)
		}
		var d deploymentResponse
		if err := json.Unmarshal(result.body, &d); err != nil || d.ID != id {
			t.Fatal("duplicate request did not preserve deployment ID")
		}
		if result.status == 201 {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("duplicate admission created responses=%d", created)
	}
	t.Logf("concurrent idempotency passed: deployment=%s", id)
	// Fill only the configured scope limit. A rejected admission must leave no row.
	for i := 1; i < cfg.DeploymentMaxConcurrent; i++ {
		nextKey := "live125-" + newID()
		nextID := newDeploymentID(accountID, nextKey)
		ids = append(ids, nextID)
		status, _, err := call(ctx, "POST", "/user/deployments", nextKey, payload)
		if err != nil || status != 201 {
			t.Fatalf("quota fill status=%d", status)
		}
	}
	rejectedKey := "live125-" + newID()
	rejectedID := newDeploymentID(accountID, rejectedKey)
	ids = append(ids, rejectedID)
	status, body, err = call(ctx, "POST", "/user/deployments", rejectedKey, payload)
	if err != nil || status != 429 || !bytes.Contains(body, []byte("deployment_quota_exceeded")) {
		t.Fatalf("quota rejection status=%d", status)
	}
	if _, err := store.GetDeployment(rejectedID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("quota rejection persisted a deployment")
	}
	t.Logf("scope quota passed: limit=%d rejected=429", cfg.DeploymentMaxConcurrent)
	// Release filler attempts immediately, while retaining the first service for
	// a public probe and a real running-to-DELETE cleanup check.
	for _, filler := range ids[1 : len(ids)-1] {
		status, _, err := call(ctx, "DELETE", "/user/deployments/"+filler, "", nil)
		if err != nil || (status != 200 && status != 202) {
			t.Fatalf("filler release status=%d", status)
		}
	}
	var running state.Deployment
	for {
		running, err = store.GetDeployment(id)
		if err != nil {
			t.Fatal("admitted deployment missing")
		}
		if running.Status == state.DeploymentStatusRunning {
			break
		}
		if running.Status == state.DeploymentStatusFailed || running.Status == state.DeploymentStatusCompleted {
			t.Fatalf("startup status=%s stage=%s", running.Status, running.FailureStage)
		}
		select {
		case <-ctx.Done():
			t.Fatal("running deployment deadline exceeded")
		case <-time.After(2 * time.Second):
		}
	}
	if running.Attempt != 1 || running.RevisionNumber != 1 {
		t.Fatal("duplicate requests created another execution attempt")
	}
	public, err := (&http.Client{Timeout: 15 * time.Second}).Get(running.Endpoint)
	if err != nil {
		t.Fatal("public endpoint unavailable")
	}
	html, readErr := io.ReadAll(io.LimitReader(public.Body, 65536))
	public.Body.Close()
	if readErr != nil || public.StatusCode != 200 || !bytes.Contains(bytes.ToLower(html), []byte("<!doctype html>")) {
		t.Fatalf("public response status=%d", public.StatusCode)
	}
	status, _, err = call(ctx, "DELETE", "/user/deployments/"+id, "", nil)
	if err != nil || (status != 200 && status != 202) {
		t.Fatalf("authenticated DELETE status=%d", status)
	}
	for {
		terminal, err := store.GetDeployment(id)
		if err != nil {
			t.Fatal("deleted deployment history missing")
		}
		if terminal.Status == state.DeploymentStatusCompleted && terminal.SandboxID == "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("DELETE cleanup deadline exceeded")
		case <-time.After(2 * time.Second):
		}
	}
	factory := &serverDeploymentRuntimeFactory{server: probe}
	if handle, err := factory.Connect(ctx, running); err != nil || handle != nil {
		t.Fatalf("provider release unconfirmed: exists=%v error_category=%T", handle != nil, err)
	}
	// Replaying the same key after cleanup must return the saved terminal plan.
	status, body, err = call(ctx, "POST", "/user/deployments", key, payload)
	var replay deploymentResponse
	if err != nil || status != 200 || json.Unmarshal(body, &replay) != nil || replay.ID != id || replay.Status != state.DeploymentStatusCompleted {
		t.Fatalf("terminal idempotency replay status=%d", status)
	}
	events, _, err := store.ListDeploymentEvents(id, 0, 100)
	if err != nil || len(events) == 0 {
		t.Fatal("DELETE erased retained history")
	}
	// A previous 429 must not reserve its key or keep capacity after cleanup.
	status, _, err = call(ctx, "POST", "/user/deployments", rejectedKey, payload)
	if err != nil || status != 201 {
		t.Fatalf("admission after release status=%d", status)
	}
	status, _, err = call(ctx, "DELETE", "/user/deployments/"+rejectedID, "", nil)
	if err != nil || (status != 200 && status != 202) {
		t.Fatalf("replacement cleanup status=%d", status)
	}
	for _, testID := range ids {
		var d state.Deployment
		for {
			d, err = store.GetDeployment(testID)
			if err != nil {
				t.Fatal("test record unavailable")
			}
			if d.SandboxID == "" && (d.Status == state.DeploymentStatusCompleted || d.Status == state.DeploymentStatusFailed) {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("test resource remains active: deployment=%s status=%s", testID, d.Status)
			case <-time.After(2 * time.Second):
			}
		}
		if testID == id {
			continue
		}
		revision, err := store.GetDeploymentRevision(testID, 1)
		if err != nil {
			t.Fatal("test revision missing")
		}
		if revision.SandboxID != "" {
			d.SandboxID = revision.SandboxID
			if handle, err := factory.Connect(ctx, d); err != nil || handle != nil {
				t.Fatalf("filler release unconfirmed: exists=%v error_category=%T", handle != nil, err)
			}
		}
	}
	evidence := map[string]any{"recorded_at": time.Now().UTC(), "repository": "qiniu/deployment", "ref": "example/vite", "deployment_id": id, "commit_sha": running.CommitSHA, "sandbox_id": running.SandboxID, "concurrent_idempotency_single_attempt": true, "scope_limit": cfg.DeploymentMaxConcurrent, "quota_status": 429, "admission_after_release": true, "public_http_status": public.StatusCode, "delete_status": replay.Status, "provider_missing_after_delete": true, "retained_events": len(events), "terminal_replay_preserved": true}
	if path := os.Getenv("RUNNERD_DEPLOY_LIVE_EVIDENCE"); path != "" {
		if !filepath.IsAbs(path) {
			t.Fatal("evidence path must be absolute")
		}
		data, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal("evidence write failed")
		}
	}
	t.Logf("real API acceptance passed: deployment=%s attempt=%d public_http=200 provider_missing=true", id, running.Attempt)
}
