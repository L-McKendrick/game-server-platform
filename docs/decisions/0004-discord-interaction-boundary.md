# ADR 0004: Discord interaction boundary

- Status: Accepted
- Date: 2026-08-03

## Context

The metadata layer now supports atomic events, optimistic concurrency, and durable command idempotency. The next vertical slice needs to accept Discord application commands without coupling the domain or application layers to Discord or AWS Lambda.

Discord HTTP interactions have security requirements that must be enforced against the exact raw request body before JSON parsing. The platform also needs a small synchronous command set that can complete within Discord's initial response window while long-running infrastructure commands remain deferred to later workflow slices.

## Decision

Add an HTTP adapter under `internal/adapters/discord/interactions` with these responsibilities:

1. accept only `POST` requests with JSON bodies;
2. enforce a bounded request size;
3. validate the Ed25519 signature over `timestamp + raw body`;
4. reject timestamps outside the configured freshness window;
5. validate the Discord application ID and approved guild ID;
6. acknowledge Discord `PING` interactions;
7. map `/session create`, `/session list`, and `/session status` to the existing session application service;
8. derive command idempotency from `discord:<interaction-id>`;
9. return ephemeral responses with mentions disabled;
10. log correlation IDs and failure categories without logging interaction tokens or raw bodies.

The adapter uses small internal protocol structures and the Go standard library. It does not import AWS packages, call Discord REST APIs, or provision infrastructure.

A local composition root is added at `cmd/discord-local`. It uses the in-memory repository and exists only for local transport testing. Durable deployment remains a separate slice.

The development-guild command contract is versioned at `deploy/discord/session-command.json` but is not registered automatically in this slice.

## Consequences

### Positive

- Signature and timestamp validation are isolated and unit-testable.
- Domain and application packages remain independent of Discord and AWS.
- Existing metadata idempotency prevents duplicate session creation when Discord retries an interaction.
- PING, create, list, and status behavior can be verified without deploying cloud resources.
- The same `http.Handler` can later be wrapped by an API Gateway/Lambda transport adapter.

### Negative

- The local server stores data only in memory and resets on restart.
- The command is not usable from Discord until API Gateway, Lambda, IAM, packaging, and command registration are implemented.
- Attachments, configuration, deferred responses, follow-up messages, role authorization, and long-running lifecycle commands remain out of scope.

## Follow-up

The next slice will deploy this handler behind an API Gateway HTTP API and Go Lambda, compose it with the DynamoDB repository, provide least-privilege IAM, package it in CI, and register the command in one development guild after reviewing the Terraform plan.
