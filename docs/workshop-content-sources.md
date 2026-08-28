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

Workshop collection membership is a snapshot, not a live subscription. The
active session never changes merely because a publisher edits a collection.
Submit the link again explicitly to resolve its current membership into a new
pending revision. Review `/rb status` first if the session changed while the
link was processing.

Common recovery actions:

- unavailable/private: make the item or collection Public, verify its page can
  be opened while signed out, and submit the canonical link again;
- wrong content type: correct the scenario Data Type/gameplay tags or choose a
  collection containing eligible Arma 3 client mods;
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
The maximum 500-child collection therefore uses at most seven Steam requests:
one root lookup, one collection expansion, and five child batches. This avoids
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
at most seven public metadata calls, three S3 writes, and one transactional
metadata write per successful resolution. The material cost remains game-host
runtime and Workshop download storage/transfer during bootstrap. Per-item
download size is capped at 20 GiB and the combined client/server item count at
250; operators should still expect large collections to increase EC2 running
time, EBS use, Steam transfer time, and start/wake duration.

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
