# Deploy configuration

[中文](zh/deployment.md)

Issue #125 starts with one temporary HTTP service per deployment. The repository
configuration is read from `.qiniu/deploy.yaml` at the selected commit before a
Sandbox is created.

The first schema is deliberately small and strict:

```yaml
version: 1
workingDirectory: .
installCommand: npm ci
startCommand: npm run start -- --host 0.0.0.0 --port 3000
port: 3000
healthcheck:
  path: /health
  timeoutSeconds: 60
```

`workingDirectory` is relative to the checked-out repository and may not escape
it. `installCommand` and `startCommand` execute trusted repository code in the
selected runtime; do not put credentials in either command. `healthcheck.path`
must be an origin-relative path. The parser rejects unknown fields, multiple YAML
documents, unsupported versions, unsafe paths, invalid ports, and files larger
than 64 KiB.

CamelCase fields above are the existing repository contract. The snake_case aliases
`working_directory`, `install_command`, `start_command`, and
`healthcheck.timeout_seconds` are also accepted. Supplying both spellings for
one field is rejected.

The parser maps this repository format to deployd's structured `Spec`. deployd
does not own this YAML format. It validates the execution parameters and binds
them to the resolved full Commit in an immutable plan. Deploy controllers must
persist the plan before provisioning, keep commands out of lifecycle events, and
keep Sandbox credentials outside the plan.

At application startup, runnerd supplies
`__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS` with the exact hostname of the current
Sandbox's application endpoint. Vite preview can then accept public health
checks without a repository-specific hostname or a wildcard Host allowlist.
This transient environment value is not part of the persisted execution Spec
and is not supplied to installation commands.

Startup confirmation has a local deadline. That deadline is not sent as the
envd `Connect-Timeout-Ms` process lifetime: after PID confirmation, cancelling
the observation stream leaves the service running until stop or Sandbox expiry.
Clone and installation commands retain their execution deadlines.

Choose a template from the selected account or organization's runnable public
or owned Sandbox catalog when creating a deployment. `runnerd.yaml` can provide
an optional default for API clients that omit `template_id`:

```yaml
deploy:
  template_id: your-deploy-sandbox-template
  work_root: /var/lib/runnerd/deployments
  max_concurrent_per_scope: 3
  retention_hours: 168
  allow_admin_fallback: false
```

`template_id` is intentionally separate from Runner Specs. `work_root` is a
controller-owned absolute path inside the Sandbox used only for transient
per-attempt workspace names. The worker creates an independently
tagged Sandbox, checks out the pinned Commit, persists deployd checkpoints, and
keeps the Sandbox alive after readiness. It records the Sandbox ID, attempt,
runtime ID and process tag so a restart can reconnect to the exact attempt.
Command output is bounded and redacted before any optional event sink receives
it. Deployment history retains at most 2,000 events and 8 MiB of message data
per deployment, keeping the event timeline bounded while preserving the newest
evidence. The worker runs independently of a configured default. Admission
validates the selected physical template ID with the effective scoped credentials
and rejects missing/unavailable templates or credentials before queuing. Existing
queued records without a template fail visibly; create a new deployment with a
selected template. Creation and retry persist a queue event and wake the worker
immediately; periodic reconciliation remains a fallback.

The region comes from the effective scope's configured Sandbox endpoint. The
region identifier, endpoint, encrypted credential and dedicated Deploy template
binding are saved with admission, so changing global configuration cannot move
an admitted deployment to another region or template. Retries resolve current
scoped credentials again; the redeploy mode decides whether to refresh a branch
source or reuse an immutable source/template binding.
The platform CI fallback is disabled for Deploy unless
`allow_admin_fallback: true`; its existing enabled/audience restrictions still
apply. Deploy never uses fork sponsorship or Cache S3 credentials.

