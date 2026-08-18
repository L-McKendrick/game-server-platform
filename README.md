# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

The platform currently provides:

- Ed25519 verification against the exact raw Discord request body;
- timestamp freshness, request-size, application-ID, and guild validation;
- `/rb create`, `/rb list`, `/rb status`, `/rb configure`, uploads, start, sleep, wake, owner-confirmed archive, restore, and owner-confirmed irreversible termination;
- ephemeral responses with mentions disabled;
- command idempotency derived from Discord interaction IDs;
- API Gateway HTTP API v2 and Lambda backed by DynamoDB;
- DynamoDB-backed guild access configured through an ephemeral Discord role select menu opened by `/admin access`;
- an asynchronous attachment worker with approved-host downloads, strict bounds, SHA-256 hashing, content validation, isolated S3 persistence, and conditional metadata updates;
- a Secrets Manager-backed Discord notification worker;
- normalized command and workflow contracts with conditional per-session workflow leases;
- command, attachment, and notification FIFO queues with dead-letter queues;
- canonical Step Functions Standard state-machine boundaries;
- a FIFO command worker that revalidates access and starts implemented lifecycle workflows;
- a dedicated game VPC, two public subnets, game/voice security groups, and no inbound SSH;
- an idempotent EC2/EBS provisioning worker with IMDSv2, encrypted volumes, Systems Manager readiness, resource discovery tags, and DynamoDB capacity slots;
- deployed provisioning, resumable Arma bootstrap, monitoring, and manual sleep/wake workflows;
- a resumable Systems Manager bootstrap path for SteamCMD, Arma, Workshop content, mission deployment, optional TeamSpeak, systemd, and health gating;
- guarded portable archive/destruction and restore workflows with versioned manifests, checksum verification, tagged-resource deletion, fresh infrastructure, safe extraction, and health acceptance;
- irreversible termination with exact tag checks, bounded EC2/EBS deletion, permanent removal of every versioned session object, and an audit tombstone;
- an AWS monthly budget and a fail-closed provisioning enablement gate;
- reproducible Lambda packages, least-privilege IAM, retained logs, and CI checks.

The reconciliation state machine intentionally terminates with `PhaseNotImplemented`. `/rb start` and restore use the development one-session capacity limit and AWS Budget alerts.

Current milestone: Phase 12 Discord experience and session UX is proceeding by
explicit roadmap reorder. Phases 10 and 11 remain pending.

See [Phase 5: Infrastructure Provisioning](docs/phase-5-infrastructure-provisioning.md) for the implemented boundary and deployment safety gates.
See [Phase 6: Arma Bootstrap](docs/phase-6-arma-bootstrap.md) for the resumable installation and health boundary.
See [Phase 8: Sleep and Wake](docs/phase-8-sleep-wake.md) and [Phase 9: Archive and Restore](docs/phase-9-archive-restore.md) for the current lifecycle boundaries.

## Local Discord interaction server

The local server uses in-memory repositories. Data resets whenever the process restarts.

Required environment variables:

```text
DISCORD_PUBLIC_KEY=<hex public key from the Discord developer portal>
DISCORD_APPLICATION_ID=<Discord application ID>
DISCORD_ALLOWED_GUILD_IDS=<comma-separated development guild IDs>
```

No administrator, role, or channel IDs need to be preconfigured. Discord members with Administrator or Manage Server permission can run `/admin access` and choose the allowed roles. Optional `DISCORD_ALLOWED_ROLE_IDS` and `DISCORD_ALLOWED_CHANNEL_IDS` values remain available as a deployment fallback.

Optional environment variables:

```text
DISCORD_LISTEN_ADDRESS=127.0.0.1:8080
DISCORD_MAX_REQUEST_BYTES=65536
DISCORD_SIGNATURE_MAX_AGE_SECONDS=300
```

Run:

```powershell
$env:DISCORD_PUBLIC_KEY = "<public-key>"
$env:DISCORD_APPLICATION_ID = "<application-id>"
$env:DISCORD_ALLOWED_GUILD_IDS = "<development-guild-id>"
go run ./cmd/discord-local
```

The endpoint is `POST /discord/interactions`. Versioned development command definitions are stored in:

```text
deploy/discord/rb-command.json
deploy/discord/admin-command.json
```

## Development deployment

Package all Linux Lambda custom runtimes:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/package-discord-lambda.ps1
```

Copy `infra/terraform/environments/dev/terraform.tfvars.example` to an ignored `.tfvars` file and provide the Discord public key, application ID, and development guild ID. Initialize with the existing remote backend configuration and apply only a reviewed plan.

After apply, set Terraform output `discord_interactions_endpoint` as the Discord application's interaction endpoint. Register the guild commands with a short-lived process environment containing the bot token:

```powershell
$env:DISCORD_APPLICATION_ID = "<application-id>"
$env:DISCORD_GUILD_ID = "<development-guild-id>"
$env:DISCORD_BOT_TOKEN = "<bot-token-from-Secrets-Manager>"
go run ./cmd/discord-register
Remove-Item Env:DISCORD_BOT_TOKEN
```

Never place the bot token in a `.tfvars` file, command definition, log, or Terraform state.

## Initial technology choices

- AWS
- EC2, EBS, and S3
- Terraform
- Go
- DynamoDB
- AWS Secrets Manager
- SQS and Step Functions
- EventBridge
- AWS Lambda and API Gateway
- GitHub Actions

## Terminology

- Deployment: a persistent game-server configuration and lifecycle record
- Server: a temporary compute instance
- Mission: an Arma-specific mission file
- Deployment archive: a long-term backup of a deployment
