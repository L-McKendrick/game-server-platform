# Phase 14: Automatic Inactivity Lifecycle

Phase 14 adds a conservative fixed-policy foundation for reducing inactive
server cost. A running session sleeps after 30 continuous minutes with no
players, and a session that remains sleeping for 72 continuous hours enters the
existing verified archive workflow.

## Fixed policy

- EventBridge invokes the health and inactivity monitor every five minutes.
- For a `RUNNING` session, a completed Systems Manager health probe is followed
  by a bounded A2S query against the managed server endpoint.
- A successful count of zero starts or continues `idle_since`. The session is
  eligible for automatic sleep at 30 minutes only when the latest persisted
  count is still zero and no more than ten minutes old.
- A positive count clears `idle_since`. A missing endpoint, timeout, malformed
  response, failed health probe, or any other unavailable count is `UNKNOWN`
  and also clears `idle_since`; unknown is never interpreted as zero.
- Successful sleep completion records `sleeping_since`. A session is eligible
  for automatic archive after 72 continuous hours in `SLEEPING`.
- The thresholds are fixed in this foundation. Because the scan runs every five
  minutes and work is asynchronous, an eligible transition normally begins on
  the first successful scan after the threshold rather than at the exact second.

## Command and workflow safety

The monitor never changes lifecycle state directly. It emits a system command
onto the existing FIFO command queue with an ID derived from the session and
the exact `idle_since` or `sleeping_since` value. Repeated scans therefore
produce the same request identity, and command/workflow replay does not start a
second execution.

The command worker reloads the session before it acquires the normal workflow
lock. It verifies the system actor, deterministic request fields, bound timing
value, current lifecycle state, fresh zero-player evidence where applicable,
and absence of another active workflow. Player return, unknown activity, wake,
manual lifecycle work, or concurrent state change makes the queued command fail
closed.

Automatic sleep uses the existing guarded sleep workflow. Automatic archive
uses the existing portable archive and verified-destruction workflow. For a
sleeping source, it first starts the retained tagged instance and waits up to
ten minutes for EC2 `running` and Systems Manager `Online`, then performs the
same host archive, S3 checksum verification, manifest persistence, ownership
checks, and guarded EC2/EBS destruction as a manual archive.

## Failure and operator behavior

- A command-queue failure is returned to the scheduled invocation so normal
  EventBridge/Lambda retry behavior can run the deterministic request again.
- A workflow-start failure records an actionable failed workflow and session
  card update, releases the lifecycle lock, and restores the source state. A
  sleeping archive retains its original `sleeping_since` value.
- Player-query failure only resets the automatic-sleep evidence. It does not
  stop the server, report zero players, or change lifecycle state.
- Archive integrity and resource-ownership failures retain infrastructure and
  identifiers according to the Phase 9 failure contract. Operators should use
  the existing status, retry, reconciliation, or termination paths; automation
  does not bypass those safeguards.
- This phase changes Lambda code, queue permission, and Step Functions/IAM
  configuration. It does not add or change Discord command definitions.

## Deferred expansion points

Later work may add administrator-configurable thresholds, a corroborating
server-side activity record, owner warnings and cancellation, quiet-hour or
calendar schedules, maximum-duration composition, temporary extensions, and
rest-aware behavior. Those features must preserve the current fail-closed
unknown-data rule, deterministic command identity, workflow locking, archive
integrity verification, resource ownership checks, and immutable audit trail.