The single Deploy worker periodically reconnects to running attempts and checks
the saved runtime/PID/tag. Healthy services survive controller restart; transient
provider failures and identity mismatches preserve resources for another check.
The worker rechecks expiry after provider I/O, so a lookup that crosses the
deadline records normal expiry rather than an unexpected process failure.
An interrupted `creating` attempt is reconciled by exact operation metadata and
compensated without replaying user commands or creating a replacement. Unknown
creation outcomes remain nonterminal until their resource can be found and
cleaned, or a successful lookup after expiry confirms no remaining resource.
Stopping an unknown create returns `202` until compensation finishes. A confirmed
missing Sandbox counts as released. Reconnection preserves remaining lifetime;
the initial provider expiry is retained when supplied. This is single-process
execution recovery; multi-instance Deploy workers are not supported or verified.

Each account or organization scope admits at most
`max_concurrent_per_scope` queued/running deployment attempts. Admission is
serialized in the state transaction, so a concurrent request receives
`429 deployment_quota_exceeded` instead of creating an extra Sandbox. Terminal
records whose Sandbox has been released are retained for `retention_hours`
before their deployment row and bounded events are pruned; audit events remain
available as the durable control-plane history.

The first version exposes the provider-generated HTTPS endpoint as a **public,
unauthenticated** test address. `endpoint_access: "public"` is returned with a
ready deployment. The URL is not an authorization mechanism: applications that
serve sensitive data must implement their own authentication, and secrets must
not be placed in the repository commands or output. An authenticated reverse
proxy or signed access contract is a separate follow-up.

This configuration describes source deployment. It does not yet define
application secrets, persistent databases, custom domains, multi-service
composition, or automatic CI/PR triggers.

## GitHub App permissions

Private repository Deploy requires the App's repository **Contents: Read-only**
permission so runnerd can read `.qiniu/deploy.yaml` and the pinned Commit. The
existing user OAuth flow must also be able to enumerate the repository and its
installation access. Like Jobs, Deploy authorizes the exact OAuth/App
installation-repository pair, independently of creator identity or organization
role. Repository members may read, create, redeploy, stop and delete deployments.
Sandbox Preferences and private template catalogs remain owner-managed; managing
organization settings still requires **Members: Read-only** and active owner
membership (`role: admin`). Deploy never inherits CI Secrets, Cache S3
credentials, or fork sponsorship permissions.

## First control-plane slice

The authenticated API currently exposes the persisted plan boundary. The
`installation_id` is the local linked installation record ID used by the
existing Repositories and Preferences APIs; the external GitHub Installation
ID is resolved server-side.

- `GET /user/deployment-templates?installation_id=<local-id>&repository_full_name=<owner/repo>`
  returns runnable owned/public templates for a scope owner. Other users must
  authorize the exact repository and receive only runnable public template IDs
  and names, without private catalogs or scope metadata. Non-owner creation
  accepts only a verified public template; existing saved revisions can be
  redeployed by repository members. Deploy fallback remains opt-in.
- `GET /user/deployment-refs?installation_id=<local-id>&repository_full_name=<owner/repo>&source=branch|tag&page=1`
  lists at most 100 reference names per page, with `has_more`. It requires the
  same exact OAuth/App repository intersection as creation;
  missing or rejected OAuth access fails closed.
- `POST /user/deployments` accepts a selected physical `template_id`, optional `template_name`, `template_cpu`,
  `template_memory_mb`, and `template_disk_mb` snapshots from the template catalog, a linked local `installation_id`, an exact
  `repository_full_name`, a `ref`, an optional `source` (`branch` or `tag`), an optional `config_path`,
  and an optional `ttl_seconds` (five minutes to 24 hours; the default is one
  hour). The `Idempotency-Key` header can replay the same request safely.
  The original normalized admission fields are hashed once and retained on the
  deployment. Replay returns the existing resource without starting another
  attempt, even after provider expiry updates, stop, or redeploy. Different
  execution fields return 409. Historical rows without this hash cannot safely
  reconstruct the request and reject keyed replay; use a new key for new work.
  Deployment IDs are 26 lowercase Base32 characters encoding 128 bits, safe
  to use directly in URLs. Keyed requests derive the ID from the account and
  key; unkeyed requests generate a new random ID. Clients should treat IDs as
  opaque strings. No legacy ID replay fallback is provided before launch.
  With `source`, the server qualifies the name as `refs/heads/<name>` or
  `refs/tags/<name>` before resolution, so identical branch/tag names stay
  distinct. API clients that omit `source` can still send a branch/tag/commit
  ref. Responses expose the source and unqualified display name when available.
