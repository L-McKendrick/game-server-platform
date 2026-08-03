# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Phase 1: Foundation

## Initial technology choices

- AWS
- EC2
- EBS and S3
- Terraform
- Go
- SQLite
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

## Run locally

```bash
go run ./cmd/platform

---

# Part 10 — Format and test the project

Run:

```bash
go fmt ./...