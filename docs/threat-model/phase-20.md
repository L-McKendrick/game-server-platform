# Phase 20 Threat Model

## Scope and method

This review covers the deployed development architecture as of 2026-09-09:
Discord ingress and outbound messages, DynamoDB metadata, SQS commands and
artifacts, Step Functions lifecycle workflows, S3 assets and archives, managed
EC2 game hosts, Systems Manager commands, Steam authorization, destructive
archive/termination/reset operations, Terraform state, and the current manual
deployment process.

Phase 19.3.7 source review (2026-09-17) removes both host asset inline policies,
uses trusted exact-object capabilities and verifies staged output before durable
publication. All six issuers share a guarded runtime and normalized script digest.
On 2026-09-18, test-67 actual-host cross-session/guild asset, secret and Steam-lease
denials and live capability constraints passed. Review found and fixed preset
authority persistence, timestamp ordering, Steam finalization and archive Bash
dispatch defects. The fixes were deployed; test-71 completed verified
archive/destruction/restore, final cleanup removed all session resources and
versions, and the Ubuntu CI race gate passed. SEC-20-01 is closed for controlled
beta admission. Detailed evidence is in
`docs/phase-19-host-object-access.md` and this chat's external context file.

The review treats Discord users, attachment metadata, Steam metadata and files,
Workshop content, game files, archives, network responses, queue payloads, and
managed hosts as untrusted. DynamoDB session/workflow records and checksum-
verified S3 objects are authoritative only after their existing conditional
validation succeeds.

## Trust boundaries and protected assets

| Boundary | Untrusted entry | Protected assets | Enforcement and evidence |
| --- | --- | --- | --- |
| Discord to API Gateway/Lambda | Headers, body, IDs, options, components, attachments | Command authority and guild/session identity | Ed25519 signature and timestamp verification, guild/role/owner checks, state-bound components, idempotency tests |
| Discord CDN to artifact worker | URL, redirects, names, sizes, file content | Lambda network access, S3 inputs, parser memory | HTTPS allowlist on every redirect, declared-size read bound, type/extension/content parsing, normalized filenames |
| Steam APIs and SteamCMD | URLs, metadata, child lists, downloaded files and output | Host filesystem, credentials, public presentation | Canonical numeric IDs, app/tag checks, bounded collections, isolated staging, allowlisted progress, auth cleanup |
| SQS and Step Functions to workers | Replayed, stale, malformed, or partial payloads | Lifecycle state, capacity slot, infrastructure | Schema/state/version checks, workflow ownership, FIFO/idempotency, bounded retries, terminal failure handling |
| Lambda to managed EC2 through SSM | Instance ID and generated shell | Root host execution and session data | Project/environment tag conditions, fixed SSM document, quoted/base64 data, no user shell, bounded timeouts |
| Managed EC2 to AWS | Compromised game process or host | All session artifacts and Steam cache | Instance role and service-user separation; important cross-session residual risk below |
| Archive input to replacement host | Compressed paths, types, size and count | Host filesystem and restored identity | Manifest/object checksums, root allowlist, path/link/device rejection, expansion bounds, auth-material scrub |
| Destructive lifecycle operations | Discord confirmation, stale metadata, forged tags | EC2, EBS, S3 versions, metadata | Owner/guild/action/state-bound confirmation, immutable tags, archive-before-destroy, repeat discovery, audit events |
| Terraform/operator to AWS | Source, variables, saved plan, credentials | Whole environment and state | Remote versioned state, public access block, TLS-only bucket policy, reviewed saved plans; OIDC deferred to 20.3 |

## Threat assessment

