# Phase 19.3 host object access contract

Status: Phase 19.3 and SEC-20-01 are complete for controlled beta admission.
Source, deployment, live isolation/lifecycle acceptance, cleanup, and the Ubuntu
CI race gate passed on 2026-09-18. The detailed review context lives outside the
repository, as linked in CURRENT_WORK.md.

## Boundary and minimal architecture

Close SEC-20-01 by removing BOTH managed-instance S3 inline policies while
retaining reviewed SSM access. Trusted existing workers issue exact-object
capabilities after strongly reading current session/workflow authority and
checking Project, Environment, SessionId and exact recorded instance identity.
No new public endpoint, queue, always-on service, IAM role per session, or
general capability database is required.

Reuse existing workflow/command records for operation ownership, snapshot,
attempt identity, latest reference generation/expiry and consumed output
evidence. Add fields only as required by dispatch recovery and conditional
completion. A live mission copy keeps its existing eligibility contract; derive
its operation identity from the accepted object and current instance without
requiring a lifecycle lock. Its short command needs no continuous refresh loop.

`domain.HostAccessScope` binds a session/guild, existing operation, one bounded
attempt, exact instance, immutable content snapshot and absolute deadline.
`domain.HostObjectRequest` defines a worker-selected slot/key, purpose, byte
bounds, trusted digest and optional accepted version. Its validation checks
structural isolation, not authoritative object acceptance. `app.HostObjectIssuer`
checks exact accepted references and fixed output purposes against current records;
`HostAccessAuthority` additionally requires exact running-instance tag verification.
Progress updates do not invalidate a content snapshot merely by incrementing
the session version. Cancellation, content revision changes, lock loss, or a
replacement instance stop issuance/refresh and prevent result acceptance.

Task 19.3.2 implements shared issuance and `HostManifestIssuer` recovery. A small
`HOSTACCESS#<operation>#<attempt>` record in the existing session partition is necessary
because current whole-workflow replacement writes cannot safely preserve concurrent
refresh state. Atomic revision CAS also checks session version/identity/lock and
running uncancelled workflow deadlines. It stores scope, manifest version/digest/size,
generation, expiry and dispatch state, never URLs. Choose whole-second attempt
deadlines; comparisons against legacy variable-precision timestamps conservatively
round fractional deadlines up. Scope equality compares instants across JSON recovery.
Live copy uses one accepted immutable read within the existing 150-second delivery/
execution window, without a fake workflow or unnecessary recovery record.
Expired-attempt cleanup reuses the existing version-aware cleaner after persisted
ownership checks. Retain recovery metadata for repeat sweeps: an already-started
upload may finish late. Runtime migrations, output byte verification/promotion and
cleanup scheduling/lifecycle retention remain tasks 19.3.3–19.3.6.

`ports.HostObjectSigner` is an internal transport port, not an authorization
service. Its ephemeral capability includes method, URL, all signed headers,
all POST fields, and actual expiry. Do not discard required headers or expose
this port to arbitrary host requests.

Issuance is state/session/instance-bound; S3 requests are bearer operations.
S3 does not consult the workflow record or verify the requesting EC2 instance.
Exact URLs are reusable until expiry. No single-use GET or non-transferability
is claimed. Phase 19.1 continues to mediate Steam separately; the cache is
temporarily visible on a host during an authorized Steam operation.

## Inventory and purpose contracts

All listed keys are selected from trusted references, never caller prefixes.

