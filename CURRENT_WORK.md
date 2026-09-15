# Current Work

## State and Objective

Phase 19.2 is complete locally on `codex/phase-19-production-guardrails` and is
not deployed. Phase 19.3 managed-host S3 isolation remains required before
production or multi-tenant use. Do not claim a guaranteed AWS spending cap.

## Current Handoff

- Guild-scoped defaults persist separately and fall back to 30 minutes without
  players before sleep and 7 days sleeping before archive. New sessions snapshot
  the current defaults; legacy rows safely receive the same fallback values.
- `/rb admin` → **Session timeouts** lets Discord Administrators replace both
  future defaults or add positive time to both settings for a `RUNNING`/`IDLE`
  session. Current and resulting values are shown. Mutations enforce bounds,
  current state, optimistic versions, idempotent replay, and immutable reasons.
- The five-minute monitor calculates independent sleep and archive deadlines
  from the persisted timeout plus `idle_since` or `sleeping_since`. It records
  bounded one-hour/fifteen-minute owner warnings and queues the existing guarded
  workflows. Command identity binds the calculated deadline, so extensions,
  player return, state drift, or an active workflow make stale work fail closed.
- The former wall-clock maximum no longer sleeps/archives normal sessions or
  blocks start/wake/restore/restart. Its persisted clock remains only for guarded
  failed-initial-creation cleanup. Later-lifecycle failures continue to retain
  data and resources for operator action.
- Old maximum-duration Discord components now direct administrators to the new
  menu. A routing defect found during coverage was corrected so all new timeout
  buttons, selects, and modal submissions reach the admin handler.
- Focused domain, service, DynamoDB atomic-write, Discord authorization/replay,
  workflow, monitoring, projection, migration, and stale-command tests pass.
  `go test -count=1 ./...`, `go vet ./...`, `go build ./cmd/...`, Terraform
  formatting/validation, and Lambda packaging pass. The bootstrap progress
  sampler regression now waits for its slow uploader to be fully established,
  removing a load-sensitive test race without changing runtime behavior. No AWS
  mutation or live Discord registration occurred.

## Important Operator Attention

- Lifecycle timeouts reduce unattended cost but do not guarantee an AWS spending
  cap. Monitor, queue, workflow, or cleanup failures can retain billable resources;
  keep AWS Budget alarms and operator review active.
- Deploy only when no Steam-authenticated lifecycle operation or broker exchange
  is active. Wait for active operations before deployment.
- Test-58 may retain a billable instance and volume. Inspect it before retrying.
- Test-60 (`ref_f735121a5ef9`) completed host bootstrap but was marked failed by
  the previously corrected broker-output path. Reconcile or terminate retained
  resources through the guarded operator workflow after deployment.
- Test-61 (`01M2GKV5ME3MG97V0FY5894M0V`) retains its earlier deadline state. Use
  the new session-timeout controls only after this slice is deployed.
- Cross-session managed-host S3 access remains an accepted supervised-development
  risk scheduled for Phase 19.3.

## Commands to Apply Current Changes

Run only after separately approving deployment and confirming no lifecycle or
Steam broker exchange is active:

```powershell
$Workspace = (Resolve-Path ".").Path
$env:GOCACHE = Join-Path $Workspace ".cache/go-build"
$env:GOMODCACHE = Join-Path $Workspace ".cache/go-mod"
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"

go test ./...
go vet ./...
go build ./cmd/...
terraform fmt -check -recursive infra/terraform
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev validate
terraform -chdir=infra/terraform/environments/dev plan -out=phase-19-2-lifecycle-timeouts.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-19-2-lifecycle-timeouts.tfplan
terraform -chdir=infra/terraform/environments/dev apply phase-19-2-lifecycle-timeouts.tfplan

aws lambda get-function-configuration --function-name game-server-platform-dev-discord-interactions --query '{State:State,Updated:LastModified}'
aws lambda get-function-configuration --function-name game-server-platform-dev-monitor-worker --query '{State:State,Updated:LastModified}'
aws lambda get-function-configuration --function-name game-server-platform-dev-command-worker --query '{State:State,Updated:LastModified}'
```

No Discord command registration is required because command definitions did not
change. Never reuse this saved plan after source, variables, credentials, or
remote state change; create and review a new plan instead.
