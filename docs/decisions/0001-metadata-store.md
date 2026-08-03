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
`internal/store`. This prevents business logic from depending directly on
DynamoDB and allows a local or test implementation to be added later.

SQLite may be used for local development or isolated tests, but it will not be
the authoritative deployed metadata store.

## Consequences

- The production control plane remains serverless.
- Metadata persists independently of Lambda and game-server infrastructure.
- Data models must be designed around DynamoDB access patterns.
- Storage implementations can be replaced without rewriting lifecycle logic.