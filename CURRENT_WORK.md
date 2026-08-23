# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Phase 12 Steps 12.1-12.7 are merged. Step 12.8 is complete on
`codex/phase-12-discord-experience`. Steps 12.9 and 12.10 are complete locally.
The next scoped UX change removes user-entered confirmation codes in favor of
one server-resolved `/rb confirm` follow-up.

## Delivered

- `/rb admin` is the single protected administration entry point. It opens an
  ephemeral component menu for access and card repair; there is no standalone
  `/admin` command or nested admin syntax.
- Normal access follows a durable Discord role policy. Administrator or Manage
  Server is rechecked from each signed command/component payload. The role
  picker replaces the complete allowed set, and removing all normal-role access
  requires a separate danger confirmation while the manager recovery path
  remains available.
- The wishlist Discord cost command is omitted. The Lambda has no Cost Explorer
  integration or Billing permission. The existing Phase 5 Terraform budget is
  unchanged; any ownership migration remains separate operator work.
- `/rb help`, first-run guidance, state-aware next actions, useful empty/success
  responses, guild-only behavior, mobile-safe component layouts, accessible
  text-plus-color states, and bounded stale-interaction guidance are complete.
- One `/rb start` now persists a deterministic pending bootstrap workflow after
  provisioning succeeds and queues its internal continuation through the
  existing FIFO command boundary. The command worker accepts that continuation
  only when its provision workflow, requester, correlation, guild/channel,
  deterministic identity, idempotency key, and active bootstrap lock all match.
  Repeated starts return existing progress and queue no duplicate work.

## Step 12.9 Delivered

- `/rb admin` now exposes one disabled-by-default full reset only to current
  Discord Administrators; Manage Server and normal access roles are
  insufficient. An exact ten-minute phrase and atomic environment lock protect
  the action, and the menu reports active or latest terminal state.
- The reset worker cleans platform-owned runtime state only: sessions, active
  executions, tagged game instances/disposable volumes, Discord session
  messages, session S3 versions, runtime queues/DLQs, runtime metadata, and
  eligible pre-reset logs.
- It preserves the installed control plane, Terraform state/resources, guild access,
  secrets, configuration, budget, and one minimal reset audit result. AWS
  billing, CloudTrail, and retained service execution history are not erasable
  runtime state and must be reported truthfully.
- Cleanup uses bounded pagination, exact ownership checks, idempotent absent
  outcomes, a final drift check, safe terminal errors, and explicit
  partial-failure/billing warnings. No automatic cleanup retry is scheduled.
- Terraform adds a reset FIFO queue/DLQ, worker, least-privilege runtime policy,
  packaging, and `reset_enabled = false`. No live reset was executed.
- Step 12.10 follows the reset with an Administrator-managed guild-level Arma
  `server.cfg`. Its private revisioned artifact is captured when bootstrap
  starts; contents are never rendered, and sessions retain the generated safe
  default when no custom file is active.

## Step 12.10 Delivered

- Current Discord Administrators can upload, inspect metadata for, replace, or
  remove one guild-level Arma `server.cfg` through `/rb admin`; Manage Server
  and configured access roles cannot use the controls.
- The artifact path accepts only non-empty UTF-8 `.cfg` files up to 64 KiB,
  stores immutable private revisions outside session prefixes, and never
  renders file contents, digests, or object keys.
- Start commands capture the active revision/object/digest or an explicit
  generated-default selection. Workflow acquisition persists the snapshot,
  and bootstrap downloads and verifies that exact object without state drift.
- Replacement affects future starts only. Revision-confirmed removal returns
  future starts to the generated safe default while retaining private prior
  revisions for deterministic existing-session replay.
- Deterministic invalid or stale uploads are acknowledged without retry;
  transient and unknown worker failures retain the existing bounded SQS retry.

## Validation

- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass using workspace-local Go caches.
- Focused native tests pass for domain, access, provisioning, sessions, and
  registration. The changed workflow and Discord suites also pass under
  Go JavaScript/WebAssembly with Node.
- Race coverage was not run locally because this Windows host has no C compiler;
  CI remains authoritative for `go test -race -cover ./...`.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- The `/rb` JSON parses, `git diff --check` passes, Terraform formatting and
  validation pass, and all 13 Lambda archives package successfully.
- Focused server-config tests pass for authorization, validation, DynamoDB and
  memory persistence, Discord raw payloads, start/workflow snapshots, checksum
  bootstrap, replacement/removal, replay, redaction, and no-retry rejection.

## Deployment Disposition

- The previously approved `phase-12-8-6-scoped.tfplan` was applied successfully
  with 0 additions, 12 Lambda updates, and 0 deletions. All functions then
  reported `Successful`/`Active`, and the unsigned endpoint probe returned 401.
- That applied plan predates the final 12.8.7 continuation and streamlined admin
  changes. It is stale and must not be reused or represented as deploying the
  completed step.
- The user will run the credential-bearing deployment and Discord registration.
  Create and review a fresh release plan after packaging. It must include the
  changed Lambda packages plus `aws_sfn_state_machine.provision_session` and
  `aws_iam_role_policy.provision_workflow`. A full plan may time out here while
  refreshing the unchanged AWS Budgets endpoint; any scoped alternative still
  requires exact review and approval.
- Steps 12.9-12.10 have not been deployed. Local Terraform inputs and saved
  plans remain outside Git; enabling reset requires a fresh exact plan review
  and a separate live-reset decision.
- Discord application `1533676701354299402`, guild `1192304488351019008`, and
  endpoint `https://ujg7q9fubf.execute-api.us-west-2.amazonaws.com/discord/interactions`
  are the known development targets. No bot token was retrieved and no final
  command registration or live-guild acceptance was performed.

## Operator Commands

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-9.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-9.tfplan
# Apply only after separate approval of that exact saved plan.

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```

After deployment, run the runbook's non-billable acceptance with a configured
role member and a manager. Billable/destructive checks remain **not run —
approval required** unless separately authorized. Phase 10 retries remain
deferred: no retry was scheduled, performed, or implied.
