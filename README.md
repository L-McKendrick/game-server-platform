# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Foundation, metadata, command idempotency, the Discord interaction boundary,
and the development Lambda/API Gateway ingress deployment are implemented.

The deployable Discord slice provides:

- Ed25519 verification against the exact raw request body;
- timestamp freshness, request-size, application-ID, and guild validation;
- Discord `PING` acknowledgement;
- `/session create`, `/session list`, and `/session status`;
- command idempotency derived from the Discord interaction ID;
- DynamoDB-backed Lambda composition;
- API Gateway HTTP API payload format `2.0`;
- least-privilege Lambda IAM;
- API throttling, access logs, retention, and alarms;
- a repeatable Windows packaging script and development-guild registration script.

Next milestone: add asynchronous command normalization and the workflow
foundation with SQS, Step Functions, workflow locks, and notification delivery.
Do not add EC2 provisioning until that control-plane slice is complete.

## Local Discord interaction server

The local server intentionally uses the in-memory repository. Data resets whenever the process restarts.

Required environment variables:

```text
DISCORD_PUBLIC_KEY=<hex public key from the Discord developer portal>
DISCORD_APPLICATION_ID=<Discord application ID>
DISCORD_ALLOWED_GUILD_IDS=<comma-separated development guild IDs>
```

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

The endpoint is:

```text
POST /discord/interactions
```

## Deploy the development Discord endpoint

Build the Linux ARM64 Lambda package on Windows:

```powershell
./scripts/build-discord-lambda.ps1
```

The script creates:

```text
dist/discord-interactions.zip
```

Copy and edit the ignored Terraform values:

```powershell
Copy-Item `
  infra/terraform/environments/dev/discord.auto.tfvars.example `
  infra/terraform/environments/dev/discord.auto.tfvars
```

Then follow:

```text
docs/runbooks/deploy-discord-interactions.md
```

The versioned development command definition remains at:

```text
deploy/discord/session-command.json
```

The Lambda does not require the Discord bot token. The token is used only by
the explicit command-registration script and must not be committed or placed in
Terraform variables.

## Initial technology choices

- AWS
- EC2
- EBS and S3
- Terraform
- Go
- DynamoDB
- AWS Secrets Manager
- EventBridge
- AWS Lambda
- DiscordGo for future Discord REST and follow-up operations
- GitHub Actions

## Terminology

- Deployment: A persistent game-server configuration and lifecycle record
- Server: A temporary compute instance
- Mission: Arma-specific mission content
- Deployment Archive: A long-term backup of a deployment
