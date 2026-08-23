# Current Work

## State and Objective

Phases 1-10 and 12 are complete; Phase 11 remains pending. Phase 13 step 13.2
is complete on `codex/phase-13-mission-management`. No deployment, Terraform
apply, or Discord registration was performed.

## Completed Development

- Arma 3 creation accepts an optional mission upload and otherwise uses BI's
  `MP_ZGM_m12.Stratis` without creating a placeholder artifact.
- Immutable, content-addressed mission records retain accepted/rejected and
  logically removed history. Backward-compatible configured/current
  projections persist through archive, restore, and termination.
- `/rb edit session:<slug> section:<mods|mission-files>` replaces `/rb mods`.
  The private mission manager renders five files per page within Discord's
  component bounds and provides `Default`, protected `Remove`, `Add mission`,
  and stale-safe pagination controls.
- Start, wake, and restore snapshot the configured mission. Bootstrap downloads
  the exact upload or uses the BI default and always replaces `class Missions`
  after loading generated or Administrator-provided `server.cfg`.
- **Begin server setup** remains functional for every readiness path. A vanilla
  session with no mission upload queues its deterministic start immediately;
  uploaded-mission and modded sessions wait for required artifact/mod validation.
  Initial delivery and idempotent replay preserve signed Discord role IDs.

## Validation

- `go test ./...` and `go vet ./...` pass with workspace-local caches.
- All 13 Lambda archives package successfully.
- `terraform fmt -check -recursive infra/terraform` passes.
- `terraform -chdir=infra/terraform/environments/dev validate` passes.
- Bootstrap Bash syntax and Discord command JSON validation pass.
- `git diff --check` passes.

## Next Development Task

- Continue with one selected pending Phase 13 step, or return to Phase 11
  production hardening. Start the selected step on its own numbered task plan.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-13-mission-management.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-13-mission-management.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-13-mission-management.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1
./scripts/register-discord-command.ps1 -ApplicationId "<application-id>" -GuildId "<development-guild-id>"
```

The fresh Terraform plan deploys the changed Lambda/bootstrap artifacts.
Discord bulk registration is required because `/rb mods` changed to `/rb edit`.
