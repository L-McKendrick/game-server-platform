# Steam Workshop Content Sources

## User workflow

Arma 3 missions and client mods may be supplied with a canonical public Steam
Community shared-file link. The link may identify one item or one collection.
Uploaded mission files and Launcher presets remain supported and use the same
authoritative content records after validation.

For scenarios, each accepted item must be an Arma 3 item with Data Type
`Scenario` and a gameplay tag of `Multiplayer` or `Coop`. For client mods, each
accepted item must classify as a public Arma 3 client mod. Server-only items are
reported separately and are not added to the client modlist.

Steam commonly delivers a scenario as a numeric `*_legacy.bin` file even
though its public metadata supplies the canonical `.terrain.pbo` filename.
The platform records that decoded, normalized filename and expected size in
the immutable resolution, accepts exactly one regular `.pbo` or numeric legacy
payload, and stages it under the recorded `.pbo` name. A size change requires
the Workshop link to be resubmitted; records accepted before canonical
filename support must also be resubmitted rather than guessed or re-resolved.

A mixed collection does not fail merely because some children are ineligible.
The completion message reports accepted and excluded counts and a bounded list
of excluded item IDs and classifications. A collection with no eligible items
is rejected with instructions describing the required type and tags.

A collection may contain at most 50 direct children. The platform rejects a
larger collection before requesting child metadata or starting SteamCMD and
rechecks the same bound when persisting and deploying the immutable snapshot.
Split a larger collection into smaller collections and submit the applicable
links separately. Nested collections remain unsupported.

Workshop collection membership is a snapshot, not a live subscription. The
active session never changes merely because a publisher edits a collection.
Submit the link again explicitly to resolve its current membership into a new
pending revision. Review `/rb status` first if the session changed while the
link was processing.

Workshop source edits are accepted in `DRAFT`, `NEW`, `READY`, `RUNNING`,
`IDLE`, `SLEEPING`, warning, and recoverable `FAILED` states when no workflow
holds the session lock. `DRAFT` and `NEW` content is consumed by initial
bootstrap; sleeping or warning-state content is retained for the next wake;
stable running/idle host synchronization is introduced by Phase 17.9. Edits
are rejected during validating, provisioning, bootstrap/install, stop, wake,
archive, restore, deletion, or any active workflow. Archived edits are also
rejected because changing session metadata after archive creation would make
the restore manifest inconsistent. A request whose session changes state while
Steam metadata is being resolved fails closed and must be resubmitted from the
new stable state.

Common recovery actions:

- unavailable/private: make the item or collection Public, verify its page can
  be opened while signed out, and submit the canonical link again;
- wrong content type: correct the scenario Data Type/gameplay tags or choose a
  collection containing eligible Arma 3 client mods;
- collection too large: split it into collections of at most 50 direct items;
- legacy scenario record or changed scenario payload: resubmit the Workshop
  mission link, then retry the failed operation;
- session changed or busy: wait for the active lifecycle operation to finish,
  review `/rb status`, and resubmit;
- temporary Steam failure: allow the bounded automatic retries to finish, then
  resubmit after a few minutes if the final notice says they were exhausted;
- Steam authorization required during host download: ask an operator to follow
  `docs/steam-auth-cache.md`; never send a password or Guard code in Discord.

## Architecture, cost, and performance

Resolution runs in the existing artifact worker and FIFO queue. Messages are
serialized per session, so a Workshop request cannot race another artifact
mutation for the same session; other sessions and guild configuration groups
remain independent. The active-revision value captured at submission is
revalidated before any artifact write, preventing a slow response from
overwriting a newer preset.

Collection metadata uses bounded batches of at most 100 published-file IDs.
The maximum 50-child collection therefore uses three Steam requests: one root
lookup, one collection expansion, and one child batch. This avoids
the latency, rate-limit exposure, and Lambda cost of one request per child.
The worker has a 90-second timeout and its FIFO queue has a six-times timeout
visibility window. Lambda billing remains based on actual execution duration;
ordinary uploads do not perform Steam calls and retain their existing code path.

Each accepted mod resolution writes three small content-addressed S3 objects:
the internal preset, public modlist, and immutable source manifest. It appends
a bounded DynamoDB source snapshot and preset revision. No database, cache,
NAT Gateway, new worker, or scheduled service is introduced. Existing S3
lifecycle and session-prefix termination cleanup cover the new objects.

Actual cost impact is expected to be small: short artifact-worker execution,
at most three public metadata calls, three S3 writes, and one transactional
metadata write per successful resolution. The material cost remains game-host
runtime and Workshop download storage/transfer during bootstrap. Per-item
download size is capped at 20 GiB and each collection at 50 direct children;
operators should still expect large collections to increase EC2 running
time, EBS use, Steam transfer time, and start/wake duration.

