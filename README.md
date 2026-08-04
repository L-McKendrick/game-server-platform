# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Foundation, core metadata, and command idempotency implemented.
Next milestone: Discord interaction verification and metadata commands.

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
- DiscordGo
- GitHub Actions

## Terminology

- Deployment: A persistent game-server configuration and lifecycle record
- Server: A temporary compute instance
- Mission: Arma-specific mission content
- Deployment Archive: A long-term backup of a deployment
