# ADR 0001: Metadata Store

## Status

Accepted

## Context

The deployed control plane will use AWS Lambda and other event-driven
services. Platform metadata must survive Lambda execution environments and
must remain available after EC2 and EBS game-server resources are destroyed.

SQLite stores data in a local file. Lambda local writable storage is temporary
and cannot act as the shared durable metadata database for the control plane.

## Decision

The deployed AWS environment will use DynamoDB as its metadata store.

Application code will access metadata through interfaces defined under
`internal/ports`. This prevents business logic from depending directly on
DynamoDB and allows a local or test implementation to be added later.

SQLite may be used for local development or isolated tests, but it will not be
the authoritative deployed metadata store.

## Consequences

- The production control plane remains serverless.
- Metadata persists independently of Lambda and game-server infrastructure.
- Data models must be designed around DynamoDB access patterns.
- Storage implementations can be replaced without rewriting lifecycle logic.

## Initial Access Patterns

The first implemented access patterns are:

1. Create a session and its initial event atomically.
2. Retrieve one session by session ID.
3. Update a session using optimistic concurrency and append an event atomically.
4. List recent sessions by Discord owner ID.

Primary records use:

- `pk = SESSION#<session-id>`
- `sk = METADATA`

Events use:

- `pk = SESSION#<session-id>`
- `sk = EVENT#<timestamp>#<event-id>`

Owner queries use:

- `gsi1pk = OWNER#<discord-user-id>`
- `gsi1sk = UPDATED#<timestamp>#SESSION#<session-id>`

Guild/state discovery uses a sparse session-metadata-only index:

- `gsi2pk = GUILD#<guild-id>`
- `gsi2sk = STATE#<lifecycle-state>#UPDATED#<timestamp>#SESSION#<session-id>`

The guild/state index requires explicit lifecycle states and independent
pagination of each matching range. Existing rows are conditionally backfilled
and exhaustively verified before an explicit cutover marker enables reads.

Global-secondary-index results are used only for candidate discovery and
listing. Authorization, eligibility, and state changes strongly reread the
primary session item; mutations remain conditional on its authoritative
version and state.
