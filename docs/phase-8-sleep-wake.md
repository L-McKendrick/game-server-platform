# Phase 8: Sleep and Wake

Phase 8 adds a safe manual sleep/wake lifecycle. It stops an already healthy
session's EC2 instance while retaining its root and data EBS volumes, then
starts the same instance and waits for the existing Arma service/UDP health
boundary before publishing the refreshed endpoint.

## Boundary

- One normal workflow lock covers the complete sleep or wake operation.
- Sleep is permitted only from `RUNNING` or `IDLE`, and transitions through
  `STOPPING` to `SLEEPING` with health `STOPPED`.
- Wake is permitted only from `SLEEPING`, and transitions through `WAKING`
  with health `STARTING` to `RUNNING` only after Systems Manager confirms the
  Arma service and UDP 2302 listener are healthy.
- After EC2 reports `running`, wake waits for the instance to report `Online`
  in Systems Manager before dispatching the service-health probe. This wait is
  bounded to 40 checks at 15-second intervals.
- A stopped instance retains both EBS volumes. No snapshot, termination,
  bootstrap, credential retrieval, or content mutation occurs in this phase.
- The session owner or a signed Discord Administrator/Manage Server member may
  request `/session sleep` or `/session wake`. The verified administrator
  capability is carried through the private command queue and applies only to
  those two implemented lifecycle workflows.
- Phase 8 registered `/session archive` as a discoverable, fail-closed command.
  Phase 9 later replaced that placeholder with guarded archive/destruction and
  restore workflows documented separately.
- The public IPv4 address may change on wake. The observed endpoint is
  refreshed from EC2 only after health succeeds.
- `/session status` uses a bounded, direct A2S `INFO` query to UDP `2303` for
  a live player count whenever a session is `RUNNING` or `IDLE`. A missing,
  malformed, or timed-out response is displayed as unavailable, never as zero.
- The status path makes a best-effort `A2S_PLAYER` query only to display names
  in that ephemeral response. Names are never persisted or logged. If a server
  does not provide a usable player response, the verified count is still shown
  and names are displayed as unavailable.

## Idle automation, server-side scripting, and Steam A2S

Arma server-side scripting is the preferred authoritative activity source. A
small, platform-owned server component should receive join/disconnect events,
periodically reconcile `allPlayers`, and atomically write only a bounded
activity record (player count, observation time, and format version) below
`/srv/game-server/state`. The monitor reads that file through its existing SSM
probe; no public query port, player identity, or player names are required.

This component must be installed and launched as a platform-owned server mod
or other server-level asset, never injected into an uploaded mission PBO.
Missions can rotate while the server is running, so mission-owned scripts are
not a reliable lifecycle control-plane boundary. The host-side record must be
owned by the service account, parsed strictly, and considered stale after a
short bounded interval. Missing, malformed, or stale data is `UNKNOWN`, never
zero players.

Steam server queries are the selected source for authoritative player
activity corroboration and an independent fallback. The status command uses
the minimal A2S `INFO` request against the dedicated Arma Steam-query UDP port
and receives the current player count. Its separately approved best-effort
`A2S_PLAYER` detail lookup is a Discord-display feature only; it is not used
for idle policy and must not retain names, scores, or Steam IDs.

Bootstrap explicitly configures Arma's `steamQueryPort=2303`; Terraform already
permits UDP `2302`-`2306`. The query adapter supports Steam challenge responses,
bounded packet sizes/timeouts, malformed packets, and a connected UDP socket
that accepts only the configured server endpoint. A query failure is `UNKNOWN`,
never zero players.

Automatic sleep requires agreement between fresh server-side activity and A2S
when both are available, several consecutive successful zero-player
observations across the configured sleep window, no active lifecycle workflow,
and an owner warning/cancellation window. Until both adapters are live, Phase
8 exposes only explicit manual sleep and wake.
