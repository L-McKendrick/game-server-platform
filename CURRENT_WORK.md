# Current Work

## State and Objective

Phase 16.5 setup polling and Workshop progress work is complete on
`codex/setup-polling-progress`, branched from `main`. Deployment is pending.

## Implemented Behavior

- The card displays one line: `Current download: Workshop item 450814997 (3/7)`.
  Missions use their own batch; client/server mods share a batch. Cached items
  retain their position but emit no download activity; retries keep the same
  position. Activity clears after successful SteamCMD return and stage changes.
- Bootstrap waits 120 seconds during explicitly observed Arma/Workshop install
  stages and 30 seconds otherwise. Missing/legacy/oversized snapshots fall back
  to 30 seconds. Existing deadline, terminal-result precedence, rollback,
  wake/restore, and standalone-sync orchestration remain intact.
- Provisioning uses three states per unsuccessful poll instead of five, plus
  removal of the initial counter state. Observers carry counters in execution
  input, preserving 15-second cadence, 40 failed observations per stage, final
  readiness precedence, and retry replay without per-poll database writes.
- Refresh retains persisted progress. A live overlay would bypass durable card
  revision/notification ordering and require additional integration. Progress,
  completion, and failure recognition may lag by roughly two minutes during
  installation; host execution continues immediately between stages.

## Validation

- Go 1.26.5: `go test -cover ./...`, `go vet ./...`, and `go build ./cmd/...` pass.
- Behavioral Git Bash tests cover latest-only snapshots, clearing, retries, and
  cached reuse. Native Bash syntax validation passes.
- Terraform 1.15.8 formatting/validation, affected Lambda packaging, and diff
  checks pass. No Terraform plan/apply or live session mutation was performed.
- No working C compiler is available locally; the required GitHub CI race job
  remains the race-test gate.

## Deployment Scope

The change adds no AWS resources, permissions, or Discord command definitions.
Expect updates to the provisioning/bootstrap state machines, bootstrap script
object/key, and affected worker packages/configuration. Shared bootstrap adapter
consumers are artifact, reliability, sleep/wake, and restore workers. Review
unrelated diffs and reject unrelated resource replacement/deletion.

## Commands to Apply Current Changes

Run from the repository root. Package only affected workers, create and review
this fresh saved plan, then apply that exact reviewed plan. Never reuse an older
plan or overwrite a user-owned plan. Discord registration is not required.

```powershell
$ErrorActionPreference = "Stop"
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
$env:GOTOOLCHAIN = "go1.26.5"
$env:GOCACHE = Join-Path (Get-Location) ".cache/go-build"
New-Item -ItemType Directory -Force .cache | Out-Null
go build -buildvcs=false -trimpath -o .cache/setup-package-lambda.exe ./cmd/package-lambda
if ($LASTEXITCODE -ne 0) { throw "Packager build failed" }
$previousSetupGOOS = $env:GOOS
$previousSetupGOARCH = $env:GOARCH
$previousSetupCGO = $env:CGO_ENABLED
try {
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    foreach ($component in @("provisioning-worker", "bootstrap-worker", "artifact-worker", "reliability-worker", "sleepwake-worker", "restore-worker")) {
        go build -buildvcs=false -tags lambda.norpc -trimpath -ldflags "-s -w" -o .cache/setup-bootstrap "./cmd/$component"
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $component" }
        & ./.cache/setup-package-lambda.exe -source .cache/setup-bootstrap -output "dist/$component.zip"
        if ($LASTEXITCODE -ne 0) { throw "Packaging failed: $component" }
    }
}
finally {
    $env:GOOS = $previousSetupGOOS
    $env:GOARCH = $previousSetupGOARCH
    $env:CGO_ENABLED = $previousSetupCGO
}
if (Test-Path -LiteralPath "infra/terraform/environments/dev/setup-polling-progress-20260905.tfplan") {
    throw "Plan already exists; choose a new descriptive filename for all three commands below."
}
terraform -chdir=infra/terraform/environments/dev plan -out=setup-polling-progress-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan failed" }
terraform -chdir=infra/terraform/environments/dev show setup-polling-progress-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan review failed" }
# Review the displayed plan before running the following apply command.
terraform -chdir=infra/terraform/environments/dev apply setup-polling-progress-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform apply failed" }
./scripts/verify-bootstrap-worker-deployment.ps1
```

After deployment, verify a normal modded setup: provisioning remains bounded,
Workshop cards show the current ID/position, installation observations use
120-second waits, and terminal completion clears download activity. Fast items
may be skipped between card updates; Refresh reads the latest persisted status.
