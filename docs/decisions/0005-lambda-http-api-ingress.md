# ADR 0005: Deploy Discord ingress with Lambda and API Gateway HTTP API

- Status: Accepted
- Date: 2026-08-03

## Context

The Discord interaction adapter is already implemented and tested as a standard
`net/http` handler. The platform now needs a public HTTPS endpoint that preserves
the exact request body and signature headers while keeping AWS transport details
outside the Discord adapter and application layer.

The initial commands are synchronous metadata operations. They do not require
Step Functions, SQS, EC2, or a long-running server.

## Decision

Deploy a Go Lambda function behind an API Gateway HTTP API route:

```text
POST /discord/interactions
```

Use API Gateway Lambda proxy payload format `2.0`. A small AWS adapter converts
`events.APIGatewayV2HTTPRequest` into an `http.Request`, invokes the existing
Discord handler, and converts the result into
`events.APIGatewayV2HTTPResponse`.

The Lambda composition root uses:

- AWS SDK for Go v2;
- the existing DynamoDB repository;
- the existing session application service;
- the existing Discord interaction handler;
- the `provided.al2023` Lambda runtime;
- an `arm64` deployment package named `bootstrap`.

The public Discord verification key, application ID, and development guild IDs
are Lambda environment configuration. The Discord bot token is not used by the
runtime handler and remains outside Terraform configuration.

The execution role is limited to:

- `dynamodb:GetItem` on the metadata table;
- `dynamodb:TransactWriteItems` on the metadata table;
- `dynamodb:Query` on the owner GSI;
- log-stream creation and log-event writes for the function log group.

API Gateway may invoke only the Discord interaction function through the
configured route. API access logs exclude bodies, headers, signatures, and
tokens.

## Consequences

- Discord can validate and use a real HTTPS endpoint.
- The exact signed request body remains intact across the transport adapter.
- The domain, application service, and Discord adapter remain independent of
  Lambda and API Gateway event types.
- Deployment requires a prebuilt ZIP package before Terraform planning.
- The first apply creates billable serverless resources and CloudWatch log
  storage, although expected personal-development usage is low.
- Bot-token use remains limited to an explicit local command-registration step.
- Long-running commands still require a later asynchronous workflow slice.
