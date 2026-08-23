# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Phase 12 Steps 12.1-12.8 are merged. Steps 12.9-12.11 are complete on
`codex/phase-12-discord-experience`. The branch is undergoing its final
cross-step security, integrity, reliability, edge-case, efficiency, and UX
review before a pull request.

## Current Review Scope

- Integrate the bootstrap handoff/input hardening already merged to `main`.
- Review the Administrator-only runtime reset, private guild `server.cfg`, and
  code-free archive/terminate confirmations as one release unit.
- Preserve current Administrator checks, atomic confirmation/state binding,
  reset ownership boundaries, private configuration redaction, and truthful
  retry and billing language.
- Run focused and full proportional validation, then commit and push the review
  hardening. No deployment, command registration, live reset, or deferred
  Phase 10 retry is authorized by this review.

## Deployment Disposition

- The previously applied `phase-12-8-6-scoped.tfplan` predates the completed
  Phase 12 work and must not be reused.
- Steps 12.9-12.11 are not deployed. The user will run credential-bearing
  deployment and Discord registration after reviewing a fresh exact plan.
- Reset remains disabled by default. Enabling it and executing a live reset
  require separate explicit decisions.
- Step 12.11 requires re-registering `/rb` so Discord removes the old `code`
  options. Pre-deployment code confirmations expire and must be requested again.

## Operator Commands

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-release.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-release.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-release.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```

After deployment, run the runbook's non-billable acceptance with a configured
role member and a manager. Billable/destructive checks remain **not run —
approval required** unless separately authorized. Phase 10 retries remain
deferred: no retry was scheduled, performed, or implied.
