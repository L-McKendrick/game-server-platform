# Phase 7: Monitoring

Phase 7 continuously observes already-running game sessions. It does not restart, stop, or re-bootstrap a host automatically; automatic recovery belongs to Phase 10 after retry and cancellation policies are implemented.

## Monitoring boundary

An EventBridge schedule invokes a monitor worker every five minutes. The worker scans only `RUNNING` sessions, dispatches one short Systems Manager probe per session, and records the pending probe identifier. A later invocation observes that identifier and writes an immutable health event only when the classified health state changes.

The host probe is read-only and returns compact JSON with:

- `arma_service`: active systemd state;
- `arma_udp_2302`: whether UDP 2302 is locally bound;
- `teamspeak_service` and `teamspeak_udp_9987` only when the session enables TeamSpeak;
- `disk_used_percent`, `memory_available_bytes`, and an Arma player-count placeholder for the later query adapter.

No secrets, Steam credentials, process environment, command arguments, or raw system logs are returned to DynamoDB, EventBridge, Discord, or CloudWatch metrics.

## Health classification

- `HEALTHY`: Arma service is active and UDP 2302 is bound; enabled TeamSpeak also has an active service and UDP 9987.
- `DEGRADED`: the game service is healthy but an optional voice service or non-critical resource threshold is breached.
- `UNHEALTHY`: the Arma service or UDP 2302 check fails, the host cannot be reached through Systems Manager, or a completed probe has malformed output.

The worker does not mutate lifecycle state while a session is `RUNNING`; it changes only `HealthStatus` and writes a health event. A notification is queued on a state transition to `DEGRADED` or `UNHEALTHY`, and on recovery to `HEALTHY`, so scheduled checks cannot spam Discord.

## Metrics and alarms

The worker publishes `SessionHealthy`, `SessionDegraded`, `SessionUnhealthy`, `ProbeFailures`, `ArmaPlayers`, `DiskUsedPercent`, and `MemoryAvailableBytes` in the project namespace with `Environment`, `GameType`, and `SessionID` dimensions. Terraform alarms on consecutive unhealthy observations and monitor-worker errors; alarm actions notify the existing operational channel path rather than modifying instances.

## Selective recovery

Phase 7 records evidence and alerts operators only. Phase 10 can consume those health events to introduce bounded, per-service recovery once there is an approved policy for retry count, cooldown, active-player protection, cancellation, and owner notification.
