# Current Work

## State and Objective

The beta client-preset integrity defect exposed by session `bro` is fixed on
`codex/fix-bro-session`, based on `3eba37a` (`main`). The artifact-worker fix is
deployed in development and `bro`'s retained preset metadata is repaired.

## Current Handoff

- Client preset ingestion previously stored generated public-modlist bytes at
  the digest-addressed preset input key. The key retained the original upload's
  SHA-256, so scoped host access rejected the stored bytes during bootstrap.
- Ingestion now preserves the original validated preset bytes, content type,
  digest-derived key, and checksum. The sanitized public modlist remains a
  separate object used for Discord publication.
- Regression coverage verifies the two objects have distinct bytes and that the
  private preset key and upload checksum match the original preset.
- The reviewed targeted Terraform plan
  `bro-preset-integrity-minimal.tfplan` updated only the development
  artifact-worker Lambda. The broader `bro-preset-integrity.tfplan` was not
  applied because local `.tfvars` drifted from live Discord, provisioning, and
  capacity settings.
- `bro` (`01M31DE6PZ34F8W1NFH2D775KM`) had its valid stored preset copied to a
  checksum-correct key. A version-guarded DynamoDB transaction updated both
  preset pointers from version 29 to 30 and appended
  `PresetIntegrityRepaired` audit evidence.
- An authorized Discord retry started workflow `1551507903913918464` on the
  retained instance. At the last observation the session was `INSTALLING`, the
  command was in progress, and durable progress had advanced past the checksum
  boundary to `MODS_APPLIED`.
- An attempted operator queue retry was denied by normal guild-role
  authorization and made no session change. It will move to the command DLQ
  after five receives unless removed through the existing operator tooling.
- Focused tests pass. `go vet ./...`, `go build ./cmd/...`, and
  `git diff --check` pass. `go test ./...` has only the pre-existing unrelated
  `TestArchiveWorkflowCompletesSuccessfullyAfterDurableCompletion` failure.

## Important User Attention

- Monitor workflow `1551507903913918464` until `bro` reaches `RUNNING` or a new
  actionable failure. Do not submit another start while it is active.
- Inspect and remove or quarantine the single authorization-denied repair
  command after it reaches the command DLQ; do not redrive it.
- Reconcile the ignored development `.tfvars` with live values before the next
  untargeted Terraform deployment. The current local values would change the
  Discord application/guild, disable provisioning, and reduce capacity.

## Commands to Apply Current Changes

No further code deployment or Discord command registration is required; the
artifact-worker source correction is already deployed in development.

Verify the active retry reaches `RUNNING`, then inspect the command DLQ with the
existing reliability runbook. Do not apply `bro-preset-integrity.tfplan`.
