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

- **P3: Centralize Creator DLC catalog metadata.** Supported keys/mod folders live
  in `internal/domain/creator_dlc.go`, display names in sessioncard, and Steam app
  IDs/names in modlist. A shared immutable catalog would prevent future additions
  from validating successfully but lacking an export mapping. Keep Steam Store
  identities distinct from Workshop identities.

Catalog centralization remains deferred; it is not required for this change.
No additional blocking correctness finding was identified in the reviewed diff.

## Validation boundaries

Strict XML/reference round-trips, uploaded/configured DLC deduplication, original
preset integrity, replay, batch success confirmation, transient retries, permanent
failures, cache reuse, unsafe payload rejection and sampler cleanup are covered.
Final results are in CURRENT_WORK.md. The artifact archive is rebuilt; rollout
requires a fresh reviewed plan. Only malformed presets from tests 1/2 need new uploads.

## Live batching follow-up: test-workshop-3

The session failed on 2026-10-06 at 10:39:54 UTC: SteamCMD returned success,
but the strict batch parser did not recognize every requested completion marker.
Progress stayed at the first item despite 2.26 GB of staged content. Raw Steam
output was removed by the normal credential cleanup, so individual success lines
from that run cannot be recovered.

A bounded no-login probe of the installed client subsequently confirmed six ANSI
CSI sequences and three ANSI-prefixed lines. The old anchored parser rejected
such prefixes. Executable regressions reproduce the defect and verify that
normalizing ANSI CSI sequences and carriage returns fixes confirmation, progress
and zero-exit error detection while preserving exact requested-ID checks and
fail-closed behavior. Scan the whole normalized error stream rather than using
`grep -q`, which can cause an upstream SIGPIPE under `pipefail` on large logs.

The fix is source-validated; successful Workshop completion and measured speedup
still require a deployment and a new authorized bootstrap operation. The failed
session has no workflow lock and can be terminated without metadata repair.

## Simulation and refactor review

- Drive the production batch functions with a simulated 19-item Steam transcript
  containing ANSI-prefixed command echoes, progress, CR-delimited success lines,
  cached login text and realistic destination/byte-count suffixes. Require one
  SteamCMD invocation and complete temporary-file cleanup.
- Table-driven expectations now assert exit status and invocation count independently.
  Previously an `&&` assertion could fail without triggering `errexit`, then be
  masked by a later successful assertion. Additional cases reject duplicate,
  unrelated, prefixed, partial and nonzero-exit success confirmations; ANSI
  transient retries skip previously confirmed items. Real sampler tests use ANSI.
- Directory traversal failure was treated as absence of symlinks by an inverted
  find/grep pipeline. Share a checked traversal helper across mod cache, downloaded
  mods and missions. Failed traversal cannot establish safe content. Simulations
  cover failed cache and payload inspection, as well as symlink rejection.
- Share preset rendering, filename, checksum and artifact construction between
  upload and Workshop exports while preserving each path's validation rules.

Directory/file creation alone does not establish completed downloads. Retain exact
SteamCMD success confirmations and independent payload, size, revision and checksum
checks. Simulations establish behavior, not successful live Workshop completion or
measured speed improvement.
