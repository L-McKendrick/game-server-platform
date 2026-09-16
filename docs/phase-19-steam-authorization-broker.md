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
- source Secrets Manager version ID, cache SHA-256, username, and enrollment
  timestamp;
- creation, absolute expiry, and DynamoDB TTL timestamps;
- state (`PREPARED`, `PROMOTED`, `REAUTH_REQUIRED`, `FAILED`, or `EXPIRED`);
  and
- exact input and output object keys.

Objects live under a non-session namespace:

```text
platform/steam-exchanges/<exchange-id>/input.json
platform/steam-exchanges/<exchange-id>/output.json
```

The bucket lifecycle provides a one-day backstop, while the broker deletes
both objects immediately after promotion or terminal failure. Ordinary host
IAM has no permission for this namespace.

## Lifecycle

1. The worker reloads the session and proves that the supplied workflow owns
   the current lock and targets the recorded managed instance.
2. The broker conditionally acquires the existing serialized Steam-cache lease
   for that exact exchange ID. A replay owned by the same workflow and purpose
   resumes the recorded exchange; every other active exchange fails closed.
3. The broker reads the current Secrets Manager version, validates its existing
   schema and digest, writes it to the exact exchange input object, and creates
   exact-object presigned GET and PUT URLs. URLs use the shortest duration that
   safely covers the bounded command and never exceed the exchange expiry.
4. The worker sends URLs and non-secret exchange metadata to the exact instance
   in its SSM command. The host downloads into a root-only tmpfs/staging file,
   runs the existing username-only SteamCMD flow, and scrubs persistent and
   temporary authentication data on every exit path.
5. A successful SteamCMD process records validity through an ephemeral,
   root-owned marker beside the private Steam-owned authorization directory.
   This marker crosses isolated shell-function boundaries without granting the
   Steam user control of promotion evidence or persisting on the managed
   volume; a Guard challenge removes it. Only a marked-valid cache is uploaded
   to the exact output URL. The SSM output identifies the exchange for
   trusted-worker completion; it does not print cache contents or URLs.
6. The worker observes completion, reloads the exchange and workflow, reads the
   output object with its own role, applies size/schema/digest validation,
   preserves the enrolled username and timestamp, and conditionally promotes it
   only if the source secret version, workflow owner, instance, purpose, and
   exchange state still match.
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
  delegated. They are bearer capabilities and can be replayed against that one
  operation until they expire; promotion remains independently state-bound.
- Promotion is a conditional, replay-safe state transition. It recognizes an
  already-created Secrets Manager version after an ambiguous response and
  verifies its exact payload before completing metadata and cleanup.
- A different workflow, session, instance, or purpose cannot ask the broker to
  resume, promote, or clean up an exchange. Possession of a still-valid URL is
  sufficient only for its exact GET or PUT operation.
- Expired URLs and records fail closed. The next broker pass marks them expired,
  deletes any objects, releases only the matching exchange lease, and reports a
  bounded retryable failure when the lifecycle still owns the operation.
- URL expiry is derived from the bounded Steam operation timeout and capped at
  twelve hours. Temporary signing credentials can shorten effective validity,
  so long-running operations must treat an expired upload as a retryable
  exchange failure rather than assuming the URL outlives the command.

## Rollout and rollback

Rollout is fail-closed and ordered:

1. Deploy exchange storage, broker persistence, permissions, and cleanup.
2. Deploy the workers, host script, broker policy, and removal of the old host
   permissions atomically through one reviewed Terraform plan. The runtime
   configuration-version guard prevents a mixed old-script/new-worker rollout.
   The broker requests S3 response checksums only when required, keeping its
   presigned GET capability compatible with the host's bounded HTTPS client.
3. Exercise vanilla, modded, restart, wake, restore, Workshop, replay, timeout,
   and reauthorization paths.
4. Disable the legacy path, remove host Secrets Manager and Steam-lease
   DynamoDB permissions, and prove denial through policy tests and a live host.
5. Retain the prior reviewed Terraform revision as the development rollback;
   there is no runtime flag that can silently restore standing host access.

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
- Cleanup and reconciliation inspect per-object deletion results and retry
  terminal cleanup without releasing a different exchange's lease.
- Existing enrollment, rollback, lease heartbeat, and
  `ERR_STEAM_REAUTH_REQUIRED` behavior remain intact.
- Successful bootstrap, resumed-install, and Workshop subshell paths propagate
  authorization validity to the parent, while a nested Guard failure clears it
  before exit cleanup can upload a cache.