| Purpose / current caller | Target operation | Acceptance rule |
| --- | --- | --- |
| Deployed script / `ssmbootstrap` | GET exact configured `platform/bootstrap/arma3-*.sh` revision | Worker supplies full deployed SHA-256; host verifies before execution |
| Accepted missions / bootstrap, wake, restart, restore bootstrap, `ssmlivemission` | GET exact accepted session input, optional pinned version | Trusted digest and bounded download; live copy rechecks current instance/eligibility |
| Client/server presets / bootstrap shell | GET exact active/applying/rollback revision | Authorize current revision; legacy missing digests may be derived by trusted verification during migration |
| Guild config / bootstrap shell | GET session-snapshotted `guilds/<guild>/server-config/revisions/.../server.cfg` | Exact referenced revision and SHA-256, maximum existing 64 KiB |
| Restore archive / `ssmrestore` | GET exact verified archive belonging to session | Existing SHA-256/size, maximum existing 4 GiB compressed; retain mount/device, extraction and health checks |
| Archive / `ssmarchive` | PUT unique `sessions/<id>/archives/<workflow>/session.tar.gz` | Signed exact length and SHA-256, signed `If-None-Match: *`; trusted HEAD verification and manifest before cleanup |
| Workshop mission / bootstrap shell | POST exact per-item attempt slot | Enforced byte range within existing 100-MiB item limit; use resolved exact size where available; trusted byte/digest, filename, provenance and revision validation before durable promotion |
| Resolution and sync result / bootstrap shell | POST exact separate attempt slots | Maximum 256 KiB each; verify schema/complete expected item set, snapshot and S3 source version before promotion |
| Runtime progress / shell uploader | POST exact mutable attempt slot | Maximum existing 16 KiB; advisory only, reject stale/terminal attempts; retain coalescing and bounded uploader shutdown |
| Diagnostics / bootstrap, archive, restore SSM output | POST exact bounded attempt slot | Maximum 1 MiB per selected diagnostic tail; preserve compact sanitized SSM results and useful failure evidence |
| Capability manifest / trusted worker | GET exact temporary manifest version | Maximum 4 MiB, worker-supplied exact length/digest; not an accepted session asset |

Staging layout: `sessions/<id>/runtime/host-access/<operation>/<attempt>/<slot>`.

19.3.4 uses finite `mission-<approved-id>`, `workshop-resolution` (TSV) and
`workshop-result` (JSON) slots. Known resolved mission sizes become exact POST
byte bounds; controls remain bounded to 256 KiB. Successful owned-command
observation validates the full staged set against current authority, approved
deployment metadata and independently hashed PBO bytes before atomically pinning
source versions in the existing attempt record. Publication streams those exact
versions using checksum-enforced create-only writes; retries cannot adopt later
staging writes or replace pinned evidence. Existing destinations require matching
independently checked bytes, length and type.

Durable resolution TSVs are canonical sorted approved-item documents assembled
from verified new rows and current trusted materialization/provenance records.
Finalizers select pending intent only, preserving removals. Normal result JSON
retains `<workflow>.json`; rollback uses `<workflow>-rollback-<attempt>.json`
within the same `workshop-sync` prefix, preventing conflicting immutable reports
from two command modes sharing a workflow. Sync checkpoints include workflow ID;
no-download restarts neither synchronize nor publish Workshop outputs. Quiesced
19.3.7 cutover must explicitly reconcile legacy differing TSV versions without
silently overwriting accepted output. Offline tests do not prove live IAM denial.
The worker fixes the finite slot set. A host does not invent a digest-key prefix,
write an accepted mission/manifest directly, or obtain a durable asset overwrite
capability. Existing ordinary host AWS-CLI S3 calls and automatic SSM OutputS3
delivery must all disappear before final IAM removal. EC2 user data currently
has no S3 requirement and receives no capability credentials.

## Signing and publication decisions

Task 19.3.3 wires bootstrap, wake, restart, restore bootstrap and live mission copy
to trusted exact-read delivery. Workers require the full `BOOTSTRAP_SCRIPT_SHA256`
from Terraform; the root-only HTTPS helper verifies it before script execution.
Accepted inventories and trusted command context stay in encrypted, versioned
temporary manifests. Legacy mission/preset bytes are bounded, hashed by the worker
and version-pinned once; command retries recover those references and the original
Steam exchange context. Rollback has its own immutable attempt/deadline and must
own the persisted rolling-back stage and applying revision.

Existing active-command observations renew within five minutes of expiry. Prepared
generations recover through an exact-instance SSM refresh command; publication
does not count as installed until that command succeeds. The helper replaces
manifests atomically under a generation lock. An expired lifecycle read waits at
most 180 seconds within the original deadline for a newer generation, then retries
the same digest/version once. Other failed reads and short live copies fail promptly.
Batch issuance revalidates authority around publication rather than performing
EC2 tag inspection for every object. No deployment is safe until output migration,
trusted-worker IAM and runtime cutover in 19.3.4–7 are complete; the 19.3.8 live
long-operation and beta-admission gate has also passed.

