# E2B GitHub Runner

Small Go service that starts ephemeral GitHub Actions self-hosted runners inside E2B sandbox instances.

## Configuration

Required environment variables:

- `E2B_API_KEY`
- `E2B_API_URL`
- `E2B_DOMAIN`
- `GITHUB_TOKEN`
- `GITHUB_WEBHOOK_SECRET`
- `GITHUB_OWNER`
- `GITHUB_REPO`
- `SANDBOX_TEMPLATE_ID`

Optional environment variables:

- `HTTP_ADDR` defaults to `:8080`
- `STATE_DIR` defaults to `./var/runners`
- `RUNNER_LABELS` defaults to `self-hosted,e2b`
- `RUNNER_VERSION` defaults to `2.334.0`
- `SANDBOX_TIMEOUT_SECONDS` defaults to `3600`
- `MAX_CONCURRENT_RUNNERS` defaults to `1`
- `GITHUB_API_BASE_URL`

## Run

```bash
go run ./cmd/runnerd
```

Use `runs-on: [self-hosted, e2b]` in the target workflow. Configure a GitHub webhook for `workflow_job` events pointing at `POST /webhooks/github`.
