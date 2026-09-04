# Phase 6: Arma Bootstrap

Phase 6 turns a managed `BOOTSTRAPPING` instance into a playable Arma 3 server. `/session start` routes provisioned sessions to the real `BootstrapGameServer` workflow; `NEW` sessions continue to use `ProvisionSession`.

## Resumable control flow

1. The command worker acquires the per-session bootstrap lease and starts Step Functions.
2. The bootstrap worker changes metadata to `INSTALLING` and dispatches one bounded Systems Manager command.
3. The command downloads the content-addressed host script from the private assets bucket, prepares and mounts the persistent data volume, then serializes work with a host lock.
4. Durable markers skip completed SteamCMD, the Arma `creatordlc` server branch, Workshop, content, and optional TeamSpeak stages on retry.
5. The final service and UDP health gate always reruns; it is never satisfied by an old marker.
6. Step Functions polls Systems Manager without holding a Lambda invocation open.
7. Success records `RUNNING`/`HEALTHY` and notifies Discord. Failure records `FAILED`, retains infrastructure and markers, and remains retryable.

Command dispatch is intentionally single-attempt because Systems Manager Run Command has no caller idempotency token. A transient dispatch failure fails closed and is retried by running `/session start` again; it cannot create two concurrent installers.

## Credential and content handling

- Modded downloads use the Phase 10 versioned SteamCMD `config.vdf`
  authorization cache and username-only login. See `docs/steam-auth-cache.md`.
- Passwords and Steam Guard codes are entered only into an isolated operator
  SteamCMD session and never enter Discord, AWS workflows, Lambda settings,
  Systems Manager command text, persistent logs, archives, or session volumes.
- The instance injects the encrypted cache only through `/run`, persists valid
  SteamCMD cache updates under a global lease, and scrubs every authentication
  path before game launch or exit.
- The non-secret bootstrap script is deployed as a content-addressed, versioned S3 artifact; its short SSM launcher is kept below 4 KiB.
- Mission input is always read from the session-scoped S3 prefix. Modded
  sessions require client Workshop content, Creator DLC, or a server-only
  preset; explicitly configured vanilla sessions require none of these. SSM
  output is stored under the session log prefix.
- Vanilla sessions use the same cached Steam authorization as modded sessions for the Arma 3 dedicated-server app, do not select the Creator DLC beta branch, and skip preset and Workshop processing entirely.
- Steam Guard challenges fail closed with `ERR_STEAM_REAUTH_REQUIRED` and
  require the local MFA-gated operator enrollment procedure before retry.
- Workshop downloads use workflow-isolated Steam libraries under
  `/srv/game-server/workshop-staging`, then validated content is copied into
  revision-owned persistent directories. A live staged revision is reused on
  wake when its bounded item markers and trees remain valid. Client mods are
  passed with `-mod=`; separately revisioned server-only mods use `-serverMod=`
  and do not enter the downloadable client preset. See
  `docs/workshop-content-sources.md` for source, collection, and recovery rules.

For a vanilla session, choose Vanilla in `/rb create` or `/rb setup`, upload
only the mission, and then use `/rb start`. The vanilla selection is immutable
after the session leaves `DRAFT`, like the rest of the session configuration.

## Host layout and health boundary

The persistent volume mounts at `/srv/game-server`. Arma, SteamCMD, Workshop content, configuration, logs, swap, and stage markers live there. Arma runs as the unprivileged `steam` user under systemd. TeamSpeak 3.13.8 is installed only when enabled and runs as a separate user. No SSH or public query administration port is opened.

Game hosts use the official Canonical Ubuntu Server 24.04 LTS AMI. Bootstrap enables the `i386` package architecture and installs the 32-bit C/C++ runtime required by SteamCMD. The short SSM launcher installs AWS CLI v2 from the official distribution when the image does not already provide it.

Phase 6 marks the session playable only when the Arma service is active and UDP `2302` is listening; optional TeamSpeak also requires UDP `9987`. Phase 7 adds continuous player, process, and application-level monitoring.
