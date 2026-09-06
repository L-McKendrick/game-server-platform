# Current Work

## State and Objective

Phase 18.1 is implemented on `codex/discord-lifecycle-ux`. Next: complete
18.2 (restart), then perform item-level checks, review, and commit before
ending the turn. The user authorizes all nested tasks within each item.
At phase completion, review the branch and provide a PR title/description;
do not publish a PR.

## Current Handoff

- `/rb start` now routes sleeping sessions through the existing wake command.
  Owner/admin wake authorization, capacity checks, workflow progress, pending
  content handling, and worker revalidation remain on the existing path.
  Initial provisioning remains owner-only; provisioned bootstrap retry remains.
- `/rb wake` is removed from registration and routing; help directs sleeping
  sessions to start. Archived sessions still use `/rb restore`; unification is
  deferred to 20.7.4 after the known test-44 restore repairs.
- No infrastructure definitions changed and nothing was deployed or registered.
- Validation: Go 1.26.5 coverage suite, vet, command builds, Lambda packaging,
  registration contracts, focused lifecycle/authorization/capacity tests, and
  diff review. Local CGO is disabled; race coverage remains a CI requirement.
- The first full run hit the existing bootstrap sampler timing test; it passed
  in isolation and the full coverage rerun passed. No bootstrap code changed.
- Restart and notification/card requirements remain recorded in PROJECT_PLAN.md.
  No further requirements clarification is needed before 18.2.

## Commands to Apply Current Changes

Run from the repository root when deploying this item. Package before creating
and reviewing a fresh plan; preserve existing plan files. Keep provisioning and
budget configuration unchanged. Do not deploy as part of routine validation.

```powershell
$env:GOTOOLCHAIN = "go1.26.5"
$env:AWS_PROFILE = "game-server-dev"
./scripts/package-discord-lambda.ps1
$phase18Plan = "phase18-1-start-wake-$(Get-Date -Format 'yyyyMMdd-HHmmss').tfplan"
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase18Plan"
terraform -chdir=infra/terraform/environments/dev show $phase18Plan
```

After reviewing and approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase18Plan
aws lambda get-function-configuration --function-name game-server-platform-dev-discord-interactions --profile game-server-dev --region us-west-2 --query '{Status:LastUpdateStatus,CodeSHA256:CodeSha256}'
```

Register the updated commands with the existing application/guild IDs and a
short-lived bot token in the process environment, as described in
`docs/runbooks/deploy-discord-interactions.md`:

```powershell
go run ./cmd/discord-register
Remove-Item Env:DISCORD_BOT_TOKEN
```

Verify `/rb wake` is absent and sleeping-session help recommends `/rb start`.
When a live wake is approved, use `/rb start` on a sleeping session and verify
private progress, final health, and pending-content application with `/rb status`.
Do not attempt live restore before 20.7 repairs are validated.
