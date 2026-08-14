# Current Work

## State and Objective

Phases 1-9.4 are complete. The operator reports that the Phase 9.3 termination
path was deployed and `Test Session` was permanently terminated. Replacement
session `01KZZA8R4BGFC1FBVSBWGSBJA8` reached bootstrap after the provisioning
IAM correction, then failed because its authenticated Steam account required a
Steam Guard challenge.

## Phase 9 Completed

- `/session archive` is owner-only and explicitly confirms that verified
  archival is followed by EC2 and EBS removal.
- Archive metadata and both S3 checksums are durably recorded before the
  session crosses into `DESTROYING`. The worker re-verifies both objects before
  mutation.
- Destructive EC2 operations require matching `Project`, `Environment`, and
  `SessionId` tags. The instance terminates before the detached data volume is
  deleted; metadata reaches `ARCHIVED` only after both observations succeed.
- `/session restore` is owner-only and validates manifest identity,
  configuration revision, artifact keys, sizes, and checksums before reserving
  capacity or creating resources.
- Restore uses fresh encrypted EC2/EBS infrastructure, the existing bootstrap,
  safe bounded archive extraction, ownership repair, service restart, and Arma
  plus optional TeamSpeak health acceptance before returning `RUNNING` and
  `HEALTHY`.
- Failure paths retain discovered resource identifiers for Phase 10
  reconciliation. Capacity is released only when resources are known absent.
- `/session terminate` is owner-only, requires explicit irreversible-action
  confirmation, creates no backup, and accepts any unlocked non-deleted
  session so failed cleanup can be retried.
- Termination requires exact immutable EC2/EBS tags, bounds deletion
  observations, permanently deletes every S3 object version/delete marker
  below the exact session prefix, releases capacity, and retains only a
  `DELETED` audit tombstone.
- Partial termination failures retain resource and object identifiers in
  `FAILED`; other lifecycle workflows cannot race the termination lock.
- Non-race Go tests, vet, all command builds, eleven Lambda packages, Discord JSON
  parsing, Terraform formatting/validation, and diff checks pass. CGO/race
  remains assigned to GitHub CI; thorough live archive/restore acceptance is
  reserved for the Phase 9 completion checks.

## Wake Correction

- `WakeSession` now waits for the restarted instance to report `Online` in
  Systems Manager before dispatching its health probe.
- The readiness loop is bounded to 40 attempts at 15-second intervals and
  fails with `ERR_SSM_TIMEOUT` rather than racing `ssm:SendCommand`.
- The sleep/wake worker role now has the read-only
  `ssm:DescribeInstanceInformation` permission required by the readiness check.
- Full non-race Go tests, vet, command builds, Lambda packaging, Terraform
  formatting/validation, and diff checks pass. CGO/race remains assigned to
  GitHub CI. No further live wake acceptance occurred before `Test Session`
  was terminated.

## Terminated Test Resource

- Session `01KZ5VR86TM25A6Q3EKZGGX4DT` (`Test Session`) is terminated.
- Former instance `i-07abe4ba82ce2649f`, data volume
  `vol-04605fd628fabaf80`, and root volume `vol-0b0c4c54fd555b99d` are no
  longer active resources.

## Vanilla Session Correction

- `/session configure` now accepts `vanilla:true`; the selection is persisted,
  audited, and displayed by configuration and status responses. Omitted or
  false remains the backward-compatible modded behavior.
- A configured vanilla session becomes `NEW` after mission validation without
  requiring a launcher preset. Modded sessions still require both artifacts.
- Vanilla bootstrap uses anonymous SteamCMD for app `233780`, skips the Creator
  DLC beta, Steam secret retrieval, preset download, and Workshop processing,
  and launches with an empty mod list.
- Archive manifests and restore verification preserve the vanilla selection.
- Focused tests, all non-race Go tests, vet, command builds, Lambda packaging,
  Discord JSON parsing, Terraform validation, Bash syntax checks, and diff
  checks pass. CGO/race remains assigned to GitHub CI.

## Operator Attention

Deploy the updated Lambda packages, Terraform-managed bootstrap artifact, and
Discord command definition before testing Phase 9.4. Vanilla mode is selected
only while a session is `DRAFT` by running `/session configure` with
`vanilla:true`; configuration plus a mission upload then makes it startable
without `/session upload-preset`. Existing session
`01KZZA8R4BGFC1FBVSBWGSBJA8` is already `FAILED` and cannot be converted through
the draft-only configuration command; terminate it and create a new vanilla
session for acceptance testing. Steam Guard handling for modded sessions remains
deferred to Step 10.3.

## Exact Next Step

Deploy and acceptance-test one new vanilla session through Discord. After that,
create a new Phase 10 branch, split Step 10.1 into 1-8 numbered tasks in
`PROJECT_PLAN.md`, and implement only the first task by default.
