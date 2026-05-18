# GitHub Runner Ubuntu 24.04 E2B Template

This template is a lightweight GitHub Actions self-hosted runner image for E2B sandboxes.

It intentionally does not copy the full GitHub-hosted Ubuntu 24.04 image. The official image includes many heavy stacks, browsers, SDKs, Android tooling, databases, and cloud CLIs. For this runner service, the template focuses on the pieces needed for stable Go CI jobs:

- `linux/amd64` Ubuntu 24.04 base, even when building from a Mac arm64 machine.
- Core shell, archive, network, build, Git, and Git LFS tools.
- `gawk`, because the GitHub runner update script uses `awk`.
- Python 3 and Node.js/npm for common Actions runtime needs.
- A writable `user` account and writable temp-backed runner homes.
- Preinstalled GitHub Actions runner package under `/opt/actions-runner`.
- Writable tool cache directory under `/opt/hostedtoolcache`.

Reference:

- GitHub hosted runner software list: https://github.com/actions/runner-images/blob/main/images/ubuntu/Ubuntu2404-Readme.md
- E2B template CLI: https://e2b.dev/docs/sdk-reference/cli/v2.7.3/template

## Build with E2B template build v2

```bash
cd templates/github-runner-ubuntu-24.04
npm install
npm run build:prod
```

For an isolated development tag:

```bash
npm run build:dev
```

The build prints a template ID and template name. Use the production template name or ID as:

```bash
export SANDBOX_TEMPLATE_ID="<template-id>"
export RUNNER_VERSION="2.334.0"
```

Do not use `e2b template build -d e2b.Dockerfile` for this template; that command uses E2B's deprecated v1 build system.

The Dockerfile pins the base image with:

```dockerfile
FROM --platform=linux/amd64 ubuntu:24.04
```

and installs the `actions-runner-linux-x64` package. This is intentional because the sandbox runtime expects Linux amd64; do not switch it to arm64 for local Mac builds.

## Smoke Test

```bash
e2b sbx create --detach <template-id>
e2b sbx list
```

Then run a workflow using:

```yaml
runs-on: [self-hosted, e2b]
```

## Notes

The runner service copies `/opt/actions-runner` into `/tmp/actions-runner` when present. This avoids downloading the runner tarball for every job.

The runner service also sets:

```bash
HOME=/tmp/runner-home
XDG_CONFIG_HOME=/tmp/runner-home/.config
```

This keeps Git config access away from the earlier broken `/home/user/.config` path. Go-specific paths are left to `actions/setup-go` defaults.
