# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is
proceeding before them by explicit user-approved roadmap reorder because
current limitations prevent completing those prerequisites; this does not
mark either prerequisite phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.6 are
complete. Step 12.6 is recorded by the scoped commit
`feat(discord): add safe modlist revisions` and is pushed to the phase branch.

## Current Step 12.6 Outcome

- Sessions now carry strict active and pending preset revision metadata plus a
  monotonic session-local revision sequence. `preset_object_key` remains a
  write-through compatibility projection of the active revision.
- Legacy rows that contain only `preset_object_key` synthesize active revision
  1 on read and persist additive revision fields on the next write; no table
  replacement or eager migration is required.
- New preset uploads during draft creation establish active revision 1.
  Revision validation binds a pending revision to its active base and rejects
  status, timing, sequence, or compatibility-pointer drift.
- Dedicated staged, applying, activated, failed, and rolled-back event types
  provide immutable revision-change audit contracts.
- `/rb mods` opens an owner-authorized private one-file modal bound to the
  active revision. A valid submission queues asynchronous preset validation;
  it does not claim acceptance or interrupt a running service.
- Accepted uploads create the next pending revision with sanitized modlist
  metadata and an immutable staged event. Invalid uploads retain active
  authority and audit rejection; pending files are never published as active.
- Bootstrap/start, wake, and restore bind a pending revision to the owning
  workflow as `APPLYING`. Managed-node commands select that pending key while
  the active revision and compatibility pointer remain authoritative.
- Bootstrap uses revision-specific Workshop markers and revision-addressed
  config files. Wake runs a bounded mod-application stage before health.
  Restore extracts the verified archive before bootstrap so archived config
  cannot overwrite pending intent.
- Only the health-success lifecycle completion promotes pending to active,
  clears pending, and updates the compatibility pointer in the same durable
  session mutation.
- Bootstrap, wake, and restore application failures now run a bounded managed
  rollback against the prior active revision. The rollback outcome and bounded
  diagnosis remain on the failed pending revision; active authority never
  moves on failure or rollback-command replay.
- Canonical cards derive active and pending revision numbers, state, and timing
  from the session aggregate. A promoted sanitized modlist is published only
  for the lifecycle transaction that activated it; stale companion-message
  references are hidden and stale queued attachments cannot replace active
  download authority.
- Additive schema-v1 archive manifests snapshot redacted active/pending intent
  and restore validation rejects revision drift while accepting legacy
  manifests and the restore-owned pending-to-applying transition. Permanent
  termination clears all revision authority after versioned session objects
  are deleted, while immutable archive/termination audit events retain revision
  numbers and status without free-form diagnostic text.

## Validation

- Focused 12.6.1 domain and DynamoDB persistence tests pass, including new
  revision creation, legacy read/write migration, invariant rejection,
  immutable event facts, and active/pending round trips.
- Focused 12.6.2 domain, session application, artifact worker, raw Discord
  interaction, command-registration, and SQS artifact-queue tests pass.
- Focused 12.6.3 lifecycle, bootstrap/wake/restore worker, SSM command,
  bootstrap-shell syntax, restore ordering, and Terraform validation pass.
- Focused 12.6.4 domain, DynamoDB, managed rollback command, lifecycle worker,
  bootstrap-shell syntax, workflow-state-machine, and Terraform validation
  tests pass.
- Focused 12.6.5 card projection/rendering, lifecycle notification, artifact,
  notification-worker, SQS, session-read authorization, and Discord delivery
  tests pass.
- Focused 12.6.6 archive-manifest, restore-transition, termination-cleanup,
  audit-redaction, DynamoDB round-trip, application-service, worker, and legacy
  compatibility tests pass.
- The complete-step review covered authorization, revision consistency,
  idempotency, rollback safety, archive/restore fidelity, auditability, billing
  warnings, and accidental promotion. It fixed cross-channel artifact ingest,
  diagnostic redaction, and truthful `UNVERIFIED` rollback disposition.
- Full `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass. Lambda packaging, Discord command JSON parsing,
  bootstrap Bash syntax, tracked Terraform formatting, Terraform validation,
  and `git diff --check` pass.
- Race validation remains unavailable on this Windows host because
  `CGO_ENABLED=0` and no C compiler is installed. The pre-existing populated
  local `infra/terraform/environments/dev/tfplan` remains untracked and was not
  modified. Validation used Go 1.26.6 (one patch
  newer than the repository's 1.26.5 CI target) and Terraform 1.15.8.

## Operator Attention

Step 12.6 is complete. Do not run Terraform apply without the separately
required plan, budget-recipient, and deployment approvals. No automatic retry
is scheduled for failed revision application or an unverified rollback;
operators must use the persisted card/status and support reference.

The current `/rb create` flow is intentionally Arma 3-specific. Phase 13.5 must
add a game field with autocomplete and route the selected supported game into
its game-specific creation/setup contract; it must not hard-code Arma 3 as the
only visible choice once another game is supported.

The current start boundary routes a ready session to provisioning and a later
start from provisioned state to bootstrap. This two-command UX is explicitly
scheduled for correction in task 12.8.7: creation remains non-billable, while
one `/rb start` owns provisioning through playable health acceptance and
repeated starts return the active operation's progress.

## Exact Next Step

Begin task 12.7.1 by defining the stable cross-workflow stage taxonomy and
persisting stage start/completion timestamps without raw command output. Start
that work only in a new development prompt; do not treat deferred Phases 10-11
as complete.