- `GET /user/deployments` reads local history after filtering authorized exact
  installation-repository pairs, before count and pagination. Creator identity
  does not restrict visibility.
- `GET /user/deployments/{id}`, saved events, revisions and lifecycle actions
  share Jobs' repository authorization cache: refresh after 20 seconds, hard
  expiry at 30 seconds, with coalesced refreshes. A cache miss may contact GitHub;
  deployment content never requires Sandbox I/O. Missing/rejected OAuth access
  fails closed, including for the original creator.
  Create/retry records the execution account for each revision; the worker
  revalidates that actor before source checkout. Historical zero actor fields
  use the original creator, whose identity and idempotency key remain stable.
- `GET /user/deployments/{id}/events` returns bounded lifecycle/output events with
  exclusive `before_id` and `after_id` cursors (mutually exclusive). Initial pages
  contain the newest events in chronological order; `after_id` returns the next
  ascending page so active polling can catch up without skipping output.
- `POST /user/deployments/{id}/stop` (or `DELETE /user/deployments/{id}`) stops
  the tagged process and removes the deployment Sandbox. Cleanup failures remain
  visible as `failed` and are retried by the worker.
- `POST /user/deployments/{id}/retry` requeues a terminal deployment only after
  its prior Sandbox evidence has been cleaned. The JSON body may select
  `{"mode":"latest"}` for a branch deployment, which resolves the branch again
  and saves a new immutable plan, or `{"mode":"revision","revision_number":N}`
  to reuse a retained revision. An omitted body reuses the current revision.

Creation resolves the ref to a full Commit SHA, reads the configuration from
that exact commit, creates a deployd immutable plan, encrypts the normalized
execution Spec, and commits the deployment record with its audit event in one
transaction. Commands are never returned by these APIs. With the worker
enabled, records advance through `planning`, `creating`, and `running`; expiry
reconnects to the saved Sandbox/PID attempt, stops the tagged process, and
deletes the Sandbox before recording `expired`. A failed create or cleanup is
recorded as `failed` without claiming that remote cleanup succeeded.
Successful compensation retains the original failure stage and reason in the
deployment and revision, including recovery after an interruption during cleanup.

`/deployments` shows only the deployment list and a Create deployment link to
`/deployments/new`. It loads no GitHub App or form catalogs. `/deployments/new`
shows only the creation form and loads no deployment list or logs.

Deployment links use in-app navigation, including list/create/detail transitions
and deployment actions from Jobs. These transitions retain the current session
without reloading the document or repeating the initial sign-in check. Browser
back/forward and opening links in a new tab remain supported; a full reload still
checks the session, and every API request retains server-side authorization.

The creation form waits for an explicit repository selection,
then offers Version source (Branch/Tag) and
Select branch or Select Tag from the repository's live reference list. Switching
repository, scope, or source discards stale discovery responses. Empty lists
block submission; failed discovery can be retried, and more versions load in
pages without losing the current choice.
If the App cannot read versions, the form asks the installation owner to approve
Contents read permission and retry; repository visibility alone does not prove
App Contents access.

The creation page opens the repository/PR-context deployment detail
after success. The URL selects the resource directly, including deployments
outside the first list page. Reload and browser history preserve selection. The
detail shows chronological stage/stdout/stderr rows, polls active attempts,
merges new pages with loaded history, and provides earlier-log and refresh
actions. Log read failures retain already loaded output and show a retry prompt.

## Deployment revisions and logs

