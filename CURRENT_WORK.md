# Current Work

## State and Objective

Phase 18 and its final review are complete on `codex/misc-fixes`. The branch
contains four lightweight Discord/Arma quality-of-life
changes and no new worker, queue, state machine, or persistent service. Phase
15 remains next in the primary delivery order after this branch is merged.

## Completed Development

- Mission-manager controls are grouped per file with `Add mission` at the
  bottom while retaining private pagination and stale-component safeguards.
- Bootstrap progress distinguishes game-file and Workshop installation, and
  exposes only bounded allowlisted SteamCMD activity. Private `/rb status`
  projects automatic sleep/archive times from authoritative lifecycle evidence.
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
- `AGENTS.md` documents that the required GitHub CI job owns race testing when
  the Windows development host has no CGO C compiler.

## Review and Validation

- Final review covers authorization and stale modal state, independent revision
  promotion/rollback, archive/restore and DynamoDB compatibility, private
  server-preset boundaries, progress redaction, mission replay/idempotency,
  checksum placement, current-mission preservation, bootstrap replay, and
  least-privilege tagged-instance SSM access.
- `go test ./...`, `go test -cover ./...`, `go vet ./...`, `go build ./cmd/...`,
  all 13 Lambda packages, Git Bash syntax, Terraform format/validation, and
  `git diff --check` pass after the final review fixes.
- `go test -race -coverprofile=coverage.out ./...` remains delegated to required
  GitHub CI because this Windows host has no CGO C compiler.

## Next Development Task

- Commit and push the Phase 18.5 review/handoff changes, then open the Phase 18
  pull request and require CI before merge.
- After merge, begin Phase 15.1 on a new phase branch.

## Terraform Refresh Note

Terraform may pause while refreshing an unchanged Lambda event-source mapping,
including `aws_lambda_event_source_mapping.command_worker`. If the same refresh
line makes no progress for several minutes, cancel once with `Ctrl+C`, confirm
the AWS identity/region, inspect the reported mapping with
`aws lambda get-event-source-mapping --uuid <reported-id>`, and rerun a new plan
with a new filename. Do not reuse or apply a plan from an interrupted run.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

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
