# ADR 0003: Command Idempotency

## Status

Accepted

## Context

Discord and other external systems can retry requests. Network timeouts can also
leave a caller uncertain whether a metadata mutation succeeded. Reprocessing the
same command must not create duplicate sessions, duplicate lifecycle events, or
later duplicate infrastructure.

## Decision

Every mutating application command requires an external idempotency key.

The application hashes a canonical representation of the command. The metadata
repository stores a completed idempotency record in the same DynamoDB transaction
as the session and event mutation.

Records use:

- `pk = IDEMPOTENCY#<idempotency-key>`
- `sk = RESULT`

The record contains the request hash, status, timestamps, result reference, and
TTL expiration.

Behavior is:

1. The first command writes its session mutation, event, and idempotency result
   atomically.
2. A retry with the same key and request hash returns the recorded session
   reference without writing another event.
3. Reusing a key with a different request hash returns an idempotency conflict.
4. DynamoDB TTL removes old records after the configured retention period.

Synchronous metadata commands write completed records directly. Long-running
workflow commands may add pending records later so concurrent processing can be
claimed before external side effects begin.

## Consequences

- Session creation and lifecycle transitions are safe to retry.
- Session/event atomicity is preserved.
- Callers must provide stable idempotency keys.
- The metadata repository owns the transaction boundary for sessions, events,
  and command results.
- Future Discord handlers should use the Discord interaction ID when building
  the idempotency key.