A Deployment remains a stable resource. Initial creation atomically inserts
Revision #1; `POST /user/deployments/{id}/retry` creates the next revision. The
latest-branch mode resolves a moving branch again, while revision mode copies
the selected pinned Commit and encrypted plan. Revision creation, queue evidence
and audit commit together. Only the
current revision can advance; old revisions retain source, status, endpoint,
confirmed Sandbox identity and log history. Execution attempts remain separate
provider fencing evidence rather than revision identifiers.

The local detail response includes `revision_number`, the newest 100 `revisions`,
`revisions_has_more`, and the last confirmed successful revision number. Use
`GET /user/deployments/{id}/revisions?before_number=N` for earlier revisions.
`GET /user/deployments/{id}/revisions/{number}/events` uses the same exclusive
`before_id`/`after_id` contract as the existing event endpoint; the existing
`/events` endpoint reads the current revision. These reads authorize the exact repository through Jobs' cache, set `Cache-Control: no-store`, and perform no provider I/O.
Encrypted plans and service credentials are never included in these responses.

The detail layout puts saved target configuration above a revision history and
log viewer. Each row contains a line number, a millisecond timestamp and the
raw retained message; stderr and failures have separate emphasis. Switching a
revision resets its cursors and rejects responses from the previous revision.
`/deployments/{id}?revision=N` preserves selection through refresh and history.
A selected old revision outside the newest page is loaded directly. No live
Sandbox probe is needed to render the page.

Existing databases are upgraded additively with revision columns and indexes,
and missing revisions are backfilled as #1 without changing existing source,
secrets, timestamps, attempt evidence or logs. Earlier executions of a legacy
row are retained together in its imported #1; lost historical state is not
reconstructed. Log count/byte budgets apply separately to each revision.
Terminal retention deletes the Deployment, all revisions and their logs
atomically while retaining audit events.

## Iteration plan

Public execution acceptance on 2026-10-13 used the public `base` template and
`qiniu/deployment` commit `fe64a78977093d7c7fd7fb9a159975c5f73f8ff6`, with the
repository configuration unchanged. The real worker returned HTTP 200, preserved
the attempt during controller-object reconnection, completed manual stop, and
recorded `expired` after a three-minute TTL. Both cleanups were confirmed by
provider 404 responses.

Authenticated local browser acceptance on the same day used `qiniu/deployment`
branch `example/vite` and `base`: creation returned 201, navigated to the detail,
showed installation/start/readiness output, and preserved the detail and logs
after reload. Public HTML returned HTTP 200 with no browser console/page errors.
The stop API returned 200; the record reached `completed` and cleared its Sandbox
reference. The Chinese form's Branch/Tag labels and empty-Tag submission guard
were also checked. At that point, private repository access and authenticated
retry/delete flows remained separate gates; later evidence is recorded below.

On 2026-10-14, the user confirmed that real Sandbox acceptance passed following
the Web terminal implementation. This records user-performed acceptance; no
additional command transcript or per-scenario results were supplied. The separate
Issue #125 gates above remain open.

An additional 2026-10-14 run used ordinary Google Chrome, the same public source
Commit and `base`. Creation and reload preserved the short deployment ID and
logs. A full runnerd process restart changed the PID from 99226 to 4048 while
preserving deployment ID, revision #1, attempt #1, Sandbox ID, runtime ID,
process ID/tag, endpoint, expiry and Commit. Public HTML returned HTTP 200 both
before and after restart. The authenticated stop then reached `completed` with
an empty Sandbox reference. This run did not separately probe provider 404 after
stop or exercise concurrent CI Jobs.

Real provider fault injection on 2026-10-14 used the opt-in `create-timeout`
worker subtest: one successful Sandbox create response was discarded and the
first metadata lookup was forced to time out. The record remained `creating`
with attempt #1 and no confirmed Sandbox ID. A new worker then recovered the
exact operation, compensated it, retained `failed` / `recovery`, and confirmed
provider 404 after cleanup. Only one create request reached the transport.
The transport faults are synthetic; creation, metadata reconciliation and
resource release use the real provider. This is worker evidence, not proof of
duplicate authenticated API submissions or an actual provider outage.