| Threat | Existing mitigation | Review result |
| --- | --- | --- |
| Forged or replayed Discord interaction | Ed25519 verification, five-minute timestamp window, interaction idempotency | Acceptable; negative and replay tests exist |
| Unauthorized lifecycle or destructive command | Guild/role/owner/admin checks, hidden session resolution, durable single-use confirmations | Acceptable; authorization is rechecked before queueing and mutation |
| SSRF through attachment URLs | Only Discord CDN HTTPS hosts are accepted before request and after each redirect | Acceptable within the current Discord attachment contract |
| Malicious upload or Workshop content | Filename/path normalization, size bounds, dedicated parsers, immutable checksums, isolated Workshop staging | Residual game-content risk remains; content is not sandboxed beyond the unprivileged game service |
| Shell or argument injection | User values are normalized or base64 encoded; generated scripts quote variables and do not use `eval` | Acceptable; shell regressions cover major generated-command paths |
| Archive traversal or expansion bomb | Archive checksum, member root/type/path validation, 20 GiB/200,000-member expansion limits | Acceptable for the documented archive contract |
| S3 disclosure or downgrade | Account-owned bucket, encryption, public access block, narrowly scoped runtime actions | Insecure transport denial was missing and is corrected in Phase 20 |
| Mutating unrelated EC2 instances | Most destructive roles require Project and Environment resource tags | Sleep/wake previously had wildcard mutation; corrected in Phase 20 |
| Compromised game host accesses another session | Shared instance profile has no standing asset permissions; an exact bearer URL can expose only its bounded authorized object during the capability lifetime | Standing Steam-cache access is removed by Phase 19.1; Phase 19.3 actual-host tests deny cross-session/guild assets, secrets and lease access. Theft of a currently issued bearer remains a bounded residual risk. |
| Secret or raw diagnostic leakage | Secrets retrieved at runtime, auth files scrubbed, bounded allowlisted progress/errors, suppressed mentions | Acceptable with continued regression scanning; CloudWatch/S3 access remains privileged operator data |
| Duplicate infrastructure and cost amplification | Idempotency, workflow locks, capacity slot, tagged discovery, budgets, inactivity policies, maximum-duration warnings and enforcement | Phase 19.2 adds a bounded wall-clock guardrail, but failed lifecycle cleanup can still retain billable resources; this is not a guaranteed spend cap |
| Queue or API denial of service | Payload limits, FIFO queues, visibility bounds, DLQs, Lambda concurrency controls where configured | Residual account-level throttling/cost risk to be verified in 20.5 and 20.6 |
| Dependency or CI supply-chain compromise | Go modules and action versions are declared; CI is read-only; Phase 19.3 removes the unused runtime AWS CLI installer | Actions are tag-pinned rather than commit-pinned; no dependency/security scan is enforced; approved host image and installer verification remain staging work |
| Terraform state disclosure or unauthorized deployment | Private versioned encrypted state and manual reviewed plans | Human/deployment permission boundary is not yet codified; OIDC and protected deployment are 20.3 work |

## Corrections made by this review

1. Both Terraform-state and session-assets buckets deny every S3 action when
   `aws:SecureTransport` is false.
2. Sleep/wake retains wildcard `DescribeInstances`, which EC2 does not support
   at resource scope, but `StartInstances` and `StopInstances` now apply only to
   instances carrying the exact project and environment tags.
3. Phase 19.3 replaces host archive/input AWS CLI access with bounded HTTPS
   transfers; mount/device, extraction and sanitized failure safeguards remain.
4. Restore terminal-result shape is explicit, and malformed or contradictory
   results route through the normal failure finalizer.

## Release exceptions and required follow-up

| ID | Severity | Residual risk | Required disposition | Owner / target |
| --- | --- | --- | --- | --- |
| SEC-20-01 | Closed | Standing host asset grants are removed. Actual-host cross-session/guild, config/archive/progress/result/log/script, secret and Steam-lease denials passed; coherent modded/vanilla/TeamSpeak lifecycle, archive/restore, cleanup and Linux race acceptance passed. | Closed for controlled beta on 2026-09-18. Retain capability-expiry, lifecycle cleanup and broker-finalization monitoring; repeat acceptance after any boundary rollback. | Platform owner; Phase 19.3 complete |
| SEC-20-02 | Medium | Phase 19.2 maximum-duration enforcement is implemented but not deployed or live-verified. Monitor, queue, workflow, or cleanup failures can still retain billable resources. | Deploy through a reviewed plan, exercise warning and deadline paths, retain AWS billing alarms, and investigate failed cleanup promptly. | Platform owner; deployment acceptance before production |
| SEC-20-03 | High | Deployment still depends on a human AWS profile and broad first-deployment permissions. | Implement scoped GitHub OIDC plan/deploy roles and protected environments in 20.3. | Platform owner; 20.3 |
| SEC-20-04 | Medium | GitHub Actions use major-version tags and CI does not run dependency, secret, or IaC security scanning. | Select pinned action commits and approved scanners as part of the protected CI/CD design. | Platform owner; 20.3 |
| SEC-20-05 | Medium | Public game/voice UDP and unrestricted host egress are intentional for Steam and players but enlarge the compromised-host boundary. | Validate egress/ingress requirements in staging and document any practical restriction or accepted exposure. | Platform owner; 20.4/20.6 |
| SEC-20-06 | Medium | AWS-managed encryption keys do not provide environment-specific key-policy separation. | Decide whether production requires customer-managed keys during staging design; record the decision in an ADR. | Platform owner; 20.4 |
| SEC-20-07 | Medium | Phase 19.3 removes runtime AWS CLI installation; other host image and installer supply-chain verification remains unaccepted. | Verify approved patched host image and remaining installer provenance/checksums before production staging approval. | Platform owner; 20.4 |

No exception authorizes production release by itself. Critical and high items
must be closed or explicitly accepted by the accountable operator with scope,
expiry, compensating controls, and rollback criteria.
