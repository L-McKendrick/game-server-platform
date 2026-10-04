# Current Work

## State and Objective

Branch `codex/workshop-download-batching` contains the committed Workshop batch
installer and the Launcher preset export correction. Neither change is deployed.

## Current Handoff

- Task 20.7.1 batches missing client/server mods into one SteamCMD session;
  missions have a separate session. Confirmed successes are omitted from bounded
  transient retries. Cache reuse, item checks, progress and cleanup are covered.
- Task 20.7.3 fixes exports missing `arma:Type=preset` and containing unclosed XML
  meta tags. Uploaded Steam Store DLC rows are retained and configured cDLCs are
  added once. Store app IDs remain separate from Workshop items and counts.
- Upload and Workshop-source exports share the corrected renderer. The supplied
  Launcher fixture round-trips all 19 mods and two cDLCs with strict XML parsing.
- Coverage tests for modlist/artifacts/Workshop/artifact-worker and vet pass;
  modlist coverage is 89.1%. Executable builds and Linux artifact-worker ZIP pass.
  Earlier batching shell tests, Terraform validation and tracked `.tf` formatting
  also passed. No compiler is available for local race tests; CI remains required.
- The earlier full-suite run exposed an unrelated unchanged archive contract test
  whose literal LF comparison rejects the CRLF checkout. Ignored local `.tfvars`
  formatting and deleted `infra/terraform/bootstrap/backend.tf` remain untouched.
- Local generated outputs: `dist/artifact-worker.zip` and a corrected reference
  export `dist/my-server-modlist.html`. Generated files are not committed.

## Important User Attention

- Default: no deployment until a fresh plan is reviewed and approved. Expected
  changes are the S3 bootstrap script, its six worker environment references,
  and artifact-worker code. Investigate any additional plan changes.
- Existing durable modlist objects are not migrated. After deployment, upload a
  new client preset or resolve its Workshop source to regenerate the attachment.
  Message repair alone reuses the old object. Pending revisions publish on activation.
- A real Launcher import and identical-content timing benchmark (20.7.2) remain
  pending. No measured speedup or live import acceptance is claimed.

## Commands to Apply Current Changes

The affected artifact-worker ZIP is already built locally; no repackaging or
Discord command registration is required for this handoff. Run from the repository
root with the user-approved `platform-admin` profile.

```powershell
$env:AWS_PROFILE = 'platform-admin'
$presetPlan = 'workshop-batching-launcher-presets-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.tfplan'
if (Test-Path -LiteralPath "infra/terraform/environments/dev/$presetPlan") { throw 'Choose a fresh plan filename.' }
terraform -chdir=infra/terraform/environments/dev plan -out=$presetPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform plan failed.' }
terraform -chdir=infra/terraform/environments/dev show -no-color $presetPlan

# Only after reviewing and approving that exact saved plan:
terraform -chdir=infra/terraform/environments/dev apply $presetPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform apply failed.' }

$workers = 'artifact', 'bootstrap', 'sleepwake', 'archive', 'restore', 'reliability'
foreach ($worker in $workers) {
    aws lambda wait function-updated-v2 --function-name "game-server-platform-dev-$worker-worker" --profile platform-admin --region us-west-2
    if ($LASTEXITCODE -ne 0) { throw "Worker update failed: $worker" }
    aws lambda get-function-configuration --function-name "game-server-platform-dev-$worker-worker" --profile platform-admin --region us-west-2 --query '{State:State,Update:LastUpdateStatus,Key:Environment.Variables.BOOTSTRAP_SCRIPT_KEY,SHA256:Environment.Variables.BOOTSTRAP_SCRIPT_SHA256}' --output json
    if ($LASTEXITCODE -ne 0) { throw "Worker verification failed: $worker" }
}
./scripts/verify-bootstrap-worker-deployment.ps1 -Worker artifact -Profile platform-admin -Region us-west-2
$assetsBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$scriptKey = aws lambda get-function-configuration --function-name game-server-platform-dev-bootstrap-worker --profile platform-admin --region us-west-2 --query Environment.Variables.BOOTSTRAP_SCRIPT_KEY --output text
aws s3api head-object --bucket $assetsBucket --key $scriptKey --profile platform-admin --region us-west-2 --query '{Bytes:ContentLength,Type:ContentType,Version:VersionId}' --output json
if ($LASTEXITCODE -ne 0) { throw 'Bootstrap script verification failed.' }
```

Check all six workers reference the reviewed script key/digest. Regenerate a
preset through a new upload/source resolution, import it into Arma Launcher,
then benchmark batching on a new approved bootstrap/sync operation.
