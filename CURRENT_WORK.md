# Current Work

## State and Objective

Phases 1-10, 12-14, and the approved out-of-order Phase 18 are complete.
`codex/misc-fixes` contains the Discord mission layout, lifecycle-progress,
server-only-mod, and live mission-upload improvements. Phase 15 is next in the
primary delivery order.

## Completed Development

- Newly accepted Arma 3 mission uploads are persisted first, then copied to the
  exact managed host only while the session is stably `RUNNING` or `IDLE` with
  no active mutating workflow.
- The artifact worker reuses bounded SSM access to download the exact
  content-addressed S3 object into `arma3/mpmissions`, verify SHA-256, apply
  `steam:steam` ownership, and atomically rename it. It never restarts Arma or
  rewrites the current mission selection.
- Sleeping, archived, changing, destructive, and instance-less sessions skip
  live copy. Accepted uploads remain available for the next compatible
  bootstrap rather than requiring a synchronization service.
- Start, wake, restore, and replacement-host bootstrap synchronize every active
  accepted mission from a checksum-bound manifest while preserving the
  separately snapshotted current mission as launch authority. Legacy
  single-mission sessions retain their existing fallback.
- Artifact replay retries an incomplete live copy idempotently; IAM permits the
  artifact worker to use `AWS-RunShellScript` only against project/environment
  tagged instances and observe its command.

## Validation

- Focused domain, artifact-service, live-SSM-copy, bootstrap-runner, and worker
  tests pass, covering acceptance replay, exact-instance selection, lifecycle
  and workflow conflicts, checksum/atomic placement, no restart, all-active
  bootstrap synchronization, and current-mission preservation.
- `go test ./...` passes with workspace-local Go caches.
- `go test -cover ./...`, `go vet ./...`, `go build ./cmd/...`, all 13 Lambda
  packages, Git Bash syntax validation, Terraform format/validation, and
  `git diff --check` pass.
- The race/coverage check remains owned by required GitHub CI on this Windows
  host because no CGO C compiler is installed, as documented in `AGENTS.md`.

## Next Development Task

- Phase 15.1: add maximum session-duration guardrails according to the project
  plan. Start it on a new phase branch.
- Because Phase 18 is complete, merge `codex/misc-fixes` only through a reviewed
  pull request after CI passes.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out misc-fixes-live-missions.tfplan
terraform -chdir=infra/terraform/environments/dev show misc-fixes-live-missions.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply misc-fixes-live-missions.tfplan

$BootstrapBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$BootstrapContent = (Get-Content -Raw "deploy/bootstrap/arma3-bootstrap.sh").Replace("`r`n", "`n")
$BootstrapHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($BootstrapContent))).ToLowerInvariant()
$BootstrapKey = "platform/bootstrap/arma3-$($BootstrapHash.Substring(0, 16)).sh"
aws s3api head-object --bucket $BootstrapBucket --key $BootstrapKey
```

No Discord command registration is required. After deployment, upload a small
mission to a running development session and verify it appears in
`/srv/game-server/arma3/mpmissions` without the Arma service PID changing. Then
start or wake a session with multiple accepted missions and verify the complete
active set is present while the configured/current mission remains unchanged.
