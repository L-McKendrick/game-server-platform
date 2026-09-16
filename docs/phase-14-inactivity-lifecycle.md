# Phase 14: Automatic Inactivity Lifecycle

Phase 14 established the conservative inactivity foundation. Phase 19.2 makes
its two thresholds configurable while preserving the same verified sleep and
archive workflows.

## Timeout policy

- EventBridge invokes the health and inactivity monitor every five minutes.
- For a `RUNNING` session, a completed Systems Manager health probe is followed
  by a bounded A2S query against the managed server endpoint.
- A successful count of zero starts or continues `idle_since`. The session is
  eligible for automatic sleep after its snapshotted timeout only when the latest persisted
  count is still zero and no more than ten minutes old.
- A positive count clears `idle_since`. A missing endpoint, timeout, malformed
  response, failed health probe, or any other unavailable count is `UNKNOWN`
  and also clears `idle_since`; unknown is never interpreted as zero.
- Successful sleep completion records `sleeping_since`. A session is eligible
  for automatic archive after its snapshotted timeout in `SLEEPING`.
- Defaults are 30 minutes without players and 7 days sleeping. Administrators
  can replace both defaults for future sessions or add time to both values for
  one `RUNNING` or `IDLE` session. Existing values never decrease.
- Because the scan runs every five
  minutes and work is asynchronous, an eligible transition normally begins on
  the first successful scan after the threshold rather than at the exact second.

## Command and workflow safety

The monitor never changes lifecycle state directly. It emits a system command
onto the existing FIFO command queue with an ID derived from the session and
the exact calculated deadline. The command also carries the `idle_since` or
`sleeping_since` anchor. Repeated scans therefore
produce the same request identity, and command/workflow replay does not start a
second execution.

The command worker reloads the session before it acquires the normal workflow
lock. It verifies the system actor, deterministic request fields, bound timing
anchor and calculated deadline, current lifecycle state, configured timeout,
fresh zero-player evidence where applicable,
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
- Owners receive at most one warning at each applicable one-hour and fifteen-minute
  threshold for the current sleep or archive deadline. An extension creates a
  new deadline identity; queued work for the old deadline fails closed.
- This phase changes Lambda code, queue permission, and Step Functions/IAM
  configuration. It does not add or change Discord command definitions.

The earlier wall-clock maximum-duration automation no longer drives normal
sleep/archive decisions. Its persisted clock remains only as a backward-compatible
safeguard for failed initial provisioning/bootstrap cleanup. Later-lifecycle
failures retain resources for operator action.
