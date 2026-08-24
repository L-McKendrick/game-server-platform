# Current Work

## State and Objective

Phases 1-10, 12, and 13 are complete. Phase 14 steps 14.1-14.3 are complete on
`codex/phase-14-inactivity-lifecycle`. No deployment, Terraform apply, or
Discord registration was performed.

## Completed Development

- Scheduled monitoring now obtains bounded authoritative player counts through
  A2S instead of treating host-probe output or query failures as an empty server.
- Sessions persist the latest known/unknown player observation and a continuous
  zero-player start time. Unknown observations and player returns clear that
  continuity, and immutable activity events retain the evidence trail.
- DynamoDB session decoding remains compatible with records created before the
  inactivity fields existed.
- Fresh monitor observations that complete a continuous 30-minute zero-player
  window enqueue a deterministic automatic-sleep command. The command worker
  uses explicit system authority and revalidates the bound idle window, fresh
  evidence, lifecycle state, and workflow lock before starting the existing
  guarded sleep workflow.
- The monitor role has least-privilege send access to the existing command FIFO
  queue; queue failures surface for scheduled retry, and FIFO deduplication plus
  deterministic command IDs make replay safe.
- Successful sleep completion persists an explicit sleeping-since timestamp.
  The scheduled inactivity scan queues a deterministic automatic archive after
  72 continuous sleeping hours and the command worker revalidates the exact
  bound sleeping window, lifecycle state, and workflow lock.
- The guarded archive workflow can start a sleeping tagged instance, wait up to
  ten minutes for EC2 and Systems Manager readiness, and then reuse the existing
  checksum verification and tag-guarded destruction stages. Running-session
  archive behavior passes through without a host start.

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

- Stop before Phase 14.4 as requested. Phase 14.4 remains pending for broader
  end-to-end regression coverage and feature documentation.
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
terraform -chdir=infra/terraform/environments/dev plan -out phase-14-3-automatic-archive.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-14-3-automatic-archive.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-14-3-automatic-archive.tfplan
```

The package command rebuilds the changed monitor, command, sleep/wake, and
archive worker archives along with the other Lambda archives. The fresh plan
should deploy those worker changes, the monitor queue configuration and scoped
permission, the sleeping-host archive preparation states, and the archive
worker's scoped EC2/SSM permissions. No Discord registration is required.
