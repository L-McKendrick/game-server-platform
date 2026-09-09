# Phase 9: Archive, Restore, and Termination

## Phase 9.1 archive creation

Phase 9.1 established the portable backup and manifest boundary. Phase 9.2
extends the same workflow through guarded destruction and adds restore.

- `/rb archive` is owner-only and creates a durable, owner-bound confirmation.
  `/rb confirm` acknowledges that services stop and the current EC2/EBS
  resources are removed only after the consistent archive is verified.
- Owner-requested archives may start from `RUNNING` or `IDLE` with a managed
  instance and data volume. One normal workflow lock covers command dispatch,
  upload, verification, manifest persistence, and metadata completion.
- Phase 14 extends the same guarded workflow to a session that has remained
  `SLEEPING` for 72 continuous hours. The workflow starts the retained instance,
  waits for EC2 and Systems Manager readiness, and then follows the same backup,
  checksum, ownership, and destruction boundaries described below.
- The host takes an exclusive archive lock, stops active Arma and optional
  TeamSpeak services, and always attempts to restart services on exit.
- The `tar+gzip` archive contains configuration, platform state, logs, mission
  files, Arma profile data, and optional TeamSpeak data. SteamCMD, installed
  Arma binaries, Workshop downloads, and the swapfile are intentionally
  omitted because bootstrap can recreate them.
- The Phase 9.1 archive is limited to 4 GiB so it can use one S3 `PutObject`
  with an explicit SHA-256. Exceeding the limit fails closed without deleting
  infrastructure. Multipart archives can be added with a versioned manifest
  change if the persistent-data boundary later requires them.
- The instance uploads `session.tar.gz` with its SHA-256. The archive worker
  verifies S3-reported size and checksum, writes `manifest.v1.json` with its
  own SHA-256, and verifies that object before recording either checksum.
- S3 bucket versioning and default AES-256 encryption remain authoritative.
  Phase 9.1 adds no new retention deletion policy and no EBS snapshot.
- A failed command, upload, checksum, or metadata transaction before the
  destructive boundary leaves the instance and volumes intact. A failed
  workflow start restores the original `RUNNING` or `IDLE` state.

## Phase 9.2 guarded destruction and restore

- Archive metadata and both object checksums are conditionally persisted while
  the workflow still owns the session. Only then does the session enter
  `DESTROYING`.
- The archive worker re-verifies the archive and manifest in S3 immediately
  before termination. The EC2 adapter refuses mutation unless `Project`,
  `Environment`, and `SessionId` tags all match the authoritative request.
- The tagged instance is terminated first. Its root volume uses
  delete-on-termination. The tagged persistent data volume is deleted only
  after the instance is confirmed terminated and the volume is detached.
- Metadata becomes `ARCHIVED` and clears disposable resource identifiers only
  after both deletion observations succeed. Partial failures retain identifiers
  in `FAILED` for Phase 10 reconciliation.
- `/rb start` routes an archived session through the same restore workflow as
  the retained explicit `/rb restore` command. It revalidates authorization,
  manifest schema, session and
  archive identity, configuration revision, artifact keys, S3 sizes, and both
  checksums before reserving capacity or launching resources.
- Restore creates a new encrypted root and data volume and a new tagged EC2
  instance with a restore-specific idempotency token. It waits for EC2 and SSM,
  verifies or installs AWS CLI v2 on the replacement host, downloads and
  validates the recorded archive, idempotently establishes the same Steam and
  optional TeamSpeak service accounts used by bootstrap, and then runs the
  normal software/bootstrap process against the restored portable data. The
  bootstrap command accepts the active restore workflow lock whether or not a
  preset revision is pending, while rejecting missing or mismatched locks.
- The configured Ubuntu AMI contract currently maps its root volume at
  `/dev/sda1`; replacing that AMI requires verifying its advertised root-device
  name before deployment so the platform does not create an unused extra disk.
- Before extraction, the host verifies compressed size and SHA-256, rejects
  absolute paths, traversal, links, devices, unexpected roots, more than
  200,000 entries, or more than 20 GiB expanded content. It restores only the
  portable roots, fixes ownership, starts services, and requires Arma and
  optional TeamSpeak service/UDP health.
- Only after bootstrap and restore health pass are the new resource identifiers
  and endpoint retained as `RUNNING`/`HEALTHY`; the durable archive remains
  available for future recovery.
- A replacement-host restore mounts and verifies the exact recorded data volume
  before downloading or extracting the archive. Temporary archive bytes and
  restored data stay on that persistent volume rather than the root filesystem;
  replays reuse the mounted XFS volume and fail closed on a missing, mismatched,
  or unsupported device. Both the existing-mount and first-mount paths enforce
  an exact recorded-device postcondition before archive access.
- Restore invalidates archived completion markers for host-local software,
  current configuration deployment, and Workshop synchronization before
  bootstrap. Bootstrap therefore recreates systemd units and rematerializes
  intentionally omitted software and Workshop data on every replacement host,
  while retaining the portable archive as the durable source. Service-started
  progress is recorded only after every enabled service starts successfully.
- Missing AWS CLI prerequisites and incomplete or contradictory managed-command
  results fail with stable bounded codes through the restore failure finalizer.
  Failures before any replacement resource exists return to `ARCHIVED`.
  Failures after replacement resources exist enter `FAILED` with the verified
  archive, resource identifiers, capacity slot, and released workflow lock
  retained for truthful cost inspection. The owner can request restore again;
  this bounded retry reuses those recorded resources and reruns the idempotent
  mount, extraction, bootstrap, and health path.

### Live development acceptance

The Test-56 retry completed the deployed restore workflow successfully on
2026-09-09 (`1547132738950406144`). It recreated the replacement host from the
verified archive, rebuilt host-local stages, passed service health, and released
the workflow normally. The disposable session was subsequently terminated and
now has a `DELETED` tombstone. Modded and TeamSpeak-enabled restore variants
retain offline coverage and should be exercised again in the Phase 20.4 staging
lifecycle matrix; they are not implied by this vanilla live exercise.

## Phase 9.3 irreversible termination

- `/rb terminate` is owner-only and creates a durable, owner-bound confirmation.
  `/rb confirm` consumes it once; the warning states that no backup is created
  and the operation cannot be undone.
- Termination is available for any unlocked, non-deleted session, including a
  `FAILED` session requiring cleanup. A single workflow lock prevents races
  with provisioning, sleep/wake, archive, restore, or another termination.
- The tagged EC2 instance is terminated immediately. The adapter requires exact
  `Project`, `Environment`, and `SessionId` tags before mutation. After EC2 is
  confirmed terminated, the detached tagged data volume is deleted; the root
  volume remains governed by delete-on-termination.
- Both infrastructure observations are bounded to 40 checks. Partial failure
  records `FAILED`, releases the workflow lock, and retains resource and object
  identifiers so an owner can safely retry termination.
- After infrastructure deletion, the worker permanently deletes every object
  version and delete marker under the exact `sessions/<session-id>/` prefix.
  This includes uploaded missions/presets and all archive versions; S3 bucket
  versioning cannot retain hidden session storage.
- The capacity slot is released only after deletion succeeds. Metadata then
  becomes `DELETED`, clears infrastructure, archive, and artifact references,
  and retains the session identity plus immutable events as the audit
  tombstone.

Phase 10 owns cancellation, automatic reconciliation, and orphan cleanup.
