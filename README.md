# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Phases 1-4 are implemented in the repository: foundation, durable metadata, the deployable Discord interface, and the asynchronous workflow foundation.

The platform currently provides:

- Ed25519 verification against the exact raw Discord request body;
- timestamp freshness, request-size, application-ID, and guild validation;
- `/session create`, `/session list`, `/session status`, `/session configure`, `/session upload-mission`, and `/session upload-preset`;
- ephemeral responses with mentions disabled;
- command idempotency derived from Discord interaction IDs;
- API Gateway HTTP API v2 and Lambda backed by DynamoDB;
- DynamoDB-backed guild access configured through an ephemeral Discord role select menu opened by `/admin access`;
- an asynchronous attachment worker with approved-host downloads, strict bounds, SHA-256 hashing, content validation, isolated S3 persistence, and conditional metadata updates;
- a Secrets Manager-backed Discord notification worker;
- normalized command and workflow contracts with conditional per-session workflow leases;
- command, attachment, and notification FIFO queues with dead-letter queues;
- canonical Step Functions Standard state-machine boundaries;
- reproducible Lambda packages, least-privilege IAM, retained logs, and CI checks.

The lifecycle state machines intentionally terminate with `PhaseNotImplemented` in Phase 4. No `/session start` command is registered yet, so the interface does not claim that EC2 provisioning works before Phase 5.

Next milestone: Phase 5 infrastructure provisioning—VPC, security groups, EC2, EBS, instance roles, and Systems Manager tasks behind `ProvisionSession`.

See [Phase 4: Workflow Foundation](docs/phase-4-workflow-foundation.md) for the delivered boundary and remaining deployment inputs.

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
deploy/discord/session-command.json
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
