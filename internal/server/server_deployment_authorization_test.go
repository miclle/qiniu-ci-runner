package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiniu/ci-runner/internal/state"
)

func deploymentAuthCall(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(testSessionCookie("hubot-id", "hubot", "user"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestDeploymentsShareJobsRepositoryAuthorization(t *testing.T) {
	var calls atomic.Int32
	var revoked atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/user/installations/987/repositories" {
			t.Errorf("unexpected GitHub call: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if revoked.Load() {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"repositories":[{"id":42,"full_name":"acme/project"}]}`)
	}))
	defer api.Close()
	store := state.New(t.TempDir())
	srv := newTestServer(t, store, api.URL, &fakeSandbox{})
	srv.Close()
	srv.loopCtx = context.Background()
	account, _, err := store.GetAccountByOAuthIdentity("github", "hubot-id")
	if err != nil {
		t.Fatal(err)
	}
	saveTestGitHubOAuthToken(t, store, account.ID, srv.cfg.AuthEncryptionKey.Value(), "user-token")
	if _, err := store.UpsertGitHubInstallation(state.GitHubInstallation{AccountID: account.ID, InstallationID: 987, AccountType: "organization", AccountLogin: "acme", GitHubAccountID: 600}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		d := state.Deployment{ID: fmt.Sprintf("shared-%d", i), AccountID: account.ID + 1, ScopeType: state.AccountScopeTypeGitHubInstall, ScopeID: 987, GitHubInstallationID: 987, RepositoryID: 42, RepositoryFullName: "acme/project", Ref: "refs/heads/main", CommitSHA: strings.Repeat("a", 40), ConfigPath: ".qiniu/deploy.yaml", SnapshotVersion: 1, SpecHash: "hash", SpecJSONEncrypted: "private-spec", SandboxAPIKeyEncrypted: "private-key", Status: state.DeploymentStatusCompleted, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
		if i == 2 {
			d.GitHubInstallationID = 988
		}
		if i == 3 {
			d.RepositoryFullName = "acme/hidden"
		}
		if _, err := store.CreateDeployment(d); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendDeploymentEvent(state.DeploymentEvent{DeploymentID: d.ID, EventType: "output", Message: "saved-output"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/user/deployments", "/user/deployments/shared-0", "/user/deployments/shared-0/events", "/user/deployments/shared-0/revisions"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "saved-output") {
			t.Fatalf("anonymous history %d %s", rec.Code, rec.Body.String())
		}
	}
	// Warm the same cache used by Jobs, not a deployment-specific cache.
	if _, err := srv.userAuthorizedRepositoryAccess(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/deployments?limit=1&offset=1", "/user/deployments?repository_full_name=acme/project&ref=refs/heads/main&limit=1&offset=1"} {
		rec := deploymentAuthCall(srv, "GET", path, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":2`) || !strings.Contains(rec.Body.String(), "shared-0") || strings.Contains(rec.Body.String(), "shared-1") {
			t.Fatalf("list %d %s", rec.Code, rec.Body.String())
		}
	}
	for _, suffix := range []string{"", "?revision=1", "/events", "/events?limit=1", "/events?after_id=1", "/events?before_id=2", "/revisions", "/revisions/1/events"} {
		rec := deploymentAuthCall(srv, "GET", "/user/deployments/shared-0"+suffix, "")
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "private-") {
			t.Fatalf("history %s: %d %s", suffix, rec.Code, rec.Body.String())
		}
	}
	for _, id := range []string{"shared-2", "shared-3"} {
		if rec := deploymentAuthCall(srv, "GET", "/user/deployments/"+id, ""); rec.Code != 404 {
			t.Fatalf("wrong pair: %d", rec.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("polling bypassed Jobs cache: %d calls", calls.Load())
	}
	for _, action := range []struct{ method, path string }{{"POST", "/user/deployments/shared-0/stop"}, {"DELETE", "/user/deployments/shared-1"}} {
		if rec := deploymentAuthCall(srv, action.method, action.path, ""); rec.Code != 200 {
			t.Fatalf("shared action %d %s", rec.Code, rec.Body.String())
		}
	}
	// The queued executor uses the submitting member, even if creator OAuth is absent.
	queued, err := store.GetDeployment("shared-0")
	if err != nil {
		t.Fatal(err)
	}
	queued.ExecutionAccountID = account.ID
	if _, err := srv.deploymentSource(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	if _, err := srv.deploymentSource(context.Background(), queued); err == nil {
		t.Fatal("worker accepted revoked repository access")
	}
	srv.invalidateUserRepositoryAccess(account.ID)
	for _, path := range []string{"/user/deployments", "/user/deployments/shared-0", "/user/deployments/shared-0/events", "/user/deployments/shared-0/revisions"} {
		if rec := deploymentAuthCall(srv, "GET", path, ""); rec.Code != 403 || !strings.Contains(rec.Body.String(), "REAUTH_REQUIRED") {
			t.Fatalf("revoked OAuth %d %s", rec.Code, rec.Body.String())
		}
	}
	if err := store.DeleteAccountSecret(state.AccountScopeTypeAccount, account.ID, state.AccountSecretTypeGitHubOAuthToken); err != nil {
		t.Fatal(err)
	}
	srv.invalidateUserRepositoryAccess(account.ID)
	if rec := deploymentAuthCall(srv, "GET", "/user/deployments/shared-0", ""); rec.Code != 403 {
		t.Fatalf("missing OAuth %d", rec.Code)
	}
}

func TestRepositoryMemberCanDeployWithoutPrivateCatalogAccess(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/memberships/orgs":
			fmt.Fprint(w, `[{"state":"active","role":"member","organization":{"id":600,"login":"acme"}}]`)
		case "/user/installations/987/repositories":
			fmt.Fprint(w, `{"repositories":[{"id":42,"full_name":"acme/project","owner":{"id":600,"login":"acme"}}]}`)
		case "/repos/acme/project/branches":
			fmt.Fprint(w, `[{"name":"main"}]`)
		case "/repos/acme/project/commits/refs/heads/main":
			fmt.Fprintf(w, `{"sha":%q}`, strings.Repeat("a", 40))
		case "/repos/acme/project/contents/.qiniu/deploy.yaml":
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte("version: 1\ninstall_command: npm ci\nstart_command: npm start\nport: 3000\n")))
		default:
			t.Errorf("unexpected GitHub request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	store := state.New(t.TempDir())
	srv := newTestServer(t, store, api.URL, &fakeSandbox{})
	srv.Close()
	srv.loopCtx = context.Background()
	srv.sandboxHTTP = deploymentTemplateTestClient()
	account, _, err := store.GetAccountByOAuthIdentity("github", "hubot-id")
	if err != nil {
		t.Fatal(err)
	}
	saveTestGitHubOAuthToken(t, store, account.ID, srv.cfg.AuthEncryptionKey.Value(), "user-token")
	inst, err := store.UpsertGitHubInstallation(state.GitHubInstallation{AccountID: account.ID, InstallationID: 987, AccountType: "organization", AccountLogin: "acme", GitHubAccountID: 600})
	if err != nil {
		t.Fatal(err)
	}
	key, err := encryptSecret("provider-key", srv.cfg.AuthEncryptionKey.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAccountPreference(state.AccountPreference{ScopeType: state.AccountScopeTypeGitHubInstall, ScopeID: 987, Namespace: accountPreferenceNamespaceSandbox, Key: accountPreferenceKeySandboxService, ValueJSON: `{"api_url":"https://us-south-1-sandbox.qiniuapi.com"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAccountSecret(state.AccountSecret{ScopeType: state.AccountScopeTypeGitHubInstall, ScopeID: 987, KeyType: state.AccountSecretTypeSandboxAPIKey, EncryptedValue: key}); err != nil {
		t.Fatal(err)
	}
	catalogPath := fmt.Sprintf("/user/deployment-templates?installation_id=%d", inst.ID)
	for _, repo := range []string{"", "acme/hidden"} {
		if rec := deploymentAuthCall(srv, "GET", catalogPath+"&repository_full_name="+repo, ""); rec.Code != 403 {
			t.Fatalf("ungated catalog %d", rec.Code)
		}
	}
	rec := deploymentAuthCall(srv, "GET", catalogPath+"&repository_full_name=acme/project", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"template_id":"public-base"`) {
		t.Fatalf("public choices %d %s", rec.Code, rec.Body.String())
	}
	for _, private := range []string{"custom", "deploy-template-a", "region", "provider-key"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("catalog leaked %s", private)
		}
	}
	if rec := deploymentAuthCall(srv, "GET", fmt.Sprintf("/user/deployment-refs?installation_id=%d&repository_full_name=acme/project&source=branch", inst.ID), ""); rec.Code != 200 {
		t.Fatalf("member refs %d %s", rec.Code, rec.Body.String())
	}
	payload := func(template string) string {
		return fmt.Sprintf(`{"installation_id":%d,"repository_full_name":"acme/project","source":"branch","ref":"main","template_id":%q}`, inst.ID, template)
	}
	for _, template := range []string{"deploy-template-a", "unbuilt"} {
		if rec := deploymentAuthCall(srv, "POST", "/user/deployments", payload(template)); rec.Code != 403 {
			t.Fatalf("non-public choice %d %s", rec.Code, rec.Body.String())
		}
	}
	rec = deploymentAuthCall(srv, "POST", "/user/deployments", payload("public-base"))
	if rec.Code != 201 {
		t.Fatalf("member create %d %s", rec.Code, rec.Body.String())
	}
	var created deploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	d, err := store.GetDeployment(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.ExecutionAccountID != account.ID || d.ScopeType != state.AccountScopeTypeGitHubInstall || d.ScopeID != 987 {
		t.Fatal("incorrect execution scope")
	}
	// Reuse an immutable revision created by a different account.
	d.AccountID = account.ID + 10
	d.ExecutionAccountID = d.AccountID
	d.ID = "other-creator"
	d.Status = state.DeploymentStatusCompleted
	d, err = store.CreateDeployment(d)
	if err != nil {
		t.Fatal(err)
	}
	rec = deploymentAuthCall(srv, "POST", "/user/deployments/"+d.ID+"/retry", `{"mode":"revision","revision_number":1}`)
	if rec.Code != 202 {
		t.Fatalf("shared retry %d %s", rec.Code, rec.Body.String())
	}
	saved, err := store.GetDeployment(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccountID != d.AccountID || saved.ExecutionAccountID != account.ID || saved.RevisionNumber != 2 {
		t.Fatal("retry replaced creator or retained wrong execution actor")
	}
}
