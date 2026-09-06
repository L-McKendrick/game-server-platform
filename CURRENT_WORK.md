# Current Work

## State and Objective

Phase 16.8 public setup-card formatting is implemented on
`codex/setup-polling-progress`; deployment is pending. Prior polling and Arma
percentage behavior remains unchanged.
Test-44 later exposed a missing AWS CLI on the replacement host and an unsafe
restore result choice that bypassed failure finalization. The live session was
reconciled to `FAILED` with its workflow lock cleared. Phase 16.7 is now the next
planned repair step; it has not been implemented.

## Implemented Behavior

- Public setup progress hides the redundant Active condition and separates
  Started with a blank line. Other conditions (such as Retrying) remain visible.
- Workshop stage reads `Downloading and installing workshop files (x of y)`;
  Current download contains the linked numeric ID. Missing batch evidence omits
  the count. Workshop percentages are not fabricated: the verified SteamCMD
  Workshop output lacks the Arma-style download percentage signal. A separate
  measured-progress source remains to be investigated if requested.
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

Go 1.26.5 full coverage tests, vet, builds, and affected Lambda packaging pass.
No Terraform or bootstrap script changes were needed in this follow-up. No local C compiler is available;
race testing remains the CI gate. All affected Lambda packages build successfully.
No deployment or Discord command registration performed.
Shared card rendering affects all Lambda packages in the standard packaging
script. This follow-up changes presentation only. No new AWS
resources, permissions, state-machine changes, or command definitions required.
Review any unrelated Terraform differences separately.
The Phase 16.7 roadmap-only addition requires no deployment or Discord command
registration.

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
if (Test-Path -LiteralPath "infra/terraform/environments/dev/setup-card-layout-20260906.tfplan") {
    throw "Plan already exists; choose a new descriptive filename in all commands below."
}
terraform -chdir=infra/terraform/environments/dev plan -out setup-card-layout-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan failed" }
terraform -chdir=infra/terraform/environments/dev show setup-card-layout-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan review failed" }
# Review the displayed plan before running the following apply command.
terraform -chdir=infra/terraform/environments/dev apply setup-card-layout-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform apply failed" }
./scripts/verify-bootstrap-worker-deployment.ps1
```

Verify a fresh setup shows Arma percentages, then the Workshop count in the stage
and linked ID on the download line; verify a blank line before Started and no
Active condition. Existing in-flight bootstrap commands keep their already
loaded script. Check `/rb status` source links and Refresh feedback. Discord
registration is not required.
