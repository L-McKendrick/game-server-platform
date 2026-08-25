# Current Work

## State and Objective

Phase 18 and its follow-up live-progress repair are complete on `codex/misc-fixes`. The branch
contains four lightweight Discord/Arma quality-of-life
changes and no new worker, queue, state machine, or persistent service. Phase
15 remains next in the primary delivery order after this branch is merged.

## Completed Development

- Mission-manager controls are grouped per file with `Add mission` at the
  bottom while retaining private pagination and stale-component safeguards.
- Bootstrap progress distinguishes game-file and Workshop installation, and
  exposes only bounded allowlisted SteamCMD activity. A workflow-scoped
  snapshot in the existing encrypted session-assets bucket makes those updates
  visible during the running SSM command instead of waiting for buffered
  command output. Private `/rb status`
  projects automatic sleep/archive times from authoritative lifecycle evidence.
- The public card removes completed progress once a session is running and
  boxes the current mission/map while leaving player count and native start
  timing immediately below it. Transitional cards and private `/rb status`
  retain progress detail.
- Creation and `/rb edit ... section:mods` accept a separate private
  server-only preset with independent active/pending revisions. Bootstrap
  installs it through the existing authenticated SteamCMD flow and generates a
  deterministic `-serverMod=` argument without exposing it on the public card
  or client modlist.
- Accepted mission uploads are copied checksum-first and atomically to a stable
  running/idle managed host without restarting Arma or changing its current
  mission. Every active accepted mission is also synchronized on start, wake,
  restore, and replacement-host bootstrap.
- Final review allows an established cDLC/server-only session to stage its first
  client preset at base revision zero and makes interrupted live-mission replay
  select the exact digest-plus-normalized-filename object.
- Terraform routes the existing account budget through AWS's documented
  `budgets.us-east-1.api.aws` endpoint using an isolated `us-east-1` billing
  provider. This avoids operator-network timeouts to the legacy global Budgets
  endpoint without changing budget policy or regional workload resources.
- `AGENTS.md` documents that the required GitHub CI job owns race testing when
  the Windows development host has no CGO C compiler.

## Review and Validation

- Final review covers authorization and stale modal state, independent revision
  promotion/rollback, archive/restore and DynamoDB compatibility, private
  server-preset boundaries, progress redaction, mission replay/idempotency,
  checksum placement, current-mission preservation, bootstrap replay, and
  least-privilege tagged-instance SSM access.
- Live-progress review covers workflow-scoped object keys, bounded reads,
  allowlisted parsing, missing-snapshot fallback, and least-privilege S3 access.
- Public-card rendering coverage verifies running cards omit progress, setup
  cards retain it, and mission boxing does not break native timestamps.
- `go test ./...`, `go test -cover ./...`, `go vet ./...`, `go build ./cmd/...`,
  all 13 Lambda packages, Git Bash syntax, Terraform format/validation, and
  `git diff --check` pass after the final review fixes.
- A complete saved Terraform plan succeeds through the alternate Budgets
  endpoint. Its actionable resources are the expected Phase 18 Lambda/IAM
  updates and content-addressed bootstrap replacement; the budget has no
  planned change.
- `go test -race -coverprofile=coverage.out ./...` remains delegated to required
  GitHub CI because this Windows host has no CGO C compiler.

## Next Development Task

- Open the Phase 18 pull request and require CI before merge.
- After merge, begin Phase 15.1 on a new phase branch.

## Terraform Budgets Endpoint Note

A trace of the apparent `aws_lambda_event_source_mapping.command_worker` stall
showed both mapping and tag reads completing normally. The actual blocked call
was `Budgets/DescribeBudget`: this operator network could not establish TCP 443
to `budgets.amazonaws.com`. AWS's documented alternate endpoint
`budgets.us-east-1.api.aws` is reachable and is now configured only for the
budget resource. Do not work around refresh failures with `-refresh=false`,
`-target`, `-lock=false`, state removal, or mapping deletion. After interrupting
a stuck plan, verify no Terraform process remains and remove only the exact
stale lock reported by the next command before creating a fresh plan.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
$AwsIdentity = aws sts get-caller-identity | ConvertFrom-Json
$AwsIdentity
aws budgets describe-budget --region us-east-1 --endpoint-url https://budgets.us-east-1.api.aws --account-id $AwsIdentity.Account --budget-name game-server-platform-dev-monthly-cost

# Phase 18 changes the Discord, artifact, bootstrap, archive/restore, monitor,
# and shared lifecycle code. Package the complete Lambda set once.
./scripts/package-discord-lambda.ps1

# Always create, review, and apply one fresh plan. The timestamp avoids
# overwriting user-owned or interrupted saved plans.
$PlanFile = "misc-fixes-phase18-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile

# Verify Terraform published the content-addressed bootstrap revision used by
# provisioning, wake, restore, and replacement-host workflows.
$BootstrapBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$BootstrapContent = (Get-Content -Raw "deploy/bootstrap/arma3-bootstrap.sh").Replace("`r`n", "`n")
$BootstrapHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($BootstrapContent))).ToLowerInvariant()
$BootstrapKey = "platform/bootstrap/arma3-$($BootstrapHash.Substring(0, 16)).sh"
aws s3api head-object --bucket $BootstrapBucket --key $BootstrapKey

# During a new setup, verify the workflow-scoped live snapshot exists. Obtain
# the current workflow ID from the private `/rb status` response or metadata.
# aws s3 cp "s3://$BootstrapBucket/sessions/<session-id>/runtime/bootstrap-progress-<workflow-id>.txt" -
```

No Discord command registration is required because the `/rb` command schema
did not change. After deployment, verify all Phase 18 behavior in development:

1. Open `/rb edit ... mission-files`; confirm each filename shares a row with
   its controls and `Add mission` is last.
2. Start a small session; confirm the card advances through game-file and
   Workshop stages with only safe current-download text, and confirm private
   `/rb status` shows applicable automatic sleep/archive projections.
3. Create or edit a modded session with a small server-only preset; confirm
   private validation/revision status, no public-card or client-modlist leak,
   healthy bootstrap, and the expected `-serverMod=` launch argument.
4. Upload a small mission while Arma is running; confirm it appears in
   `/srv/game-server/arma3/mpmissions` with `steam:steam` ownership and that the
   Arma service PID/current mission do not change. Start or wake with multiple
   accepted missions and confirm the complete active set is synchronized.
