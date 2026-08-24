# Current Work

## State and Objective

Phases 1-10 and 12-14 are complete. Phase 18 is the approved out-of-order
miscellaneous-fixes and quality-of-life phase on `codex/misc-fixes`; its
mission-menu, lifecycle-visibility, and server-only-mod steps are complete.

## Completed Development

- `/rb create` mod options and `/rb edit session:<slug> section:mods` accept a
  separate private Arma Launcher preset for server-only Workshop mods, with the
  existing owner, guild, version, upload, validation, and idempotency controls.
- Server-only preset inputs use independent active and pending revisions. They
  are persisted backward-compatibly and preserved through start, wake,
  archive, restore, replacement-host bootstrap, rollback, and termination.
- The existing authenticated SteamCMD stage installs bounded validated
  server-only Workshop items and writes a deterministic `-serverMod=` launch
  argument. Items duplicated in the client preset remain client mods only.
- Server-only preset contents and names are excluded from the public card and
  downloadable client modlist. Private `/rb status` exposes only safe
  validation and revision state.
- Modded sessions may use client mods, Creator DLC, server-only mods, or a
  combination; vanilla sessions continue to skip every mod path.
- Phase 18.2 lifecycle progress and projected inactivity timing remain
  complete, including the CI-owned Windows CGO race-check guidance in
  `AGENTS.md`.

## Validation

- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass with workspace-local caches.
- Focused coverage passes for server-mod validation, creation/readiness,
  private rendering, revision replay, persistence, lifecycle promotion,
  rollback, archive/restore, termination, and bootstrap selection.
- All 13 Lambda archives package successfully; the Arma bootstrap script passes
  Git Bash syntax validation.
- `terraform fmt -check -recursive infra/terraform`, development-environment
  `terraform validate`, and `git diff --check` pass.
- `go test -race -coverprofile=coverage.out ./...` remains delegated to the
  required GitHub CI job because this Windows host has no CGO C compiler.

## Next Development Task

- Phase 18.4 is next: deploy newly accepted mission uploads to compatible
  running servers through the existing artifact worker and bounded SSM copy,
  without restarting Arma or adding a synchronization worker.
- Phase 15 remains next in the primary delivery order after this approved
  out-of-order branch.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out misc-fixes-server-mods.tfplan
terraform -chdir=infra/terraform/environments/dev show misc-fixes-server-mods.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply misc-fixes-server-mods.tfplan

$BootstrapBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$BootstrapContent = (Get-Content -Raw "deploy/bootstrap/arma3-bootstrap.sh").Replace("`r`n", "`n")
$BootstrapHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($BootstrapContent))).ToLowerInvariant()
$BootstrapKey = "platform/bootstrap/arma3-$($BootstrapHash.Substring(0, 16)).sh"
aws s3api head-object --bucket $BootstrapBucket --key $BootstrapKey
```

No Discord command registration is required because the command definition did
not change. After deployment, create or edit a modded development session with
a small server-only preset, verify private status reaches accepted/staged, then
start or wake it and confirm the Arma service is healthy with the expected
`-serverMod=` argument while the public card and client modlist omit those
items.
