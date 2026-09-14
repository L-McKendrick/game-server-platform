# Phase 19.2 maximum-duration contract

## Persisted model and policy

The existing session metadata item stores `maximum_duration_seconds`,
`maximum_duration_started_at`, and `maximum_duration_deadline_at`. New drafts
carry a 24-hour default. Valid configured durations are 1 hour through 7 days.
An absent duration on a legacy item resolves to the 24-hour default in the
domain without mutating the item on read. An absent start/deadline pair means
the clock has not started; loading an old session never creates an overdue
deadline. A partially populated or reversed timestamp pair is invalid.

The duration is wall-clock time, not accumulated running time. Later lifecycle
work starts it once when the first resource-bearing start is committed. Sleep,
wake, failure, archive, and restore never reset that original anchor. A
configured policy change affects new starts; existing sessions retain their
persisted duration and deadline unless explicitly extended by an admin.

The first accepted provisioning workflow starts the wall-clock clock. A failed
workflow start does not reset it; this is conservative for cost. Wake, restore,
and restart do not reset it. An expired session cannot start, wake, restore, or
restart; it may still sleep, archive, or be explicitly terminated.

The Administrator-only `/rb admin` maximum-duration menu configures a draft
session from 1 to 168 whole hours. A started session can only be extended,
in whole-hour increments, to a later deadline no more than seven days after
the immutable start. The admin supplies an audit reason (up to 200 characters).
The session event records actor, request/correlation ID, previous and new
deadline, reason, and time. Versioned writes and idempotency records reject
conflicting updates and replay. The owner is notified in the session channel;
only that owner's mention is permitted. The private status view shows the
configured limit and current deadline.

The existing five-minute monitor checks the persisted deadline, not
`created_at`. It queues at most one 1-hour and one 15-minute owner warning for
each deadline. Extension creates a new deadline identity, so a newly due
warning may be sent for it. At expiry, running/idle sessions queue the normal
sleep workflow; sleeping sessions queue the normal archive workflow, without
waiting for inactivity. Command IDs bind the session, deadline, and action;
the worker revalidates all three against current state and rejects stale
commands after extension or workflow-state drift. An active workflow lock
defers new deadline action until reconciliation releases the lock. The
duration clock continues while sleeping and during other workflows.

If initial provisioning or game/content bootstrap fails, the monitor warns
the owner that setup files and retained resources will be permanently deleted
at the deadline. At expiry it queues the **existing** termination workflow,
which verifies resource ownership before deleting the instance and volume,
removes session-owned objects, releases capacity, and retains an auditable
terminal record. A stale deadline, later extension, active workflow, or failure
from wake, restore, restart, archive, or termination itself cannot authorize
automatic deletion. An extension is rejected once termination or another
active lifecycle workflow has begun. Later-lifecycle failures with retained
resources still receive one owner-facing operator-attention alert; their data
is not automatically destroyed. An incomplete termination also retains
resource references and requires operator inspection rather than an unsafe
automatic retry.

The monitor scans all candidate pages in bounded DynamoDB requests, so a
later session cannot be starved by earlier rows. Warning intent and its audit
event are saved together before queueing. If queueing fails, the next monitor
pass retries the same deterministic notification ID and only marks it queued
after a successful send. A crash after queue acceptance but before that final
marker may repeat a warning; delivery is at-least-once, not exactly-once.
Very large metadata tables can increase the work per monitor pass.

## Operator checks

After a separately approved deployment, confirm the five-minute monitor runs
without errors and inspect its duration-warning and command-queue messages for
one test session before relying on the guardrail. Verify a one-hour warning,
a fifteen-minute warning, and that an expired running session starts the
normal sleep workflow; an expired sleeping session should start the normal
archive workflow. Check that an extension changes the deadline and invalidates
already queued old-deadline commands. Inspect retained EC2/EBS resources and
capacity when a workflow fails or a deadline action cannot complete. Do not
manually replay a termination command against an altered session; use the
existing operator cleanup path after checking resource tags and references.

This is a maximum-runtime policy, not an AWS spend guarantee. The monitor can
run late, queue delivery or a lifecycle workflow can fail, and later-lifecycle
failures deliberately preserve data and billable resources. Keep AWS budget
alarms and operator review active; investigate a missed monitor run, queue
DLQ item, or failed cleanup promptly. Never assume an expired timestamp alone
means EC2/EBS cost has stopped.

No `terraform apply` or live Discord command registration has been performed
for this development slice. The existing five-minute monitor schedule and
queues are reused; deploying updated Lambda packages requires a fresh reviewed
Terraform plan. Keep independent AWS billing alarms and operator monitoring
active until live verification and the later-lifecycle failed-state exception
are resolved.
