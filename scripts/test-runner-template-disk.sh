#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="$(mktemp -d)"
trap 'find "$workdir" -depth -delete' EXIT

cat >"$workdir/qshell" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  version)
    printf 'v%s\n' "${MOCK_QSHELL_VERSION:-2.19.13}"
    ;;
  'sandbox template list --format json')
    jq -n --arg alias "${MOCK_ALIAS:-github-runner-ubuntu-24-04-large}" --argjson disk "$MOCK_DISK" \
      '[{Aliases: [$alias], DiskSizeMB: $disk}]'
    ;;
  'sandbox template publish -y')
    echo 'Template fixture published'
    ;;
  *)
    echo "unexpected qshell command: $*" >&2
    exit 1
    ;;
esac
EOF
cat >"$workdir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cat "$MOCK_CATALOG"
EOF
chmod +x "$workdir/qshell" "$workdir/curl"

expect_failure() {
  local wanted_message="$1"
  shift
  if "$@" >"$workdir/output" 2>&1; then
    echo "expected failure: $*" >&2
    exit 1
  fi
  grep -Fq "$wanted_message" "$workdir/output" || {
    cat "$workdir/output" >&2
    echo "missing expected error: $wanted_message" >&2
    exit 1
  }
}

large_template_dir="$repository_root/templates/github-runner-ubuntu-24.04-large"
standard_template_dir="$repository_root/templates/github-runner-ubuntu-24.04"
operation_script="$repository_root/scripts/run-runner-template-operation.sh"
export QINIU_SANDBOX_API_URL=https://sandbox.invalid QINIU_API_KEY=fixture
export QSHELL="$workdir/qshell"
export MOCK_DISK=22222
expect_failure 'qshell >= 2.19.13 is required' \
  env MOCK_QSHELL_VERSION=2.19.12 bash "$operation_script" build "$large_template_dir"
expect_failure 'has disk size 22222 MiB; expected 81920 MiB' \
  bash "$operation_script" build "$large_template_dir"
expect_failure 'has disk size 22222 MiB; expected 81920 MiB' \
  bash "$operation_script" publish "$large_template_dir"

MOCK_DISK=81920 bash "$operation_script" publish "$large_template_dir" >"$workdir/output"
grep -Fq 'Template fixture published' "$workdir/output"
MOCK_DISK=81920 MOCK_QSHELL_VERSION=2.19.12 \
  bash "$operation_script" publish "$large_template_dir" >"$workdir/output"

expect_failure 'has disk size 22222 MiB; expected 20480 MiB' \
  env MOCK_ALIAS=github-runner-ubuntu-24-04 bash "$operation_script" build "$standard_template_dir"
expect_failure 'has disk size 22222 MiB; expected 20480 MiB' \
  env MOCK_ALIAS=github-runner-ubuntu-24-04 bash "$operation_script" publish "$standard_template_dir"
MOCK_ALIAS=github-runner-ubuntu-24-04 MOCK_DISK=20480 \
  bash "$operation_script" publish "$standard_template_dir" >"$workdir/output"

mkdir "$workdir/missing-disk" "$workdir/invalid-disk"
sed '/^disk_size_mb[[:space:]]*=/d' "$standard_template_dir/qshell.sandbox.toml" \
  >"$workdir/missing-disk/qshell.sandbox.toml"
sed 's/^disk_size_mb[[:space:]]*=.*/disk_size_mb = "20480"/' \
  "$standard_template_dir/qshell.sandbox.toml" \
  >"$workdir/invalid-disk/qshell.sandbox.toml"
expect_failure 'template config has no valid disk_size_mb' \
  bash "$operation_script" publish "$workdir/missing-disk"
expect_failure 'template config has no valid disk_size_mb' \
  bash "$operation_script" publish "$workdir/invalid-disk"

write_catalog() {
  local standard_disk="$1"
  local large_disk="$2"
  jq -n --argjson standard_disk "$standard_disk" --argjson large_disk "$large_disk" '
    ["github-runner-ubuntu-slim", "github-runner-ubuntu-22-04",
     "github-runner-ubuntu-24-04", "github-runner-ubuntu-26-04",
     "github-runner-ubuntu-slim-large", "github-runner-ubuntu-22-04-large",
     "github-runner-ubuntu-24-04-large", "github-runner-ubuntu-26-04-large"] |
    map({names: [.], templateID: ., buildStatus: "ready", public: true,
         diskSizeMB: (if endswith("-large") then $large_disk else $standard_disk end)})
  ' >"$workdir/catalog.json"
}
export MOCK_CATALOG="$workdir/catalog.json"
export PATH="$workdir:$PATH"
write_catalog 22222 81920
expect_failure 'has disk size 22222 MiB; expected 20480 MiB' \
  bash "$repository_root/scripts/check-default-template-catalog.sh"
write_catalog 20480 22222
expect_failure 'has disk size 22222 MiB; expected 81920 MiB' \
  bash "$repository_root/scripts/check-default-template-catalog.sh"
write_catalog 20480 81920
bash "$repository_root/scripts/check-default-template-catalog.sh" >"$workdir/output"
test "$(wc -l <"$workdir/output" | tr -d '[:space:]')" = 8

echo 'standard/large template disk and qshell version gates passed'
