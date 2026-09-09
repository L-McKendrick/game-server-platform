# Current Work

## State and Objective

Phase 20 steps 20.1 and 20.2 are complete on
`codex/phase-20-production-hardening`. The branch is in final review for a pull
request; do not begin 20.3 without new user direction.

## Current Handoff

- Phase 20 hardens restore prerequisites, terminal-result handling, failure
  finalization, archived `/rb start` routing, replacement-host storage, root
  mapping, retained-resource retry, diagnostic redaction, and host-stage replay.
- The final Test-56 retry execution `1547132738950406144` succeeded on
  2026-09-09 after rebuilding the replacement host from its verified archive.
  The disposable session was subsequently terminated and now has a `DELETED`
  tombstone, so its replacement resources no longer require recovery.
- The branch review covered lifecycle authorization, replay and rollback,
  replacement-host accounts/filesystems/markers, Step Functions result shapes,
  EC2 mappings, S3 transport policy ownership, IAM mutations, diagnostics,
  documentation, and manual deployment boundaries.
- One review defect was corrected: restore now mounts the discovered recorded
  EBS device explicitly and verifies that exact source after either a first
  mount or an existing mount before downloading the archive.
- Documentation now distinguishes the live vanilla acceptance from modded and
  TeamSpeak-enabled variants, records the approved AMI `/dev/sda1` coupling,
  and tracks the unpinned AWS CLI installer as SEC-20-07.
- TLS-only state/assets bucket policies and scoped sleep/wake mutations were
  confirmed live. SEC-20-01 remains the production release blocker.
- Full Go coverage tests, vet, all command builds, Lambda packaging, Terraform
  recursive formatting, and bootstrap/development validation passed. No AWS or
  Discord state was changed by this final review.

## Important Operator Attention

- **Production release blocker SEC-20-01:** the shared managed-game instance
  profile can access other sessions' S3 inputs/archives and the shared Steam
  authorization cache. Default: do not approve production use until a
  session-scoped host-access design is implemented and verified.
- Phase 19 maximum-duration enforcement remains pending because Phase 20 was
  started out of order by explicit approval. Default: keep use non-production
  and manually supervised until Phase 19 is complete.
- SEC-20-07 records that the replacement-host AWS CLI installer is fetched from
  AWS over TLS and major-version checked, but is not checksum/signature pinned.
  Default: close it through a versioned host image or verified installer before
  production staging approval.
- The local race build remains unavailable without a working C compiler and is
  a required GitHub CI gate. Modded and TeamSpeak-enabled restore variants have
  offline coverage but remain staging lifecycle cases.

## Commands to Apply Current Changes

Run from the repository root. Package the affected Lambda sources and create a
fresh saved plan; preserve every existing user-owned plan file.

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$phase20ReviewPlan = "phase20-review-$timestamp.tfplan"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase20ReviewPlan"
terraform -chdir=infra/terraform/environments/dev show $phase20ReviewPlan
```

After approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase20ReviewPlan
aws lambda get-function-configuration --function-name game-server-platform-dev-restore-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId,BootstrapScript:Environment.Variables.BOOTSTRAP_SCRIPT_KEY}" --output table
```

No Discord command registration is required. This final review changes only the
restore worker's exact-device first-mount postcondition plus documentation; the
fresh plan must be reviewed for that boundary. After deployment, confirm the
restore worker is active. Exercise modded and TeamSpeak-enabled restores in the
Phase 20.4 staging lifecycle matrix. Do not proceed to 20.3 without new
direction.
