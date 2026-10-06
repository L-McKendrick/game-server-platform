# Current Work

## State and Objective

Reconcile `codex/workshop-download-batching` with `origin/main` at `b5e386f`
and review the combined Workshop batching and Launcher preset changes.
Batching and Launcher fixes remain undeployed.

## Current Handoff

- Preserve main's original client-preset bytes, digest-addressed key and checksum;
  generate the public Launcher modlist independently with uploaded/configured cDLCs.
  The merge regression covers both contracts and idempotent replay.
- Preserve main's creation continuation fix; its handler deployment was still
  pending in main's handoff. Package that handler together with artifact-worker.
- Permanent Workshop visibility/removal failures now precede transient matching,
  avoiding retries caused by ordinary connection text in a batch log.
- Correct the shell regression's concatenated redaction/cleanup assertions and
  make negative retry/redaction/sampler checks explicitly fail the harness.
- Review details and optional refactors: `docs/workshop-batching-review.md`.
- Bootstrap now extracts only typed ModContainer rows from original client/server
  inputs, matching ingestion and excluding DLC/footer IDs. Mixed-row, DLC-only,
  deduplication and unreadable-file regressions exercise the real extractor.
- Affected coverage tests, vet, executable builds and Bash syntax checks pass.
  The full suite fails the unchanged archive contract's LF-only comparison in this
  CRLF checkout. Its sampler timing threshold also failed under concurrent load;
  three isolated repetitions passed. No local C compiler; CI race gate remains.

## Important User Attention

- Default: no deployment until a fresh plan is reviewed and approved. Reconcile
  ignored development tfvars with live values before an untargeted plan; main
  reports Discord, provisioning and capacity drift. Stop on unrelated plan changes.
- Expected rollout: bootstrap S3 object, six worker script environment references,
  artifact-worker code and the pending Discord handler code. No registration needed.
- Existing durable attachments require a new upload/source resolution; message
  repair alone reuses old bytes. Actual Launcher import and matched timing remain
  task 20.7.2/live acceptance gates.
- Carry forward main's unverified recovery follow-up: inspect workflow
  `1551507903913918464` and quarantine its authorization-denied repair if it enters
  the command DLQ; do not redrive it.

## Commands to Apply Current Changes

Run from the repository root. Rebuild the two affected archives after reconciliation;
older local archives are stale. Preserve old plan files. Review a fresh plan and
apply only the exact approved plan, after resolving the configuration drift above.

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGOEnabled = $env:CGO_ENABLED
try {
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    foreach ($entry in @(@{ Command = './cmd/artifact-worker'; Archive = 'artifact-worker.zip' }, @{ Command = './cmd/discord-lambda'; Archive = 'discord-interactions.zip' })) {
        go build -buildvcs=false -tags lambda.norpc -trimpath -ldflags '-s -w' -o .cache/reconciled-bootstrap $entry.Command
        if ($LASTEXITCODE -ne 0) { throw 'Lambda build failed.' }
        # Go's packager runs on the host, so temporarily restore its target.
        $env:GOOS = $previousGOOS
        $env:GOARCH = $previousGOARCH
        $env:CGO_ENABLED = $previousCGOEnabled
        go run ./cmd/package-lambda -source .cache/reconciled-bootstrap -output "dist/$($entry.Archive)"
        if ($LASTEXITCODE -ne 0) { throw 'Lambda packaging failed.' }
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        $env:CGO_ENABLED = '0'
    }
} finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGOEnabled
}
$env:AWS_PROFILE = 'platform-admin'
$presetPlan = 'reconciled-workshop-launcher-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.tfplan'
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
aws lambda wait function-updated-v2 --function-name game-server-platform-dev-discord-interactions --profile platform-admin --region us-west-2
if ($LASTEXITCODE -ne 0) { throw 'Discord handler update failed.' }
./scripts/verify-bootstrap-worker-deployment.ps1 -FunctionName game-server-platform-dev-discord-interactions -ArchivePath dist/discord-interactions.zip -Profile platform-admin -Region us-west-2
$assetsBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$scriptKey = aws lambda get-function-configuration --function-name game-server-platform-dev-bootstrap-worker --profile platform-admin --region us-west-2 --query Environment.Variables.BOOTSTRAP_SCRIPT_KEY --output text
aws s3api head-object --bucket $assetsBucket --key $scriptKey --profile platform-admin --region us-west-2 --query '{Bytes:ContentLength,Type:ContentType,Version:VersionId}' --output json
if ($LASTEXITCODE -ne 0) { throw 'Bootstrap script verification failed.' }
```

Check all six workers reference the reviewed script key/digest. Regenerate a
preset through a new upload/source resolution, import it into Arma Launcher,
then benchmark batching on a new approved bootstrap/sync operation. Verify the
creation continuation still opens mod options after asynchronous draft updates.