- READ uses header-free S3 GET (`X-Amz-SignedHeaders=host`), optionally with an
  accepted versionId. Disable optional checksum-request middleware; the host
  checks the separately trusted digest/length. This preserves the 19.1 fix.
- CREATE uses S3 PUT with exact ContentLength, explicit gzip ContentType,
  ChecksumSHA256 and IfNoneMatch. The pinned SDK signs
  `content-length;content-type;host;if-none-match;x-amz-checksum-sha256`.
  Send all returned required headers. The SDK removes a default content type
  but preserves/signs an explicitly provided one; use the complete tested
  transport data. Do not interpret an ETag as SHA-256.
- STAGE uses exact-key S3 POST with a content-length-range condition and exact
  Content-Type; add every matching form field explicitly. The SDK does not
  automatically return custom condition fields. Do not use a key starts-with
  policy or assume POST supports create-only conditional writes.
- Stage objects can change until their capability expires. Verification and
  promotion must use the SAME S3 source version; record consumed output
  evidence conditionally and make durable publication immutable. A later
  staged write cannot change accepted metadata or durable bytes. Validate
  Workshop payloads once at acceptance, not again on every status request.
- Archive preparation reports bounded length/digest while retaining the local
  file for the current attempt. The existing worker observes preparation,
  issues the constrained upload, and verifies the object. This requires a
  bounded prepare/upload handoff but avoids a mandatory second multi-GiB
  copy and avoids streaming the whole archive through Lambda. Ambiguous PUT
  and HTTP 412 mean verify existing exact bytes, not overwrite/recreate them.
- Conditional PUT prevents overwriting a current object, not recreation after
  a delete marker. Keep consumed archive/staging evidence until issuance
  expires, stop refresh on termination, ignore late writes, and perform
  expiry-aware final version cleanup. Existing guarded session-prefix cleanup
  remains the destructive authority. TTL/lifecycle are retention backstops.

## Bounded SSM delivery and credential expiry

Use one temporary encrypted manifest object for each reference generation, not
inline thousands of presigned URLs. Its short-lived version-pinned GET URL,
digest, exact byte length, scope and generation travel in
`ports.HostAccessReference`. This is temporary privileged exchange storage,
like the Steam exchange, not durable session content. Never store raw
capabilities in DynamoDB workflow records, events, ordinary logs, accepted
assets or archives. Delete all temporary manifest versions after capability
expiry and merge retention rules into the existing bucket lifecycle resource.

The codecs reject unknown schema, duplicate slots, conflicting HTTP methods,
invalid/expired windows, unsafe URL syntax and oversized envelopes. Limits:
manifest 4 MiB, reference JSON 8 KiB, generated SSM command 24 KiB. The encoded
reference leaves room for a bounded fetch/verification stub; no full manifest
is included in the command. AWS documents a 64-KiB document limit. If limits
are exceeded, fail with a bounded actionable error; never truncate the list or
fall back to standing credentials. Consumer tasks must enforce the actual
generated command size and bounded root-only download. Formatting helpers
redact URLs/headers/forms, but JSON encoding intentionally carries credentials:
never log encoded envelopes.

Default capability lifetime is 15 minutes. Clip it to the earliest of the
command/workflow deadline and actual signer credential expiry minus one minute;
truncate seconds downward. Refuse a remaining safe window below one minute.
Retrieve/freeze the same credential snapshot for signing and calculating expiry;
an expiring provider must report its actual expiry. These values are not a
promise that a six-hour bootstrap can reuse its first upload capability.

Existing observation/reliability passes refresh only a live, owned, same-content
attempt when expiry is within five minutes. Regeneration must provide a useful
later window; skip duplicate/no-improvement dispatch and never extend the
absolute operation deadline. Install refreshed references atomically under a
root-only `/run` directory using a generation check so delayed SSM commands
cannot replace a newer manifest. The host fetches the current bounded manifest
when needed and retries an expired transfer only after a refreshed reference
is available. It never calls an arbitrary-key signing endpoint.

