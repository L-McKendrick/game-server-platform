# Phase 19 Steam Authorization Broker

## Decision

Managed game hosts must not hold standing permission to read or update the
shared Steam authorization cache or its DynamoDB lease. Trusted lifecycle
workers broker each authenticated Steam operation through short-lived,
single-workflow S3 exchange objects and exact-object presigned HTTPS
operations.

This design accepts one unavoidable boundary: a host performing an authorized
SteamCMD operation can read the cache while that operation is active. It does
not let that host retrieve the cache before or after the operation, update the
authoritative secret directly, or use AWS credentials to obtain another
exchange.

## Trust boundaries

- The lifecycle worker is trusted to validate the authoritative session,
  workflow lock, instance ID, lifecycle purpose, and Steam-cache lease.
- The managed host and every process or downloaded file on it are untrusted.
- SSM command text and history may contain opaque, expiring presigned URLs but
  must never contain `config.vdf`, a Steam password, a Guard code, a secret ARN
  value payload, or an unencrypted authorization envelope.
- The session-assets bucket is private, versioned, encrypted at rest, and
  TLS-only. Exchange objects are not session artifacts and must never be
  archived, presented, or copied into ordinary logs.
- Secrets Manager remains the only authoritative durable Steam cache.

## Exchange identity and storage

Every exchange has a cryptographically random 256-bit ID. Its authoritative
record binds:

- exchange ID and schema version;
- exact session ID, workflow ID, workflow type, and EC2 instance ID;
- purpose (`bootstrap`, `wake`, `restart`, `restore`, or Workshop sync);
- source Secrets Manager version ID and expected cache SHA-256;
- creation and absolute expiry timestamps;
- state (`PREPARED`, `DOWNLOADED`, `UPLOADED`, `PROMOTED`, `FAILED`, or
  `EXPIRED`);
- exact input and output object keys; and
- single-use download, upload, and promotion markers.

Objects live under a non-session namespace:

```text
platform/steam-exchanges/<exchange-id>/input.vdf
platform/steam-exchanges/<exchange-id>/output.vdf
```

The bucket lifecycle provides a one-day backstop, while the broker deletes
both objects immediately after promotion or terminal failure. Ordinary host
IAM has no permission for this namespace.

## Lifecycle

1. The worker reloads the session and proves that the supplied workflow owns
   the current lock and targets the recorded managed instance.
2. The broker conditionally acquires the existing serialized Steam-cache lease
   for that workflow. A replay owned by the same workflow resumes the recorded
   exchange; every other owner fails closed.
3. The broker reads the current Secrets Manager version, validates its existing
   schema and digest, writes it to the exact exchange input object, and creates
   exact-object presigned GET and PUT URLs. URLs use the shortest duration that
   safely covers the bounded command and never exceed the exchange expiry.
4. The worker sends URLs and non-secret exchange metadata to the exact instance
   in its SSM command. The host downloads into a root-only tmpfs/staging file,
   runs the existing username-only SteamCMD flow, and scrubs persistent and
   temporary authentication data on every exit path.
5. If SteamCMD produces a valid updated cache, the host uploads only to the
   exact output URL. It reports a bounded digest and completion marker; it does
   not print cache contents or URLs.
6. The worker observes completion, reloads the exchange and workflow, reads the
   output object with its own role, applies existing size/schema/redaction
   validation, and conditionally promotes it only if the source secret version,
   workflow owner, instance, purpose, and exchange state still match.
7. The broker deletes exchange objects, marks the exchange terminal, releases
   the owner-checked lease, and retains only bounded audit metadata and digests.

An unchanged valid cache may complete without an output promotion. A Guard
challenge preserves the existing `ERR_STEAM_REAUTH_REQUIRED` behavior and
never promotes partial output.

## Replay, concurrency, and expiry

- A workflow and purpose have at most one active exchange. Duplicate worker
  invocations return the same exchange until it becomes terminal.
- Download and upload URLs name only their exact object and HTTP operation.
  Bucket listing, deletion, arbitrary keys, and Secrets Manager access are not
  delegated.
- Promotion is a conditional state transition and can occur once. It verifies
  the source secret version so a stale exchange cannot overwrite a newer cache.
- A different workflow, session, instance, or purpose cannot resume, upload,
  promote, or clean up an exchange.
- Expired URLs and records fail closed. Reconciliation marks them expired,
  deletes any objects, releases only the matching lease, and reports a bounded
  retryable failure when the lifecycle still owns the operation.
- URL expiry must be derived from the bounded Steam operation timeout and the
  actual signing-credential lifetime. The broker must refuse an exchange when
  it cannot issue a capability long enough for that operation.

## Rollout and rollback

Rollout is fail-closed and ordered:

1. Deploy exchange storage, broker persistence, permissions, and cleanup.
2. Deploy workers and host scripts that prefer the brokered contract while the
   old host permissions remain available only as an explicit temporary rollout
   flag.
3. Exercise vanilla, modded, restart, wake, restore, Workshop, replay, timeout,
   and reauthorization paths.
4. Disable the legacy path, remove host Secrets Manager and Steam-lease
   DynamoDB permissions, and prove denial through policy tests and a live host.
5. Remove the temporary flag after the rollback window.

Rollback may restore the prior worker and narrowly scoped host policy only in
the supervised development environment. It must never copy exchange objects
into SSM command text or session storage. Cache rotation is not part of routine
rollout or rollback and is required only when evidence indicates disclosure.

## Required verification

- A host cannot call `GetSecretValue`, `PutSecretValue`, or mutate the Steam
  lease after final IAM removal.
- A presigned capability cannot access a different object or use a different
  HTTP method and fails after expiry.
- Wrong-session, wrong-workflow, wrong-instance, wrong-purpose, stale-version,
  duplicate-promotion, oversized, malformed, and missing-output cases fail
  closed.
- Logs, SSM output, workflow payloads, Lambda environment variables, archives,
  and durable session artifacts contain neither cache bytes nor Steam
  passwords/Guard codes.
- Cleanup and reconciliation remove input and output objects on success,
  failure, timeout, cancellation, and worker replay.
- Existing enrollment, rollback, lease heartbeat, and
  `ERR_STEAM_REAUTH_REQUIRED` behavior remain intact.
