# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Phase 12 release work is on
`codex/fix-phase-12-reset-workflow-arns`. Step 12.13 is complete; stop before
adding another Phase 12 step.

## Final Review Hardening

- Merged the bootstrap handoff/input fixes already on `main`, preserving
  renewable Steam authorization leases, recoverable continuation delivery,
  mission/config escaping, explicit game selection, and deployment checks.
- Reset cleanup now validates every configured cleanup scope, walks bounded
  EC2/DynamoDB/S3/Step Functions/log pagination, preserves guild access,
  guild `server.cfg`, Steam authorization state, and the current reset audit,
  and fails closed on unknown metadata instead of deleting it.
- An unacknowledged reset attempt goes to the reset DLQ without automatic
  destructive replay. Discord receives reset queue permission only when the
  disabled-by-default reset gate is enabled.
- Reset confirmation replays validate environment, guild, actor, expiry, use,
  and the phrase derived from the immutable confirmation ID.
- Guild `server.cfg` snapshots require an exact guild/revision/SHA-256 object
  path. Transport errors cannot log signed Discord attachment URLs, deterministic
  invalid requests are not retried, transient failures retain bounded retries,
  and definitively superseded upload objects receive compensating deletion.
- Artifact-worker delete permission is limited to superseded guild
  configuration objects and does not permit deletion of session inputs.
- Archive/terminate confirmation retains current Discord role context while
  remaining optionless, atomic, state-bound, single-use, and replay-safe.
- Reset workflow scope is now an explicit flattened list of ARN strings,
  including every `for_each` lifecycle workflow plus provisioning and
  bootstrap. This fixes the Terraform plan-time unsupported-attribute error.
- Reset-worker deployment no longer attempts to set Lambda's reserved
  `AWS_REGION` environment key. The worker continues to read the region that
  Lambda supplies automatically at runtime.
- Archive and termination prompts now say to run optionless `/rb confirm`
  within ten minutes, without the awkward relative-time wording. The command
  definition remains optionless; any visible `code` field is stale Discord
  registration and is removed by re-registering `/rb`.
- Session configuration now carries a deterministic selection from the seven
  supported Arma 3 Creator DLC identifiers. Unknown or duplicate values and
  vanilla sessions with cDLC selections fail closed. DynamoDB and immutable
  configuration events preserve the selection without changing legacy rows.
- Discord cannot open a second modal directly from a modal submission. The
  planned flow therefore uses an ephemeral continuation button for modded
  creation, finishes vanilla creation after base setup, and makes `/rb mods`
  open the same shared mod-options modal directly.
- Page one now includes `Begin server setup`; its durable intent is preserved
  across DynamoDB and draft repair. Modded creation returns an ephemeral,
  stale-bound `Continue to mod options` button. Page two and `/rb mods` share
  the same optional preset upload and seven-item Creator DLC checkbox group.
- Shared mod-option writes recheck owner, guild, lifecycle lock, session
  version, supported values, and request idempotency. Existing sessions can
  change only cDLCs, only the preset, or both without changing a running server
  in place. Drafts without an active preset can recover through `/rb mods`.
- Discord modal controls support option emoji but not arbitrary images,
  thumbnails, or media galleries. Official cDLC artwork is therefore not
  renderable in this form; official product names are used without placeholders.
- `Begin server setup` now invokes the existing session start use case only
  after required artifacts make the session ready. Its deterministic command
  identity makes artifact replay safe and retries a failed queue handoff.
- Creator DLCs reuse the existing revisioned mod path: bootstrap verifies the
  selected server directories and combines them with Workshop links in the
  same `mods.txt` and `-mod` argument.
- Archive manifests preserve the canonical cDLC selection and restore rejects
  drift. Public cards and private status show concise product names.
- Step 12.13 review now makes the form the sole cDLC authority. Preset ingestion
  accepts only `ModContainer` rows and rebuilds both the private bootstrap input
  and public download, dropping `DlcContainer`, footer, and unrelated IDs.
- cDLC-only modded sessions are supported without a placeholder Workshop
  preset. Combined cDLC+preset submissions remain draft/pending until validation
  so automatic start cannot race the artifact worker.
- Bootstrap mod checkpoints include configuration revision, ensuring cDLC-only
  changes are not skipped when the Workshop preset revision is unchanged.

## Validation

- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass with workspace-local Go caches.
- Focused reset, server-config, confirmation, session/workflow, S3, bootstrap,
  Discord interaction, and registration suites pass.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- Focused `go test ./internal/config` passes with the workspace-local Go cache,
  confirming the worker can continue using the runtime-provided region.
- Focused interaction and command-registration tests pass, and the parsed
  `/rb` JSON confirms confirm/cancel-confirmation remain optionless.
- Focused domain, session-service, and DynamoDB adapter tests pass for the
  Creator DLC catalog, invariants, request hashing, events, and persistence.
- Full `go test ./...` passes after the shared mod-options and begin-setup UI
  changes, including creation continuation, existing-session revision, cDLC
  persistence, stale-state, and modal-bound validation coverage.
- Focused artifact, bootstrap, archive, restore, domain, and session-card tests
  pass for deterministic automatic start, cDLC directory mapping, shared mod
  launch composition, manifest drift checks, and presentation.
- Post-step review regression covers cDLC rows and unrelated footer IDs in
  uploaded presets, sanitized server inputs, cDLC-only readiness/bootstrap,
  combined-form start ordering, configuration-aware checkpoints, and truthful
  cDLC-only status presentation.
- The `/rb` command JSON parses, `git diff --check` passes, the bootstrap
  artifact passes its Bash syntax test, and all 13 Lambda archives package.
- Race coverage was not run locally because this Windows host has no C
  compiler; CI remains authoritative for `go test -race -cover ./...`.

## Deployment Disposition and Attention

- No deployment, Discord registration, live reset, billable acceptance, or
  deferred Phase 10 retry was run during this review.
- Never reuse `phase-12-8-6-scoped.tfplan` or another older saved plan.
- The failed apply may have created other resources before Lambda creation
  stopped. Do not reuse its saved plan; create and review the fresh plan below,
  which will reconcile the partial apply and omit the reserved environment key.
- Reset remains disabled by default. The default action is to keep
  `reset_enabled = false`; enabling it and executing a live reset are separate
  explicit decisions.
- Re-register `/rb` so Discord removes the former confirmation `code` options.
  Pre-deployment code confirmations expire and must be requested again after
  release.
- No live session was started and no Creator DLC ownership was assumed. Steam
  entitlement and selected content are checked during normal bootstrap.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-mod-preset-hardening.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-mod-preset-hardening.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-mod-preset-hardening.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```
