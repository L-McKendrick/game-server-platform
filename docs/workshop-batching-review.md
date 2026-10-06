# Workshop batching branch review

Reviewed the combined changes against `origin/main` at `b5e386f`. This is an
offline review; Steam throughput and real Launcher import acceptance remain open.

## Reconciliation

Main preserves original validated client-preset bytes, their digest-addressed
input key and checksum. This branch generates a separate downloadable preset with
Launcher metadata and uploaded/configured cDLCs. The merged ingestion regression
asserts both contracts and replay. Main's creation mod-options continuation fix
is retained. Roadmap entries from both branches are retained.

## Corrected findings

- Keeping the original preset exposed bootstrap's broad ID scan to DLC/footer
  IDs that ingestion excludes. Extract only typed ModContainer rows for client
  and server inputs; fail on unreadable inputs and preserve first-seen deduplication.
  Executable regressions cover mixed rows, DLC-only inputs and missing files.
- Permanent Workshop failures could be classified as transient when earlier batch
  output mentioned a connection. Check visibility/removal before transient text;
  mixed successful/permanent output tests require one attempt and the correct code.
- One shell-test line accidentally joined redaction and temporary-file assertions.
  Split them. Negative checks using `!` alone also do not trigger Bash `errexit`;
  use explicit failures for redaction, successful-item retry and sampler checks.
- The Discord experience document still claimed server input was regenerated.
  Align it with main's original-byte integrity contract and separate public export.

## Optional refactors

- **P3: Share artifact construction in `internal/app/modlist/modlist.go`.**
  `Generate` and `GenerateWorkshop` repeat filename, digest, object-key and Artifact
  construction. A private helper accepting the normalized rows would keep these
  export paths consistent. Keep their distinct validation and empty-item policies.
- **P3: Centralize Creator DLC catalog metadata.** Supported keys/mod folders live
  in `internal/domain/creator_dlc.go`, display names in sessioncard, and Steam app
  IDs/names in modlist. A shared immutable catalog would prevent future additions
  from validating successfully but lacking an export mapping. Keep Steam Store
  identities distinct from Workshop identities.

These refactors are deferred; they are not required to reconcile existing behavior.
No additional blocking correctness finding was identified in the reviewed diff.

## Validation boundaries

Strict XML/reference round-trips, uploaded/configured DLC deduplication, original
preset integrity, replay, batch success confirmation, transient retries, permanent
failures, cache reuse, unsafe payload rejection and sampler cleanup are covered.
Final command results are recorded in CURRENT_WORK.md. Live rollout needs rebuilt
archives and a fresh approved plan; existing public objects need regeneration.
