# Current Work

## State and Objective

Phase 16.6 download/status polish is complete on `codex/setup-polling-progress`.
Deployment of this follow-up is pending. Phase 16.5 was observed live on test-44:
provisioning finished in 33.35 seconds; installation waits used 120 seconds.

## Implemented Behavior

- Workshop activity renders `Current download: <linked ID> (Item x/y)`.
- Arma activity shows the latest observed whole download percentage. One local
  sampler reads a bounded SteamCMD log tail every 30 seconds and publishes only
  changed values. Existing two-minute installation observations remain intact.
  Missing/verification output uses the generic label; sampler cleanup precedes
  activity clearing. No speed estimates, history, or extra AWS polling states.
- Private status groups progress, connection, content, players, and diagnostics;
  active/pending Workshop mod sources are linked. Irrelevant optional settings
  are omitted, and failure guidance precedes content detail.
- Refresh feedback is concise; persisted status and revision/rate limits remain.

## Validation and Deployment Scope

Go 1.26.5 full coverage tests, vet, builds, native Bash behavior/syntax checks,
and Terraform 1.15.8 formatting/validation pass. No local C compiler is available;
race testing remains the CI gate. All affected Lambda packages build successfully.
No deployment or Discord command registration performed.
Shared card rendering affects all Lambda packages in the standard packaging
script. The bootstrap script and shared SSM adapter also changed. No new AWS
resources, permissions, state-machine changes, or command definitions required.
Review any unrelated Terraform differences separately.

## Commands to Apply Current Changes

Run from the repository root. The earlier setup-polling plan is not reused.

```powershell
$ErrorActionPreference = "Stop"
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
$env:GOTOOLCHAIN = "go1.26.5"
$env:GOCACHE = Join-Path (Get-Location) ".cache/go-build"
./scripts/package-discord-lambda.ps1
if ($LASTEXITCODE -ne 0) { throw "Lambda packaging failed" }
if (Test-Path -LiteralPath "infra/terraform/environments/dev/download-status-qol-20260905.tfplan") {
    throw "Plan already exists; choose a new descriptive filename in all commands below."
}
terraform -chdir=infra/terraform/environments/dev plan -out download-status-qol-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan failed" }
terraform -chdir=infra/terraform/environments/dev show download-status-qol-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan review failed" }
# Review the displayed plan before running the following apply command.
terraform -chdir=infra/terraform/environments/dev apply download-status-qol-20260905.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform apply failed" }
./scripts/verify-bootstrap-worker-deployment.ps1
```

Verify a fresh setup shows Arma percentages, then linked Workshop IDs, then clears
activity on completion. Existing in-flight bootstrap commands keep their already
loaded script. Check `/rb status` source links and Refresh feedback. Discord
registration is not required.
