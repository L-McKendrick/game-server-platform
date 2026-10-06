# Current Work

## State and Objective

Creation mod-options continuation is fixed in source on the current beta-fix
branch. The earlier client-preset integrity fix is already deployed; the
Discord interaction handler change still needs deployment.

## Current Handoff

- The creation button previously required an exact session version. Asynchronous
  mission validation and card metadata changes could invalidate it immediately.
- The button now opens the latest owner-authorized modded draft, rejects future
  versions, other guilds, non-drafts, vanilla sessions and active workflows,
  and binds the modal to the current version. Submission concurrency checks remain.
- Continuation parsing now requires its own prefix so unrelated controls cannot
  be mistaken for creation buttons.
- Regression coverage checks older-button recovery/current modal version, future
  versions, other owners and other guilds. Interaction package tests and vet,
  Discord handler build, and diff whitespace checks pass with Go 1.26.5.
- No infrastructure definitions or Discord command definitions changed.

## Important User Attention

- Reconcile ignored development `.tfvars` with live values before any untargeted
  deployment; the previous handoff recorded Discord, provisioning and capacity drift.
- Prior recovery follow-up remains unverified: check workflow
  `1551507903913918464` to completion and inspect/quarantine the authorization-denied
  repair command if it reaches the command DLQ. Do not redrive it.

## Commands to Apply Current Changes

Run from the repository root. Package only the changed handler, create a fresh
saved targeted plan because of the recorded local configuration drift, and review
it. Proceed with apply only after approving a plan that changes only the handler's
code; stop if it changes environment, permissions or other configuration.

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGOEnabled = $env:CGO_ENABLED
try {
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    go build -buildvcs=false -tags lambda.norpc -trimpath -ldflags '-s -w' -o .cache/mod-options-bootstrap ./cmd/discord-lambda
    if ($LASTEXITCODE -ne 0) { throw 'Handler build failed' }
} finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGOEnabled
}
go run ./cmd/package-lambda -source .cache/mod-options-bootstrap -output dist/discord-interactions.zip
if ($LASTEXITCODE -ne 0) { throw 'Handler packaging failed' }
$env:AWS_PROFILE = 'game-server-dev'
$modOptionsPlan = 'creation-mod-options-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.tfplan'
terraform -chdir=infra/terraform/environments/dev plan -target=aws_lambda_function.discord_interactions "-out=$modOptionsPlan"
if ($LASTEXITCODE -ne 0) { throw 'Terraform plan failed' }
terraform -chdir=infra/terraform/environments/dev show $modOptionsPlan
```

After reviewing and approving that exact saved plan:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $modOptionsPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform apply failed' }
```

Verify through Discord: create a modded draft with a mission, wait for validation
and card updates, then click `Continue to mod options`. It should open the mod
options form without `/rb edit`. Submit options and verify normal validation.
No Discord command registration is required. Preserve older saved plan files.
