# Current Work

## State and Objective

Workshop batching is implemented on `codex/workshop-download-batching` for task
20.7.1. AWS development remains in `us-west-2`; this work is not deployed.

## Current Handoff

- Missing client/server mods share one SteamCMD session; missing missions use
  a separate batch session. Cached revision content skips downloads.
- Exact requested-item success confirmations gate batch completion. Transient
  failures retry only unconfirmed IDs, up to three attempts; Guard and permanent
  failures remain terminal. Item positions and private-output cleanup are retained.
- Existing per-item metadata, size, checksum, symlink and publication checks
  remain after download. Copy/marker failures cannot report a completed mod.
- Reviewed executable shell tests cover batching, duplicate/cache handling,
  transient/terminal failures, unconfirmed success, auth failure, mission replay,
  size drift, unsafe payloads, and sampler/temporary-file cleanup.
- Go 1.26.5 affected-package coverage tests, vet/build, Bash syntax and Terraform
  1.15.x validation pass. Tracked Terraform `.tf` formatting passes.
- Full `go test -cover ./...` has one unrelated unchanged archive contract test
  failure: its literal LF comparison rejects the CRLF checkout of `phase9.tf`.
  LF-normalized content matches. Ignored `terraform.tfvars` also fails formatting;
  left untouched. No working C compiler; the CI race gate remains required.
- Deployment changes the content-addressed S3 script and six worker environment
  script references. Lambda executable code and Discord commands are unchanged;
  no Lambda packaging or Discord registration is required.
- The pre-existing deletion of `infra/terraform/bootstrap/backend.tf` is untouched.

## Important User Attention

- Review and approve a fresh Terraform plan before applying. Default: no deployment.
- Task 20.7.2 remains pending: benchmark identical Workshop content after deployment.
  No measured speedup or live Steam output-format acceptance is claimed yet.
- Existing executions retain their dispatched script; assess batching on a new
  bootstrap/sync operation. Game and Workshop installation remain sequential.

## Commands to Apply Current Changes

Run from the repository root. Authentication uses the user-approved `platform-admin`
profile. No packaging or Discord command registration is needed.

```powershell
$env:AWS_PROFILE = 'platform-admin'
$batchPlan = 'workshop-batching-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.tfplan'
if (Test-Path -LiteralPath "infra/terraform/environments/dev/$batchPlan") { throw 'Plan already exists; choose a fresh filename.' }
terraform -chdir=infra/terraform/environments/dev plan -out=$batchPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform plan failed.' }
terraform -chdir=infra/terraform/environments/dev show -no-color $batchPlan

# Only after reviewing and approving that exact saved plan:
terraform -chdir=infra/terraform/environments/dev apply $batchPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform apply failed.' }

$workers = 'artifact', 'bootstrap', 'sleepwake', 'archive', 'restore', 'reliability'
foreach ($worker in $workers) {
    aws lambda wait function-updated-v2 --function-name "game-server-platform-dev-$worker-worker" --profile platform-admin --region us-west-2
    if ($LASTEXITCODE -ne 0) { throw "Worker update failed: $worker" }
    aws lambda get-function-configuration --function-name "game-server-platform-dev-$worker-worker" --profile platform-admin --region us-west-2 --query '{State:State,Update:LastUpdateStatus,Key:Environment.Variables.BOOTSTRAP_SCRIPT_KEY,SHA256:Environment.Variables.BOOTSTRAP_SCRIPT_SHA256}' --output json
    if ($LASTEXITCODE -ne 0) { throw "Worker verification failed: $worker" }
}
$assetsBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$scriptKey = aws lambda get-function-configuration --function-name game-server-platform-dev-bootstrap-worker --profile platform-admin --region us-west-2 --query Environment.Variables.BOOTSTRAP_SCRIPT_KEY --output text
aws s3api head-object --bucket $assetsBucket --key $scriptKey --profile platform-admin --region us-west-2 --query '{Bytes:ContentLength,Type:ContentType,Version:VersionId}' --output json
if ($LASTEXITCODE -ne 0) { throw 'Bootstrap script verification failed.' }
```

Verify all six workers reference the reviewed script key/digest. Then use a new,
approved modded-session operation to verify real Steam confirmations, current-item
progress, cached replay, and download timing before claiming live acceptance.
