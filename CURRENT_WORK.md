# Current Work

## State and Objective

Phases 1-10 and 12 are complete; Phase 11 remains pending. Phase 13 steps 13.2
and 13.7 are complete on `codex/phase-13-mission-management`. No deployment,
Terraform apply, or Discord registration was performed.

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
- The mission-block rewrite is compatible with Ubuntu's default `mawk`; its
  scanner and brace counter no longer declare the built-in `index` or `close`
  names as local parameters.
- Empty-session termination always serializes `objects_deleted: 0`, preventing
  the final Step Functions JSONPath from failing after successful cleanup.
- Live sessions `test-12-3` and `test-13` had no remaining EC2, EBS, or S3
  resources and were safely finalized as unlocked `DELETED` tombstones.
- Live session `test-14` failed during configuration deployment because Ubuntu
  `mawk` rejected the AWK built-in `close` name as a helper local. Its EC2
  instance and data volume remain retained and billable; no retry is scheduled.
- The development Terraform directory retains only the current saved plan.
  Twenty-seven stale plans were moved to the recoverable, gitignored archive
  `.cache/obsolete-terraform-plans-20260823`; active providers, configuration,
  local variables, state migrations, and the current plan were preserved.
- **Begin server setup** remains functional for every readiness path. A vanilla
  session with no mission upload queues its deterministic start immediately;
  uploaded-mission and modded sessions wait for required artifact/mod validation.
  Initial delivery and idempotent replay preserve signed Discord role IDs.
- `docs/deployment.md` provides a start-to-finish self-hosting guide for Discord
  application setup, Terraform state, AWS deployment, secret installation,
  guild command registration, verification, and provisioning enablement. The
  README links to it.
- The README's current-status section now describes the product and its user
  workflows without exposing an implementation inventory or stale roadmap state.

## Validation

- `go test ./...` and `go vet ./...` pass with workspace-local caches.
- All 13 Lambda archives package successfully.
- `terraform fmt -check -recursive infra/terraform` passes.
- `terraform -chdir=infra/terraform/environments/dev validate` passes.
- Bootstrap Bash syntax and Discord command JSON validation pass.
- The focused bootstrap adapter regression covers both Ubuntu `mawk` local-name
  collisions (`index` and `close`) and passes.
- `git diff --check` passes.

## Next Development Task

- Phase 14 now plans the basic automatic inactivity lifecycle: sleep after 30
  continuous verified zero-player minutes and archive after 72 continuous hours
  sleeping. Development has not started. Before implementation, start the phase
  on a new branch and break Phase 14.1 into its numbered task plan.
- Remaining product delivery is ordered as Phase 14 inactivity automation,
  Phase 15 maximum-duration cost guardrails, Phase 16 production hardening and
  measured optimization, then Phase 17 potential enhancements.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out phase-13-test-14-mawk-fix.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-13-test-14-mawk-fix.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-13-test-14-mawk-fix.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1
./scripts/register-discord-command.ps1 -ApplicationId "<application-id>" -GuildId "<development-guild-id>"

# Only after deployment verification, retry retained session test-12-2 once.
# In Discord: /rb start session:test-12-2
# Retry retained session test-14 only when the operator chooses to do so.
# In Discord: /rb start session:test-14
```

The fresh Terraform plan deploys the changed Lambda/bootstrap artifacts.
Discord bulk registration is required because `/rb mods` changed to `/rb edit`.
The documentation-only 13.7 change adds no deployment or registration action.
The README status cleanup adds no deployment or registration action.
No retry is scheduled for `test-12-2`; its retained running EC2 instance may
continue incurring cost until the operator retries or ends the session.
No retry is scheduled for `test-14`; its retained running EC2 instance and data
volume may continue incurring cost until the operator retries or ends it. The
fresh saved plan proposes one content-addressed bootstrap-object replacement
and seven in-place Lambda/IAM-policy updates, with no runtime EC2/EBS changes.
`test-12-3` and `test-13` need no further recovery action.

The Phase 14 planning-only update requires no deployment or Discord command
registration.