Reuse bootstrap's 30/120-second observation and content reliability's existing
five-minute cadence; do not schedule a new heartbeat or database write for each
30-second progress snapshot. Inputs/terminal uploads should be issued near their
transfer stage. Archive and short live-copy commands use bounded stages without
a background refresh daemon. If the current credential window cannot cover a
transfer, fail/retry through existing retained-resource paths; preserve truthful
lock, capacity and resource state. Full live long-operation renewal remains
19.3.8 acceptance, not a claim established by offline tests.

## Evidence and next task

19.3.1 tests cover scoped key/bounds rejection and expiry clipping, 1,000-object
manifest transport, oversized/contradictory envelopes and diagnostic redaction.
Pinned-SDK tests generate real signatures offline and independently recompute
SigV4: changing method, object, length, type, checksum, create-only header or expiry
invalidates the original archive signature. They decode the actual POST policy
to prove exact key and byte range are signed. These are contract tests, not AWS
denial or deployment acceptance. Host broad IAM removal is implemented in
19.3.7 source; deployed grants have not been inspected or changed.

19.3.5 archive preparation/upload now uses existing SSM runner observations.
An owned preparation command retains a root-only compressed file after service
restart/health acceptance; its actual canonical SHA-256 and <=4-GiB size become
the immutable exact PUT inventory in the existing manifest. The host streams
one create-only upload to the workflow archive key. Trusted HEAD, authorized
before inspection and revalidated afterward, checks whole-object checksum,
gzip, exact size and non-null version against preparation before archive success.
The archive service repeats this verification before completing the trusted
manifest or destroying resources. Hosts receive neither HEAD nor replay authority.

Restore derives only the accepted own-session archive and pins the HEAD-verified
version in the temporary manifest. Dispatch replay and observation-driven renewal
preserve that version, checksum, size and original deadline. HTTPS transfer retains
exact-volume mounting and existing extraction/expansion/auth scrub safeguards.
Archive transfers allow at most 15 minutes, clipped to capability expiry and the
original operation deadline, with one bounded same-object expiry retry. Shared
owned-command recovery/generation delivery replaces duplicated refresh orchestration.
Failed uploads retain resources/local preparation; successful PUT removes the local
file, and subsequent preparation cleans old root-owned attempts under the archive lock.
No whole archive is buffered in Lambda, and no staging/promotion copy is required.

19.3.6 progress uses a single exact `progress` attempt slot (text/plain,
<=16 KiB). Strong owned-command scope checks select its read key. It remains
advisory and is excluded from immutable Workshop output sets and acceptance.
Command inventory recovery preserves the slot during observation-driven renewal;
the sampler still closes inherited locks and terminates its child uploader promptly.

SSM OutputS3 is retired across bootstrap, archive and restore. Diagnostics use
bounded native invocation output with the existing exact command/instance identity
and sanitized failure tails. Full S3 command transcripts are intentionally retired;
no new diagnostic queue, uploader daemon or logging service is introduced.

Staging POST policies sign the mandatory `gsp-retention=host-access` tag; private
encrypted manifests use that tag too. The bucket expires current/noncurrent tagged
versions after three days and removes empty session delete markers separately.
Published content, accepted inputs/configuration and durable archives remain untagged.
Trusted issuer `PutObjectTagging` grants must join the atomic 19.3.7 cutover.
Existing reliability/termination passes query one strong session partition and
recheck persisted scope ownership/expiry before sweeping every staging version.
Metadata stays to prevent replay/resurrection and allow repeated late-write sweeps.
Routine S3 sweeps stop after four days to bound historical empty-prefix costs;
lifecycle tagging is the backstop for older or arbitrarily late in-flight uploads.
Cleanup failures stay visible while workflow reconciliation continues. Existing
termination/reset all-version deletion and reset runtime-drift refusal are retained.
Late staging writes cannot enter accepted output sets after ownership/expiry closes.

Next: deploy the reviewed acceptance fixes, retry archive/restore and verify final
cleanup before an accountable beta admission decision.

## 19.3.8 live evidence and review fixes (2026-09-18)

