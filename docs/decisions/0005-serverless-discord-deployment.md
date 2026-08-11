# ADR 0005: Serverless Discord interaction deployment

## Status

Accepted.

## Context

Discord requires a public interaction endpoint that acknowledges signed commands quickly. The local interaction server used process memory and could not provide durable metadata or a production-compatible HTTP boundary. Attachments can be large enough that downloading them during an interaction risks exceeding Discord acknowledgement time and couples untrusted input processing to the public handler.

## Decision

- Deploy the existing `net/http` interaction handler as a Go custom-runtime Lambda behind API Gateway HTTP API payload format 2.0.
- Decode API Gateway base64 bodies exactly once and pass the resulting raw bytes to signature verification unchanged.
- Compose the Lambda with the DynamoDB session repository.
- Validate attachment metadata in the interaction path and enqueue versioned requests on a FIFO SQS queue.
- Use the session ID as the FIFO message group and the Discord interaction ID as the deduplication key.
- Defer downloading, file-format validation, S3 persistence, and user notification to the Phase 4 worker.
- Register development commands through Discord's guild bulk-overwrite endpoint using a token supplied only through the registration process environment.

## Consequences

The public handler remains bounded and idempotent. Attachment acceptance does not mean the file has passed validation; Discord explicitly reports that validation is pending. Phase 4 must consume the queue and update metadata only after successful content validation and S3 persistence.
