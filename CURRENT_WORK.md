# Current Work

## State and Objective

Phase 18.3 (creation readiness automation and notification) is implemented on
`codex/discord-lifecycle-ux`. Next: 18.4 (public-card lifecycle simplification).
Complete all nested tasks, then checks, review, and commit before ending the
turn. At phase completion, review the full branch and provide a PR title and
description without publishing a PR.

## Current Handoff

- `/rb create` now offers an off-by-default `Notify when ready?` selection.
  The session stores the preference, the invoking channel, and one durable
  initial-ready delivery-attempt timestamp.
- Successful initial bootstrap health claims and queues one ready message. It
  permits only the owner mention and links the session title when a persisted
  public-card reference exists; otherwise it uses the plain title.
- Ready-message delivery failures are logged and acknowledged without SQS
  retry. Workflow replay, wake, restore, and restart do not send another ready
  message.
- Initial uploaded presets and asynchronously resolved Workshop mod items or
  collections now enter the same deterministic automatic `/rb start` request
  after the session reaches normal provisioning readiness. Existing command
  authorization, capacity, lifecycle, and idempotency checks remain authoritative.
- No Terraform resources or Discord command definitions changed. Runtime code
  changed for Discord interactions, artifact, bootstrap, and notification
  workers.
- Validation passed with installed Go 1.26.6: sequential full coverage suite,
  vet, command builds, Lambda packaging, Terraform format/validate, focused
  readiness/mention/replay/persistence tests, and diff checks. The documented
  Go 1.26.5 toolchain download was sandbox-blocked; 1.26.6 is one patch newer.
  CGO is unavailable, so race coverage remains for required CI.
- Nothing was deployed or registered against Discord or AWS.

## Commands to Apply Current Changes

Run from the repository root. Package the affected Lambdas before creating a
fresh saved plan. Preserve existing plan files and do not change provisioning
or budget settings.

```powershell
$env:GOTOOLCHAIN = "go1.26.5"
$env:AWS_PROFILE = "game-server-dev"
./scripts/package-discord-lambda.ps1
$phase18Plan = "phase18-3-creation-readiness-$(Get-Date -Format 'yyyyMMdd-HHmmss').tfplan"
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase18Plan"
terraform -chdir=infra/terraform/environments/dev show $phase18Plan
```

After reviewing and approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase18Plan
foreach ($component in @("discord-interactions", "artifact-worker", "bootstrap-worker", "notification-worker")) {
  aws lambda get-function-configuration --function-name "game-server-platform-dev-$component" --profile game-server-dev --region us-west-2 --query '{Status:LastUpdateStatus,CodeSHA256:CodeSha256}'
}
```

Discord command re-registration is not required because command definitions did
not change. Verify `Notify when ready?` defaults off. With it enabled, test an
initial modded creation through an uploaded preset and a Workshop collection;
confirm automatic startup begins after validation, the healthy server produces
one owner-only message in the creation channel, card linking degrades to a plain
title when unavailable, and delivery failure is not retried. Confirm wake,
restore, and restart emit no creation-ready notification.