The deployed source through `c38de4b` matched all seven worker package checks and
six issuer runtime/script identities. Test-67 (`01M2TWDA417PVWX6T85PJT3XVD`) is a
modded TeamSpeak session. Its bootstrap succeeded after more than 15 minutes with
three manifest generations; renewal commands succeeded, progress was tagged and
native SSM output had no S3 transcript bucket. Arma/voice health, sleep, wake and
restart passed. Accepted disposable mission copy and existing-content mod sync
also passed through production adapters using operator credentials; those helper
results do not independently establish effective final Lambda permissions.

Actual original-host IMDS credentials passed a positive STS identity check and
failed S3 read/write probes against populated same-guild and synthetic
second-guild sessions and unrelated config/archive/progress/result/log/script/
Steam-exchange namespaces. Steam-cache and Discord secret reads and Steam-lease
read/write probes were denied. Both unprovisioned isolation fixtures were deleted.
Actual S3 capability tests passed exact key/version, expiry, POST size/type/tag,
PUT length/type/checksum/create-only and replay constraints (19 checks). Probe
objects belong only to test-67 and remain subject to final all-version cleanup.

Live testing and branch review found five bounded corrections:

- The reliability deployment verifier requires its actual ARN-based queue
  configuration rather than a notification queue URL it never receives.
- Terminal bootstrap observation finalizes the Steam exchange even when trusted
  Workshop publication fails, preserving both errors and refusing success.
- The active/pending client preset DynamoDB codec persists Workshop source ID and
  resolution digest. Legacy records recover only a unique exact preset-key match
  in their trusted source records; ambiguity or partial explicit identity fails.
  Explicit identities and ordinary presets are preserved. Upgrade every metadata
  writer while quiesced because older writers drop these fields on session saves.
- Transactional authority compares a conservative whole-second deadline ceiling
  formatted with nine zero fractional digits. This preserves legacy fractional
  lease ordering without authorizing a lease shorter than the real deadline.
- Archive preparation keeps its Bash shebang before the ownership preamble. The
  initial live archive failed under `sh` at `pipefail`; the normal failure
  finalizer released the lock and retained compute/data without publishing an
  archive or destroying resources. A successful Step Functions failure-finalizer
  execution is not successful archive acceptance.
- Test-69 then exposed an avoidable pre-upload inspection: checksum-enabled HEAD
  of the deliberately absent create-only archive key returns 403 when the worker
  correctly lacks ListBucket. The runner now discovers an exact owned upload
  command first, dispatches a new bounded upload without preflight HEAD, observes
  pending/failed commands without inspection, and verifies checksum, size, type
  and immutable version only after owned upload success. Recovery no longer mints
  another manifest generation. No bucket-list permission was added.
- After that archive fix was applied, test-70 exposed a separate resumed-bootstrap
  edge. Test-69's successful recovery command had no remaining SteamCMD work, so
  it returned no Steam exchange output. Broker completion failed its output read
  but retained the six-hour serialized lease; test-70 provisioned its instance
  and data volume, then failed before SSM dispatch while preparing a new exchange.
  The exact stale exchange was conditionally marked failed, its sole temporary
  object version removed, and only its owner-matched lease released. The host now
  returns the unchanged checksum-verified input after successful no-download
  resumes, while a still-missing or unreadable terminal output fails cleanup and
  releases that exact lease. This adds no service, permission, or persistence.

Go 1.26.5 coverage tests, vet, builds, focused regressions and all Lambda packages
pass; Terraform 1.15.8 fmt/validation, deployment-verifier fixtures, and the
required Ubuntu CI race gate pass.
The review fixes, test-69 archive correction and functional test-70 Steam-exchange
correction are deployed. A later invocation of the already-applied saved plan was
correctly rejected as stale. Five broker consumers match the local release and all
six workers carry the corrected content-addressed bootstrap script. The earlier
minimal plan pinned the unchanged archive binary, so its embedded revision did
not match the coherent local package. The final narrow plan updated that package;
the dependent workflow policy was already identical. All six affected package and
runtime verifiers now pass.

