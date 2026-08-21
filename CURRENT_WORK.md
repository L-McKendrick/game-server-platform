# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Phase 12 Steps 12.1-12.7 are merged. Step 12.8 is complete on
`codex/phase-12-discord-experience`; stop before Phase 13.

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
- `DEV_SETUP.local.md` is gitignored and contains a non-secret session startup,
  authentication, deployment-gate, and Git checklist.

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
- The `/rb` JSON parses, the retired command definition is absent,
  `git diff --check` passes, and all 12 Lambda archives package successfully.

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
- The populated local Terraform inputs, all saved plans, and the user-owned
  untracked `infra/terraform/environments/dev/tfplan` remain untouched.
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
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-8-final.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-8-final.tfplan
# Apply only after separate approval of that exact saved plan.

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```

After deployment, run the runbook's non-billable acceptance with a configured
role member and a manager. Billable/destructive checks remain **not run —
approval required** unless separately authorized. Phase 10 retries remain
deferred: no retry was scheduled, performed, or implied.