Host downloads use one target-aware `sync_workshop_content` implementation for
initial bootstrap, wake/restore application, and the live-sync command mode.
The mode accepts `all`, `missions`, or `mods`, retains item-scoped transient
retries, serializes through the host and Steam authorization locks, checks free
space before downloading, and writes a bounded workflow result manifest under
the existing session S3 prefix. The manifest contains only session/workflow
identity, target, immutable revision, item IDs, and success state; raw Steam
output and host paths are never published.

Each operation redirects SteamCMD to
`/srv/game-server/workshop-staging/<workflow-id>` instead of the library used by
active server links. Scenario payloads are validated and copied atomically into
`mpmissions` without changing the configured or current mission. Validated mod
trees are copied into client/server revision-owned directories. Bootstrap,
wake, and restore may promote those exact directories through the established
revision apply path. Live `workshop_sync` mode sets stage-only behavior, so it
does not change `@workshop_*` links, active preset compatibility links, mod
argument files, launch arguments, or the running service. Wake-time promotion
is different: after it switches the revision-owned links, the same bounded host
command restarts and verifies Arma before the workflow performs its external
health check and promotes the metadata revision.

Live synchronization is represented by a durable `WorkshopContentSync`
workflow and the existing exclusive session lease. The artifact worker sends
one SSM command and stores its command ID and deadline. A single EventBridge
rule delivers terminal `AWS-RunShellScript` changes to that worker; it resolves
the SSM command and accepts only the exact
`gsp:workshop-sync:<session-id>:<workflow-id>` comment and recorded instance.
Unrelated terminal commands are ignored. The existing 15-minute reliability
scan observes unfinished sync workflows as the missed-event fallback.
If interruption occurs after SSM accepts a command but before its ID is stored,
the callback or scheduled scan recovers it only from the exact platform comment
and recorded instance. A failed durable write cancels the command before the
session lock is released.

This adds one EventBridge rule plus low-volume Lambda and SSM lookup calls. It
does not add a Step Functions execution, polling transitions, Lambda function,
queue, table, bucket, GSI, or schedule. Wake renames its existing mod stage to
`DispatchContent` and retains the same state count and 30-second polling
cadence, while also synchronizing missions when no mod revision is pending.
EventBridge cannot filter these notifications by the platform-owned comment,
so each terminal `AWS-RunShellScript` command causes one artifact-worker
invocation and bounded `ListCommands` lookup. Commands without the exact
session/workflow comment and recorded instance are ignored without a session
write. This small variable cost replaces continuous polling and another worker.

`/rb create` never starts host work; accepted sources remain queued for initial
bootstrap. `/rb edit` starts a live stage-only sync only while the session is
stably running or idle. Sleeping and pre-runtime sessions show that content is
queued for the next wake or start, while lifecycle transitions and active
workflows reject the edit. A persisted request marker shows metadata as
resolving until the worker either records the immutable source or publishes a
terminal rejection. The private detailed status distinguishes resolving, queued,
downloading and validating, available, awaiting restart, and action-required
states and includes a bounded ID/class summary for excluded collection children.

The live workflow persists its exact target, resolution digest, instance ID,
SSM command ID, and deadline. Replays must match the same digest and requester;
callbacks must also match the active workflow, current instance, and current
immutable snapshot. The host lock serializes commands on each managed host.
Expired commands are cancelled before releasing their session lock. Scenario
size and mod publisher timestamps reject content changed after resolution.

After host validation and staging complete, the managed game instance writes a
bounded workflow result JSON under
`sessions/<session-id>/workshop-sync/<workflow-id>.json`. Its IAM permission is
limited to `PutObject` on that JSON prefix. If publication fails, the command
returns `ERR_WORKSHOP_RESULT_PUBLISH`; status directs the user to an operator
because retrying before storage access is repaired cannot safely finalize the
revision.
Current staging and temporary manifests are removed on every exit, and
constrained cleanup removes abandoned workflow staging older than one day.

Safe failure codes provide distinct remedies for collection size, disk space,
private/restricted items, removed children, metadata drift, Steam
reauthorization, bounded timeout, and an individual item download failure. Raw
Steam output, credentials, filesystem paths, and untrusted titles remain out of
workflow and Discord failure projections.

## Failure and operational behavior

Metadata rate limits, transient Steam responses, malformed responses, S3
failures, and DynamoDB failures are retryable. Policy, ownership, lifecycle,
stale-revision, and idempotency conflicts fail permanently. On the final
bounded retry, the worker sends a sanitized actionable notice instead of
silently allowing the message to disappear into the dead-letter queue.

Generated S3 keys are content addressed. A failure between object writes and
the atomic metadata transaction can leave an unreferenced object under the
session prefix, but cannot change active session authority; normal termination
cleanup removes it. Pending revisions are installed only by the established
start, wake, or restore workflow, promoted only after health verification, and
retained with rollback evidence on failure.

CloudWatch logs contain stable error codes, target, source kind, counts,
revision number/status, session ID, and correlation ID. They do not contain raw
Steam responses, Workshop titles, credentials, Guard state, or downloaded
paths. Existing queue alarms and DLQ inspection procedures remain applicable.
