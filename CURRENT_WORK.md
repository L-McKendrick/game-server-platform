# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is proceeding
before them by explicit user-approved roadmap reorder because current limitations
prevent completing those prerequisites; this does not mark either prerequisite
phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.3 are
complete.

## Step 12.3 Verification

- Registered `/rb create` is now an optionless, authorized modal launcher; it no
  longer persists a session from slash-command options.
- The private modal has exactly five top-level labels: required session name,
  optional 64-character description, combined Modded/TeamSpeak choices, required
  mission upload, and optional preset upload. Modded is selected by default,
  TeamSpeak is off, and clearing Modded expresses vanilla intent.
- Sleep and archive are intentionally absent from the modal and retain the
  established platform defaults of 30 minutes and 7 days.
- Creation submissions strictly validate the five-field schema, normalize name
  and description, resolve one required mission and at most one optional preset,
  and validate extension, size, and Discord CDN source before persistence.
- Valid submissions idempotently create and configure one draft, then enqueue
  mission and optional preset validation under distinct replay-safe keys. The
  private response says the uploads are queued and not yet validated; a modded
  draft without a preset remains recoverable and is told what is missing.
- Creation queues one public setup card before artifact work. Card delivery uses
  the existing per-channel FIFO notification path and Discord's enforced nonce
  uniqueness, so retries create exactly one bot-authored message.
- The card channel/message reference is stored in a separate DynamoDB item under
  the session partition. Lifecycle and artifact writes cannot overwrite it, and
  storing it does not increment the session version.
- Mission and preset validation outcomes are durable. Accepted or rejected
  outcomes refresh the same public card, rejected drafts remain recoverable,
  and a submitted optional vanilla preset must finish validation before the
  otherwise-ready draft advances.
- Registered `/rb setup` replaces the public configure/upload commands with a
  draft-only recovery modal. It pre-fills name, description, mode, and
  TeamSpeak, preserves the stable slug and policy defaults, and refreshes the
  existing setup card.
- Setup changes identity, configuration, and replacement-pending state in one
  replay-safe mutation. Only missing or rejected artifacts can be replaced;
  accepted, legacy-object-backed, and already-pending artifacts are protected.
- Creation and setup inspect Discord's signed `app_permissions` capability
  snapshot both before opening and when submitting a modal. Creation stops
  before persistence when the required public card cannot be sent, and setup
  stops before mutation when the existing card cannot be accessed for editing.
- Missing optional component, embed, or attachment capability keeps the card
  on its bounded, mention-safe plain-text projection and tells the user about
  the degradation. Omitted permissions remain compatible with older captured
  payloads; malformed permission values fail closed.
- The recovery matrix covers duplicate submissions, inclusive and rejected
  modal limits, vanilla and modded requirements, mixed artifact outcomes,
  Discord card-delivery retry, and setup replay/replacement recovery.
- Legacy object-backed artifacts are inferred as accepted on DynamoDB reads,
  remain readiness-compatible, render correctly, and cannot be replaced by
  the setup flow even when the additive status fields are absent.
- Repository-wide tests, coverage, `go vet`, command builds, Lambda packaging,
  command-registration checks, touched-file Terraform formatting, Terraform
  validation, and `git diff --check` pass. The race detector cannot run in the
  current Windows environment because CGO is disabled.

## Operator Attention

The updated Discord handler, artifact worker, notification worker, Terraform,
and `/rb` command definition are not deployed. Live-guild use requires applying
the reviewed IAM/environment changes, repackaging and deploying those Lambdas,
and bulk guild command registration. The current card is the setup projection;
the full lifecycle projection remains scoped to Step 12.4.

## Exact Next Step

Implement only task 12.4.1: define one authoritative card projection for name,
slug, description, lifecycle, health, stage, elapsed time, players, endpoints,
mods, failures, and data freshness. Do not begin task 12.4.2.
