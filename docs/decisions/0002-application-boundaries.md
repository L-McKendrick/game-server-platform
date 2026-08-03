# ADR 0002: Application and Adapter Boundaries

## Status

Accepted

## Context

The platform will receive commands from Discord and will persist metadata in
DynamoDB. Business rules must not depend directly on Discord SDK types or AWS
SDK clients.

Embedding lifecycle and authorization logic directly in Discord handlers would
make the rules difficult to test and would couple the platform to one user
interface.

## Decision

The codebase uses the following boundaries:

- `internal/domain` contains session entities, lifecycle rules, actors, events,
  and domain errors.
- `internal/app` contains application commands and queries.
- `internal/ports` contains interfaces required by application services.
- `internal/adapters` contains implementations for DynamoDB, Discord, memory,
  and other external systems.
- `cmd` contains composition roots that connect implementations.

The session application service currently implements:

- Create session
- Get session
- List owner sessions
- Transition session

The service performs owner authorization before returning or modifying a
session.

Unit tests use an in-memory repository. The AWS integration smoke test uses the
same application service with the DynamoDB repository implementation.

## Consequences

- Business logic is testable without AWS or Discord.
- Discord handlers remain small transport adapters.
- DynamoDB can be replaced in tests without changing application behavior.
- Authorization rules have one application-level implementation.
- Future interfaces can reuse the same application service.