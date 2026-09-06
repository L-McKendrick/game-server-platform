# Current Work

## State and Objective

Branch review for `codex/setup-polling-progress` is complete against `main`.
The branch reduces setup polling transitions and improves download/status
presentation. No PR has been opened, and this review performs no deployment.
See `docs/setup-polling-review.md` for findings, validation, and proposed PR text.

## Review Findings and Changes

- Fixed sampler shutdown during an in-flight S3 upload. Both upload and sleep
  are interruptible; telemetry children release inherited host/bootstrap locks.
- Added slow-upload and Steam reauthorization failure regression coverage while
  preserving success/transient-failure exit codes and private-output redaction.
- Reviewed provisioning counters/replay, readiness precedence, bootstrap deadline
  and terminal precedence, snapshot bounds/fallback, cached/retried Workshop
  positions, safe links, activity clearing, and card revision/rate limits.
- Workshop percentage is not implemented: no reliable per-item percentage signal
  was verified in the current command output. Arma percentages use latest-only
  local sampling; normal visibility lag can reach roughly 150 seconds plus
  delivery overhead, and unavailable snapshots can remain stale longer.

## Validation and Remaining Attention

Go 1.26.5 coverage tests, vet, all command builds, Bash behavior/syntax,
Terraform 1.15.8 formatting/validation, and Lambda packaging all pass.
No local C compiler is available; race testing remains the required CI gate.

The pre-existing test-44 restore failure is still unresolved: replacement-host
AWS CLI prerequisites and restore failure finalization need Phase 16.7. Test-44
was reconciled to FAILED with its workflow lock cleared. Default: do not attempt
another live restore as part of this branch review. This branch does not claim
restore release readiness.

Branch deployment scope: provisioning/bootstrap state-machine definitions,
bootstrap script object/key, and all shared-renderer Lambda consumers in the
standard packaging script. No new AWS resources, IAM grants, migrations, or
Discord command definitions. Review unrelated Terraform differences separately.

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
if (Test-Path -LiteralPath "infra/terraform/environments/dev/setup-polling-review-20260906.tfplan") {
    throw "Plan already exists; choose a new descriptive filename in all commands below."
}
terraform -chdir=infra/terraform/environments/dev plan -out setup-polling-review-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan failed" }
terraform -chdir=infra/terraform/environments/dev show setup-polling-review-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform plan review failed" }
# Review the displayed plan before running the following apply command.
terraform -chdir=infra/terraform/environments/dev apply setup-polling-review-20260906.tfplan
if ($LASTEXITCODE -ne 0) { throw "Terraform apply failed" }
./scripts/verify-bootstrap-worker-deployment.ps1
```

Verify a fresh setup shows Arma percentages, then the Workshop count in the stage
and linked ID on the download line; verify a blank line before Started and no
Active condition. Existing in-flight bootstrap commands keep their already
loaded script. Check `/rb status` source links and Refresh feedback. Discord
registration is not required.
