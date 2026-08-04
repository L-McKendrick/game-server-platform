# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Foundation, core metadata, command idempotency, and the local Discord interaction boundary are implemented.

The Discord adapter currently provides:

- Ed25519 verification against the exact raw request body;
- timestamp freshness, request-size, application-ID, and guild validation;
- Discord `PING` acknowledgement;
- `/session create`, `/session list`, and `/session status`;
- ephemeral responses with mentions disabled;
- command idempotency derived from the Discord interaction ID;
- a local in-memory HTTP composition root for transport testing.

Next milestone: deploy the interaction handler through API Gateway HTTP API and Lambda, compose it with DynamoDB, and register the command in one development guild.

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

The versioned development command definition is stored at:

```text
deploy/discord/session-command.json
```

Do not register the endpoint or command until the Lambda/API Gateway deployment slice is complete.

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