Ordinary Chrome acceptance on 2026-10-14 at `09c3a81` also redeployed revision
#1 as #2 with an unchanged saved plan, then selected latest branch code for #3.
Both public endpoints returned HTTP 200. Reload preserved revision #1 and its
logs, and the historical terminal remained unavailable. Both new revisions were
stopped; the final record was completed with an empty Sandbox reference and all
three revisions retained. The branch had not advanced, so this does not prove
moving-branch behavior. Provider 404 was not separately checked for these stops.
This predates repository-shared authorization and does not prove cross-account
live acceptance; private repository/fork PR runs remain deferred.

Authenticated API acceptance on 2026-10-14 used the running local runnerd,
`qiniu/deployment` / `example/vite` at the same Commit above, and public `base`.
Two concurrent submissions of the same key created one deployment and attempt.
The configured scope limit of three rejected an extra admission with 429 and
no row; admission resumed after release. Public HTML returned 200. DELETE
completed, cleared the Sandbox reference and confirmed provider absence; replay
after cleanup returned the same terminal resource with its 20 events retained.
All four admitted test resources were stopped. The test signs a short-lived
in-memory session for an existing local account; it exercises real authorization
and provider execution, but does not test OAuth login or a second user.
This exposed a replay defect caused by provider expiry precision; creation now
compares its immutable request hash instead of mutable expiry/revision fields.

A separate real worker `start-failure` run injected `install: true` and
`start: exit 23` into the pinned public plan. Exactly one Sandbox was created;
the deployment and revision retained `failed` / `probing`, and provider absence
confirmed cleanup. This is synthetic-plan execution evidence, not repository
configuration admission evidence.

The running local worker also passed an accelerated retention check with the
configured 168-hour window. Only the completed API-test record's `updated_at`
was moved back 169 hours after confirming its Sandbox was absent. The regular
sweep removed its deployment, one revision and 20 events while retaining both
audit events. This proves deployed cleanup with timestamp injection, not seven
days of naturally elapsed retention.

Ordinary Chrome narrow-screen acceptance on 2026-10-14 at `84abfa8` used the
real local runnerd history with three revisions and selected revision #1.
At a 375 × 667 viewport, document scroll width stayed 375; both history and
logs were 343 pixels wide at x=16. History ended at y=1290.5 and logs started
at y=1524.5, confirming stacked layout. All 20 saved log rows remained present
and DevTools reported no console errors. This uses real API data, independently
of the production fixture; it observes a stopped historical deployment.

