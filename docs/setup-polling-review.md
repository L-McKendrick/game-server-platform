# Setup polling branch review

Reviewed `codex/setup-polling-progress` against `main` on 2026-09-06.

## Findings

The Arma sampler originally cancelled its sleep but waited for a foreground S3
upload before exiting. The review makes uploads interruptible and waits for
cleanup before SteamCMD output removal or stage/activity updates. Telemetry also
closes inherited host/bootstrap lock descriptors, so an orphan cannot retain the
host lock. Regression coverage includes a deliberately stalled upload, normal
completion, transient download failure, and Steam reauthorization failure.

No additional blocking regression was identified in the changed provisioning
and bootstrap orchestration. This is a code and offline-test assessment, not a
claim of exhaustive live lifecycle validation.

| Area | Evidence and limits |
| --- | --- |
| Provisioning | Both stages retain 15-second waits and 40 failed observations; input-carried counters replay deterministically; readiness wins on the last permitted observation; false booleans are explicit in task JSON. |
| Bootstrap | Initial/default waits remain 30 seconds; explicit Arma/Workshop stages select 120 seconds; persisted deadline caps subsequent waits; terminal SSM results take precedence; rollback stays at 30 seconds. |
| Snapshots | Workflow-scoped keys, 16 KiB read bound, unknown/legacy-stage fallback, bounded activity fields, and checkpoint/activity clearing. S3 errors retain SSM fallback rather than failing setup. |
| Steam sampler | Latest download-phase percentage only, bounded 8 KiB private-log tail, interruptible cleanup, original command result preserved, no credentials/raw logs on the card. |
| Workshop | Current item ID and batch position, stable retry position, cached-item skips, shared client/server-mod batch; mission batch is separate. No unverified per-item percentage. |
| Discord | Canonical numeric ID links, escaped fallback text, compact stage/count layout, actionable conditions retained, persisted Refresh revision and rate-limit checks retained. |

The 120-second wait reduces steady installation polling transitions by 75%.
Provisioning removes two counter states per unsuccessful poll plus one fixed
initial state; savings depend on readiness duration. Historical test-44 evidence
showed provisioning completed in 33.35 seconds and bootstrap used 120-second
waits. Its 36 transitions through 05:46 HST on September 5 compare with roughly
108 at the previous cadence; this is a partial-run comparison, not a completed
setup benchmark. Arma percentage capture was added after that observation.

## Known limitations and deployment

Arma percentages can lag by 30 seconds of local sampling plus 120 seconds of
workflow polling and delivery overhead. Upload failures may leave older progress
visible. Refresh reads persisted progress. Short Workshop downloads can finish
between observations. Existing executions retain their state-machine definition
and running host script; deploy before starting a fresh setup to validate the
complete new behavior. Existing workers accept old request shapes, but rolling
back to old workers while new executions are active is unsafe because new ASL
expects next-poll/counter result fields.

The separately recorded test-44 restore failure remains open in Phase 16.7:
replacement-host AWS CLI prerequisites and safe restore failure finalization.
This review does not fix or live-test that unrelated lifecycle path.

Use the fresh-plan commands in CURRENT_WORK.md. The branch changes existing
provisioning/bootstrap state machines, the bootstrap script, and shared Lambda
packages; it adds no resources, IAM grants, migrations, or Discord commands.

## Validation

Go 1.26.5 coverage tests, vet, command builds, native Bash behavior/syntax,
Terraform 1.15.8 formatting/validation, and Lambda packaging all pass.
Local Windows has no
working C compiler, so required GitHub CI supplies the race-test gate. No AWS
mutation, Terraform apply, Discord message, or PR creation is part of this review.

## Proposed pull request

Title: `perf(setup): reduce polling transitions and clarify download progress`

Description:

Reduce setup orchestration overhead while preserving bounded retries, deadlines,
and rollback behavior. Provisioning carries poll counters in existing observer
results, removing two states per failed poll. Bootstrap uses 120-second waits
while installing Arma or Workshop content and 30 seconds otherwise.

Show the latest Arma download percentage and a linked Workshop item ID, with
`(x of y)` in the Workshop stage label. Simplify public progress and Refresh
feedback, and group private status details with linked Workshop sources. The
host sampler is bounded and promptly cancels sleep or upload during cleanup.
Workshop percentages remain unavailable pending a verified signal.

Infrastructure: update existing state-machine definitions, the bootstrap script,
and affected Lambda packages. No new resources, permissions, schema migrations,
or Discord command registration. Package, review a fresh saved Terraform plan,
and apply that exact plan using CURRENT_WORK.md; no deployment in this branch
review. Avoid rolling back worker binaries while new executions are active.

Validation: Go coverage tests, vet, command builds, native Bash behavior/syntax,
Terraform formatting/validation, and Lambda packaging. CI race testing and a
fresh post-deployment setup remain release checks. The known Phase 16.7 restore
failure is outside this PR's implemented scope.
