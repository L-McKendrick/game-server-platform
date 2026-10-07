# Current Work

## State and Objective

PR #33 is merged into main at a6697df; local main is synchronized. Exact-head
Ubuntu Go race/coverage/packaging and Terraform checks passed. Successful live
Workshop completion, matched timing and real Launcher import remain pending.

## Current Handoff

- PR #33 merged after all checks passed on 8ad769d. Commit 8ad769d includes the
  parsing/review fixes; 629a849 preserves the earlier main conflict resolution.
  Merge commit a6697df is on main. Deployment remains the separate plan below.

- Task 20.7.8: production shell functions pass simulated ANSI/CR output, a complete
  19-item batch in one SteamCMD invocation, partial transient retries, duplicate or
  misleading confirmations, missing items, zero-exit errors and cleanup checks.
- Table-driven shell expectations now independently assert exit status, invocation
  count and error category. Failed `&&` assertions could previously be masked.
- Share checked symlink traversal for cached/downloaded mods and missions; failed
  directory inspection cannot establish that content is safe. Failure regressions pass.
- Share rendering/checksum/artifact construction between uploaded and Workshop
  Launcher exports, preserving validation and original-preset integrity.
- All 81 packages checked sequentially: 80 pass; only the unchanged archive
  security-contract test fails its LF-only multiline assertion in this CRLF checkout.
  Affected package coverage: ssmbootstrap 82.0%, modlist 88.9%, artifacts 63.8%,
  workshop 46.5%, interactions 67.5%. Full vet and Bash syntax checks pass.
- The broad build hit low disk space. Every executable then built individually,
  temporary build artifacts were removed, and the artifact-worker Linux archive
  was rebuilt successfully. No local C compiler; Ubuntu CI race gate passed.
- Artifact ZIP SHA256: 671c8eb22d57465ac0b614467bea0d95b629ae5e9a57c5ae87820124b91d5b5d.
- test-workshop-3 failed when ANSI prefixes hid success/progress markers. The
  installed client's no-login probe confirms ANSI output; raw failed-run lines
  were scrubbed during credential cleanup. Formatting regressions now pass.
- test-workshop-3 is FAILED with no workflow lock and is terminable. Its original
  preset checksum is valid. No live setup retry, deletion or deployment performed.
- Details and deferred Creator DLC catalog refactor: docs/workshop-batching-review.md.

## Important User Attention

- Default: apply only a fresh reviewed plan. Expected changes are the new bootstrap
  S3 script object, six workers' script key/digest references and artifact-worker
  code. Stop on unrelated infrastructure or other Lambda package changes.
- File/directory existence alone does not prove completion. Preserve SteamCMD item
  confirmations alongside size, revision, symlink and applicable checksum checks.
- Old checksum-broken objects from tests 1/2 require new uploads. Test 3 can reuse
  its valid original preset. Live timing and Launcher acceptance remain 20.7.2 gates.
- Carry forward main's unverified recovery follow-up: inspect workflow
  `1551507903913918464` and quarantine its authorization-denied repair if it enters
  the command DLQ; do not redrive it.

## Commands to Apply Current Changes

The affected artifact-worker archive is already rebuilt. No further packaging or
Discord registration is required for this handoff. Deploy the script and artifact
worker with a new plan; the argument array preserves complete dotted targets in
PowerShell. From the repository root:

```powershell
$ErrorActionPreference = 'Stop'
$env:AWS_PROFILE = 'platform-admin'
$env:AWS_REGION = 'us-west-2'
$workshopReviewPlan = 'workshop-reviewed-output-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.tfplan'
$workshopPlanArguments = @(
    '-chdir=infra/terraform/environments/dev'
    'plan'
    "-out=$workshopReviewPlan"
    '-target=aws_s3_object.bootstrap_script'
    '-target=aws_lambda_function.bootstrap_worker'
    '-target=aws_lambda_function.artifact_worker'
    '-target=aws_lambda_function.sleepwake_worker'
    '-target=aws_lambda_function.archive_worker'
    '-target=aws_lambda_function.restore_worker'
    '-target=aws_lambda_function.reliability_worker'
)
terraform @workshopPlanArguments
if ($LASTEXITCODE -ne 0) { throw 'Terraform plan failed.' }
if (-not (Test-Path -LiteralPath (Join-Path 'infra/terraform/environments/dev' $workshopReviewPlan))) { throw 'Saved plan file is missing.' }
terraform -chdir=infra/terraform/environments/dev show $workshopReviewPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform plan review failed.' }

# Run only after reviewing and approving this exact newly saved plan.
terraform -chdir=infra/terraform/environments/dev apply $workshopReviewPlan
if ($LASTEXITCODE -ne 0) { throw 'Terraform apply failed.' }
foreach ($worker in @('bootstrap', 'artifact', 'sleepwake', 'archive', 'restore', 'reliability')) {
    ./scripts/verify-bootstrap-worker-deployment.ps1 -Worker $worker -Profile platform-admin -Region us-west-2
}
```

After deployment, retry test-workshop-3 through the authorized Discord `/rb start`
flow, or terminate it through `/rb terminate` and its normal confirmation flow.
Do not reuse failed SSM commands, old host-access manifests or saved Terraform plans.
Preserve user-owned plans, including the literal `$presetPlan` file.