Real CI independence acceptance on 2026-10-14 used
[`qiniu/deployment` Commit `bd336bd`](https://github.com/qiniu/deployment/commit/bd336bdc3e93d5aca8fd25a8aa2258d90e644123)
and [workflow Run 38533165014, attempt 2](https://github.com/qiniu/deployment/actions/runs/38533165014/attempts/2).
Job `115752065357` and deployment `pgcwpifs2ovrpw37oayfvbz44y` were observed
running concurrently with different Sandboxes. The Job succeeded, runnerd retained
the success, and the provider confirmed Runner Sandbox absence. The same
Deployment stayed running with attempt #1 and public HTTP 200 immediately after
cleanup and after another 20 seconds. GitHub listed only the expected CI Runner
while it ran, then zero repository Runners while the Deployment was still running.
The API gate finally stopped all its own deployments and confirmed provider
absence. The first CI attempt completed before overlap was observed and was not
counted as acceptance. This proves the public same-source cleanup path, not
private/fork execution or multi-instance recovery.

Real public fork acceptance on 2026-10-14 used
[`miclle/qiniu-deployment` branch `example/vite`](https://github.com/miclle/qiniu-deployment/tree/example/vite)
and [draft PR `qiniu/deployment#1`](https://github.com/qiniu/deployment/pull/1).
The fixture adds only HTML repository/version markers; deployment commands and
Vite Host validation remain unchanged. Branch deployment
`ifad75cgk6eyltqlltfhdbecnu` pinned Commit `9885680` and served `fork-preview-v1`.
After the branch and PR head advanced to `90a244a` (`fork-preview-v2`), PR deployment
`qrshzidd2vu7aupytig7l4yyqu` still pinned full SHA
`98856804498a253e8ae8ca35f19cd951b88ea769`, retained source repository
`miclle/qiniu-deployment`, and served `fork-preview-v1` with HTTP 200.
Both real API runs passed concurrent idempotency, scope quota/re-admission,
terminal replay, DELETE and provider-confirmed release of all their test resources.
This proves public fork and older PR Commit execution; live GitHub access
revocation, private Contents permissions and a second user's access remain separate gates.

1. **Preflight and plan (done):** strict repository configuration, immutable
   Commit resolution, exact OAuth/App repository intersection, encrypted Spec
   snapshot, and audited durable state.
2. **Sandbox execution and recovery (done locally):** resolve the effective
   scoped Sandbox service and dedicated Deploy template, create one independent
   Sandbox, materialize the exact Commit, run `deployd.Apply`, persist
   checkpoints, enforce Sandbox/PID/attempt fencing, and compensate unknown
   create outcomes by reconciling exact provider operation metadata before retry.
3. **Lifecycle API (done locally):** stop/delete/retry operations, TTL expiry,
   bounded deployment events and logs, and status-aware cleanup evidence are now
   available in the backend; real lost-response reconciliation and cleanup passed.
4. **User surface (first slice done):** `/deployments` lists local history,
   `/deployments/new` handles authorized repository and Branch/Tag selection,
   template choice and immutable deployment creation, and the deployment detail
   shows status, endpoint, TTL, template resources (CPU, memory, and disk), lifecycle/output events and stop/delete/retry
   actions. Local verification and the authenticated public create/detail/reload/
   stop browser flow passed; the remaining real acceptance gates are listed above.
5. **Acceptance:** run focused tests, local startup/build checks, then a real
   GitHub App and Sandbox deployment with stop, expiry, restart recovery, and
   unauthorized-access evidence.

## Deployment Web terminal

The detail page has Deployment logs and Terminal tabs. Only the current running,
unexpired revision with a Sandbox ID can open a terminal. Connection is manual;
switching tabs/revisions, leaving the detail page or stopping the deployment
closes the interactive shell. The shell can inspect processes and application
log files; it does not automatically replay the deployment log or attach to the
service process's stdout. Shell changes are local to that Sandbox.

Terminal routes use `/user/deployments/{id}/revisions/{number}/terminal`:
`POST` creates a PTY; `GET /{sessionID}/events` streams output;
`POST /{sessionID}/input` sends input; `POST /{sessionID}/resize` resizes it;
`DELETE /{sessionID}` closes it. Every access checks the current user's repository permission and shell ownership.
Closing an existing shell requires its creating account, so cleanup still works
after permissions or lifecycle change. Sessions bind the opening account,
deployment, revision, attempt and Sandbox identity; historical
revisions cannot connect to the current Sandbox. Persisted details/logs remain
local reads; opening a terminal uses the saved provider endpoint and encrypted
credential, without exposing them to the browser. Connect uses remaining
lifetime rather than a fixed Sandbox timeout. A local lifecycle check runs every
five seconds and open sessions revalidate remote access every thirty seconds;
closed/disconnected streams receive the existing thirty-second idle cleanup.

## GitHub context routes

Jobs and deployments use repository resource collections:

- `/github/{owner}/{repo}/jobs` and `/github/{owner}/{repo}/deployments`
- `/github/{owner}/{repo}/jobs?branch=example/vite`
- `/github/{owner}/{repo}/deployments?branch=example/vite`
- `/github/{owner}/{repo}/deployments?tag=release/v1`
- `/github/{owner}/{repo}/pull_requests/{number}/jobs` or `/deployments`
- `/github/{owner}/{repo}/runs/{number}/jobs` for Jobs without source context

Branch and Tag names are query values. Generated browser links leave `/` readable
while still encoding `&`, `#`, `+`, `%` and other reserved characters. Encoding is
applied before restoring only `%2F`; a literal `%2F` in a ref remains `%252F`.
`branch` and `tag` are mutually exclusive. Empty or repeated source filters are
invalid. Tags are deployment-only because Jobs do not retain reliable Tag evidence.
`commit={sha}` optionally filters a collection to an immutable commit;
branch Job group links always include it. Jobs use `job={id}` to select a resource.
Deployment details put the ID in the path:
`/github/{owner}/{repo}/deployments/{id}?branch=example/vite` (or `?tag=release/v1`),
and `/github/{owner}/{repo}/pull_requests/{number}/deployments/{id}`.
`revision=N` selects an immutable deployment revision.
Deployment detail URLs omit `commit`: the saved deployment/revision owns the
SHA. Detail headers show a short SHA linked to the source repository's GitHub
commit, with a full-SHA copy button. Returning to history lists the whole source
without adding a commit filter; explicitly filtered collection URLs still work.

Repository Jobs show only matching groups from the existing authorized history
window (up to 500 loaded Runner requests); an empty filter never falls back to
another repository. This route change does not add an unbounded Jobs archive.
Repository deployment lists filter saved revisions before database pagination.
The collection route lists saved deployments in that context; the detail route
loads the selected local deployment.
`/deployments` lists all authorized repository history; `/deployments/new` is the form.
A repository-only creation link prefills the repository and lets the user choose
a source. Existing Jobs links remain compatible and canonicalize to the current
Jobs URLs. Deploy is not released: old deployment source paths and
`?deployment={id}` links are unsupported. Existing API paths remain unchanged.

`/deployments/{id}` is a context-free local-record entry and replaces the URL with
its saved source context. Historical revisions use their own saved ref; their
Commit SHA is read from the stored revision, not the URL.
Repository/source text in a URL never grants access: detail reads still authorize
the exact repository pair and deployment ID, then canonicalize the display URL. History
filters (`repository_full_name`, `ref`, `commit_sha`, `pull_request_number`) on
`GET /user/deployments` run before pagination and match persisted revisions;
`selected_revision` identifies the matching historical revision. Content reads do not query the Sandbox provider; authorization may refresh GitHub access.

Both grouped and standalone ordinary-user Jobs provide deployment history and
"Deploy this commit" links using the selected Job's repository and PR/branch.
History spans commits; creation pins the selected Job's SHA, including historical
Jobs. Creation uses `/deployments/new?repository=...&source=...&ref=...&commit_sha=...`;
these values only prefill the form. The form waits for authorized repository
catalogs, preserves an explicitly selected PR even if it is closed,
and displays the fixed SHA. Changing repository/source/ref clears that pin;
"Use latest source commit" explicitly removes it. GitHub validates the final
source on submission. Jobs without a full SHA or source context omit the links.
Synthetic PR merge SHAs are rejected by the PR head-commit membership check.

## Pull request deployments

Create with `source: "pull_request"` and the positive PR number in `ref`.
The form lists open PRs; the API can explicitly target an existing closed PR.
This deploys the PR head, never GitHub's synthetic merge result. The authorized
base repository owns the Sandbox scope and quota. Base repository identity must
match GitHub's PR response; a deleted/unavailable head repository is rejected.
The server resolves `refs/pull/{number}/head`, verifies it matches the observed
head SHA, and reads configuration/checks out that immutable SHA through the base
repository. Fork-provided URLs never select credentials or checkout targets.
The App needs approved `Pull requests: Read-only` and `Contents: Read-only`.

Optional `commit_sha` pins a previously selected commit. A PR commit must be in
GitHub's bounded PR commit listing (up to 250); a branch commit must be an ancestor
of the current branch head; a Tag must still point to that exact commit. A
force-pushed/unavailable selection fails rather than silently deploying a new
head. PR revisions snapshot `source_repository_id`, `source_repository_full_name`
and `source_ref`; existing records migrate additively with empty source evidence.
The PR number is retained in its qualified ref and exposed as `pull_request_number`.
Latest redeploy resolves the current PR head again; revision redeploy copies the
saved source/plan evidence. Saved deployment history remains local and shared by authorized repository members.
