package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	deploy "github.com/miclle/deployd"
	deploygit "github.com/miclle/deployd/source/git"
	"github.com/qiniu/ci-runner/internal/config"
	deployconfig "github.com/qiniu/ci-runner/internal/deployment/config"
	"github.com/qiniu/ci-runner/internal/sandboxrunner"
	"github.com/qiniu/ci-runner/internal/state"
)

func TestDeploymentLivePublicWorker(t *testing.T) {
	if os.Getenv("RUNNERD_DEPLOY_LIVE") != "1" {
		t.Skip("manual real Sandbox acceptance")
	}
	configPath := os.Getenv("RUNNERD_DEPLOY_LIVE_CONFIG")
	if !filepath.IsAbs(configPath) {
		t.Fatal("RUNNERD_DEPLOY_LIVE_CONFIG must be an absolute local config path")
	}
	accountID, err := strconv.ParseInt(os.Getenv("RUNNERD_DEPLOY_LIVE_ACCOUNT_ID"), 10, 64)
	if err != nil || accountID <= 0 {
		t.Fatal("RUNNERD_DEPLOY_LIVE_ACCOUNT_ID must identify a configured account")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal("local config load failed")
	}

	cfg.DeploymentWorkRoot = "/tmp/runnerd-deploy125-acceptance"
	original := state.NewWithOptions(state.Options{Backend: cfg.StateBackend, DatabaseDSN: cfg.StateDatabaseDSN.Value()})
	srv := New(cfg, original, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	service, snapshot, err := srv.sandboxServiceForDeployment(ctx, accountPreferenceScope{Type: state.AccountScopeTypeAccount, ID: accountID})
	if err != nil {
		t.Fatal("account Sandbox config unavailable")
	}
	template := os.Getenv("RUNNERD_DEPLOY_LIVE_TEMPLATE")
	if template == "" {
		template = "base"
	}
	cfg.DeploymentTemplateID = template
	if catalog, ok := service.(sandboxrunner.DefaultTemplateCatalog); ok {
		entries, err := catalog.ListDefaultTemplates(ctx)
		if err != nil {
			t.Fatal("public template catalog unavailable")
		}
		for _, entry := range entries {
			for _, name := range entry.Names {
				if name == template && entry.Runnable() {
					cfg.DeploymentTemplateID = entry.TemplateID
				}
			}
		}
	}
	srv.cfg.DeploymentTemplateID = cfg.DeploymentTemplateID
	// The isolated worker DB tests execution only. Repository admission through
	// GitHub OAuth/App authorization is a separate API acceptance gate.
	const commit = "fe64a78977093d7c7fd7fb9a159975c5f73f8ff6"
	response, err := (&http.Client{Timeout: 15 * time.Second}).Get("https://raw.githubusercontent.com/qiniu/deployment/" + commit + "/.qiniu/deploy.yaml")
	if err != nil {
		t.Fatal("public source config unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("public source config read failed")
	}
	spec, err := deployconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := deploy.NewPlan(deploy.ResolvedSource{SourceID: githubDeploymentSourceURL("qiniu/deployment"), CommitSHA: commit}, spec)
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := json.Marshal(plan.Spec())
	encrypted, err := encryptSecret(string(specJSON), cfg.AuthEncryptionKey.Value())
	if err != nil {
		t.Fatal("spec encrypt failed")
	}
	evidence := []map[string]any{}
	for _, mode := range []string{"stop", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			store := state.New(t.TempDir())
			worker := srv.newDeploymentWorker()
			worker.Store = store
			worker.Source = func(context.Context, state.Deployment) (deploy.Source, error) {
				return deploygit.New(githubDeploymentSourceURL("qiniu/deployment"), deploygit.Options{Ref: commit})
			}
			worker.Event = func(_ state.Deployment, event state.DeploymentEvent) {
				if err := store.AppendDeploymentEvent(event); err != nil {
					t.Error("event persistence failed")
				}
			}
			lifetime := 10 * time.Minute
			if mode == "expiry" {
				lifetime = 3 * time.Minute
			}
			record := state.Deployment{ID: "live125-" + mode + "-" + newID(), AccountID: accountID, ScopeType: state.AccountScopeTypeAccount, ScopeID: accountID, GitHubInstallationID: 1, RepositoryID: 1, RepositoryFullName: "qiniu/deployment", Ref: "example/vite", CommitSHA: commit, ConfigPath: ".qiniu/deploy.yaml", SnapshotVersion: plan.Snapshot().Version, SpecHash: plan.Snapshot().SpecHash, SpecJSONEncrypted: encrypted, Status: state.DeploymentStatusQueued, DeploymentTemplateID: cfg.DeploymentTemplateID, SandboxAPIURL: snapshot.APIURL, SandboxAPIKeyEncrypted: snapshot.EncryptedAPIKey, SandboxConfigSource: snapshot.Source, SandboxRegion: srv.sandboxRegionForAPIURL(snapshot.APIURL), ExpiresAt: time.Now().UTC().Add(lifetime)}
			if _, err := store.CreateDeployment(record); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := worker.Stop(cleanup, record.ID, false); err != nil {
					t.Error("deferred Sandbox cleanup failed")
				}
			}()
			started := time.Now().UTC()
			if err := worker.Process(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			running, err := store.GetDeployment(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("deployment=%s status=%s sandbox=%s endpoint=%s expires_at=%s", running.ID, running.Status, running.SandboxID, running.Endpoint, running.ExpiresAt.Format(time.RFC3339))
			if running.Status != state.DeploymentStatusRunning {
				events, _, _ := store.ListDeploymentEvents(record.ID, 0, 20)
				for _, event := range events {
					if event.EventType != "output" {
						t.Logf("failure evidence stage=%s message=%s", event.Stage, event.Message)
					}
				}
				t.Fatal("real service did not become ready")
			}
			probeClient := &http.Client{Timeout: 15 * time.Second}
			public, err := probeClient.Get(running.Endpoint)
			if err != nil {
				t.Fatal("public endpoint probe failed")
			}
			html, _ := io.ReadAll(io.LimitReader(public.Body, 65536))
			public.Body.Close()
			if public.StatusCode != http.StatusOK || !bytes.Contains(bytes.ToLower(html), []byte("<!doctype html>")) {
				t.Fatalf("public HTTP evidence status=%d bytes=%d", public.StatusCode, len(html))
			}
			// New controller instance reuses only durable state and the exact attempt.
			resumed := srv.newDeploymentWorker()
			resumed.Store = store
			resumed.Event = worker.Event
			if err := resumed.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			afterRestart, _ := store.GetDeployment(record.ID)
			if afterRestart.Status != state.DeploymentStatusRunning || afterRestart.SandboxID != running.SandboxID || afterRestart.Attempt != running.Attempt {
				t.Fatal("restart replaced or stopped healthy service")
			}
			if mode == "stop" {
				if err := resumed.Stop(ctx, record.ID, false); err != nil {
					t.Fatal(err)
				}
			} else {
				for time.Now().Before(running.ExpiresAt.Add(20 * time.Second)) {
					if err := resumed.RunOnce(ctx); err != nil {
						t.Logf("expiry reconciliation error category=%T", err)
					}
					latest, _ := store.GetDeployment(record.ID)
					if latest.Status == state.DeploymentStatusFailed {
						t.Fatalf("unexpected failure stage=%s reason=%s failed_at=%s expires_at=%s provider_released=%v", latest.FailureStage, latest.FailureReason, latest.FailedAt.Format(time.RFC3339Nano), latest.ExpiresAt.Format(time.RFC3339Nano), latest.SandboxID == "")
					}
					if latest.Status == state.DeploymentStatusExpired && latest.SandboxID == "" {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("acceptance deadline exceeded")
					case <-time.After(10 * time.Second):
					}
				}
			}
			terminal, err := store.GetDeployment(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := state.DeploymentStatusCompleted
			if mode == "expiry" {
				want = state.DeploymentStatusExpired
			}
			if terminal.Status != want || terminal.SandboxID != "" {
				t.Fatalf("cleanup not confirmed status=%s sandbox=%s", terminal.Status, terminal.SandboxID)
			}
			factory := &serverDeploymentRuntimeFactory{server: srv, templateID: cfg.DeploymentTemplateID}
			if handle, err := factory.Connect(ctx, running); err != nil || handle != nil {
				t.Fatalf("provider resource release not confirmed: exists=%v error_category=%T", handle != nil, err)
			}
			evidence = append(evidence, map[string]any{"mode": mode, "started_at": started, "finished_at": time.Now().UTC(), "repository": "qiniu/deployment", "commit": commit, "deployment_id": record.ID, "sandbox_id": running.SandboxID, "endpoint": running.Endpoint, "expires_at": running.ExpiresAt, "terminal_status": terminal.Status, "public_http_status": public.StatusCode, "controller_reconnect_preserved_attempt": true, "provider_missing_after_cleanup": true})
		})
	}
	if path := os.Getenv("RUNNERD_DEPLOY_LIVE_EVIDENCE"); path != "" {
		if !filepath.IsAbs(path) {
			t.Fatal("RUNNERD_DEPLOY_LIVE_EVIDENCE must be absolute")
		}
		payload, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal("evidence write failed")
		}
	}
}
