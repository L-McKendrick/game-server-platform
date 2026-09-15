# Phase 19.2 lifecycle-timeout contract

## Policy and administration

Each session stores two independent thresholds: time with no verified players
before automatic sleep, and time sleeping before automatic archive. New sessions
snapshot the guild defaults, which are 30 minutes and 7 days when no guild policy
exists. Legacy rows with absent values receive the same defaults when loaded.

Discord Administrators use `/rb admin` → **Session timeouts**. **Edit future
sessions** replaces both guild defaults within 10–1,440 minutes and 1–90 days.
**Extend an active session** lists only `RUNNING` and `IDLE` sessions and adds a
positive amount to both stored values; it cannot shorten either threshold or
change drafts, transitions, sleeping, archived, failed, or deleted sessions.
Both forms show current values and the response shows the resulting values.

Every mutation requires a reason of at most 200 characters. Guild policy writes
atomically store the versioned policy, immutable audit record, and idempotency
record. Session extensions use the existing atomic session/event/idempotency
write. Replays return the original result and conflicting reuse fails closed.

## Monitoring and enforcement

The five-minute monitor calculates the next deadlines independently:

- automatic sleep: `idle_since + sleep_after_seconds`;
- automatic archive: `sleeping_since + archive_after_seconds`.

Sleep still requires a fresh authoritative zero-player observation. Unknown,
failed, malformed, or stale player evidence clears or pauses the idle window and
is never treated as an empty server. Archive still requires `SLEEPING`, retained
instance and volume references, and no active workflow lock.

The monitor durably records at most one owner warning at one hour and fifteen
minutes before each current deadline, then queues the existing sleep or archive
command. Notification mentions are limited to the session owner. Command IDs and
parameters bind the session, timing anchor, calculated deadline, and action. The
worker reloads the session and recomputes the deadline before acquiring the
normal workflow lock, so an extension, player return, state change, or concurrent
workflow invalidates stale queued work. Queue failures remain retryable under the
existing deterministic command identity.

The old 24-hour wall-clock maximum no longer sleeps or archives normal sessions
and no longer blocks start, wake, restore, or restart. Its persisted timestamps
remain backward compatible and continue to guard only failed initial
provisioning/bootstrap cleanup. Later-lifecycle failures retain resources and
data for operator action; automation does not bypass archive verification,
resource ownership checks, capacity truth, or workflow reconciliation.

## Operator checks

After a separately approved deployment, use a disposable session to verify the
configured values shown by `/rb status`, the one-hour/fifteen-minute warnings,
automatic sleep after continuous verified emptiness, and archive after continuous
sleep. Extend the session after a command is queued and confirm the old command
fails closed while a command for the new deadline can proceed. Inspect monitor,
command-worker, workflow, DLQ, EC2/EBS, and capacity state after any failure.

This policy reduces unattended cost; it is not an AWS spending guarantee.
Monitor, queue, workflow, or cleanup failures can leave billable resources, and
later-lifecycle failures intentionally retain data. Keep AWS Budget alarms and
operator review active. Never run Terraform apply or live command registration
without a fresh reviewed plan and separate deployment approval.