Test-71 subsequently completed vanilla + TeamSpeak bootstrap, a verified
checksum-bearing archive, guarded deletion of its original instance and data
volume, and replacement-host restore to `RUNNING`/`HEALTHY`. Both test-71 Steam
exchanges reached `PROMOTED`; the cache is `ACTIVE` without a lease. Alongside
test-67, this supplies the missing coherent-revision live archive/restore evidence.
The archive worker and durable workflow correctly recorded success, but the
ArchiveSession Step Functions execution falsely ended `FAILED` because the
Terraform definition routed its successful `Complete` task directly into a Fail
state. The minimal correction makes `Complete` terminal and adds a contract
regression test. It is deployed and verified. Test-71 termination succeeded with
no tagged EC2/EBS resource or session-owned S3 version/delete marker remaining.
The platform owner approved completion and beta preparation, closing 19.3.8 and
SEC-20-01 for controlled beta admission.

These corrections add no service, table, index, daemon or runtime dependency.
Legacy recovery uses existing in-memory records; no migration scan or extra
request per read is added. Capability renewal adds bounded control-plane requests,
while transfers stream directly between hosts and S3. Attempt metadata remains
for replay safety; historical partition growth should be measured before adding
storage/index machinery. Reusable bearer URLs remain valid until expiry and do
not intrinsically bind a request to the original host or enforce single use.

## Quiesced IAM/runtime cutover and rollback

The following procedure remains required for future boundary changes and
rollbacks. Stop new lifecycle/content requests and Steam exchanges, drain or
explicitly finalize existing executions and owned SSM commands, and wait out
previously issued capabilities and credential lifetimes before changing the
runtime boundary.
Do not assume revoking an issuer policy instantly stops an already-started transfer.
Inventory retained hosts and resources before the maintenance window.

Package the final revision, create a fresh Terraform plan, and review its complete
runtime/script, issuer/tagging grants, lifecycle and both host inline-policy
removals together. Apply only that exact saved plan after separate approval.
All six issuers require BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION set to
scoped-host-access-v1; explicit Lambda dependencies ensure their scoped policy
resources are present, but do not establish IAM propagation. Do not deploy a
consumer, signer or host-policy removal alone. Discord definitions are unchanged.

Retained instances keep the shared profile and lose standing asset access with
the role policy update. Verify the effective instance profile, all attached and
inline policies, bucket/access-point grants and organizational boundaries live;
then use the host's actual credentials to test cross-session/guild asset denial
and SSM connectivity after propagation. The source retains only
AmazonSSMManagedInstanceCore on the host role. That managed policy includes broad
Parameter Store reads; do not store platform secrets there. Native bounded SSM
invocation output replaces S3 command transcripts; no transcript bucket grant
should be reintroduced.

Restart retained-host operations through trusted workers to obtain new scoped
manifests; do not resume legacy in-flight commands. Preserve accepted input and
archive identities and existing attempt ownership metadata. Canonical resolution
TSVs may differ from legacy bytes: inventory those versions and reconcile accepted
metadata explicitly while quiesced, rather than overwriting a create-only durable
key or treating conflicting bytes as verified. Expired staging is swept through
existing maintenance and tagged lifecycle; durable objects remain untagged.

If rollback is necessary, close admission and quiesce first. Review a fresh plan
for a coherent prior runtime/script/permissions revision, preserving state and
accepted version pins. Restoring broad host grants restores SEC-20-01 exposure;
the environment must remain supervised and closed to beta. Repeat effective IAM,
broker and disposable lifecycle acceptance after the final coherent revision.
Offline checks alone do not authorize a future boundary change. Keep
infrastructure removal and continued admission gated on coherent consumers and
repeat the relevant live 19.3.8 checks after any rollback or material change.

AWS references:
[presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html),
[conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html),
[PUT integrity](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html),
[POST policy](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-HTTPPOSTConstructPolicy.html),
[POST checksum fields](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/RESTObjectPOST.html),
[SendCommand](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html),
[SSM S3 output permissions](https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-param-runcommand.html),
[reviewed SSM managed policy](https://docs.aws.amazon.com/aws-managed-policy/latest/reference/AmazonSSMManagedInstanceCore.html),
[DynamoDB transaction IAM](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/transaction-apis-iam.html).
