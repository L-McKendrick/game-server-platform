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

- Steam credentials use JSON keys `username` and `password` in `/game-server-platform/dev/steam-credentials`.
- Terraform creates only the secret container; values never enter state, environment variables, workflow input, or logs.
- The instance retrieves the secret at runtime. A mode-`0600` SteamCMD runscript is deleted on every exit.
- First-login Steam Guard authorization is performed as a one-time operator action on the managed host. Any temporary `steam_guard_code` secret field must be removed after authorization; normal bootstrap reads only `username` and `password`.
- The non-secret bootstrap script is deployed as a content-addressed, versioned S3 artifact; its short SSM launcher is kept below 4 KiB.
- Mission input is always read from the session-scoped S3 prefix. Modded sessions also require a launcher preset; explicitly configured vanilla sessions require no preset. SSM output is stored under the session log prefix.
- Vanilla sessions use `login anonymous` with the public Arma 3 dedicated-server app, do not select the Creator DLC beta branch, and skip Steam secret retrieval and Workshop processing entirely.
- Steam Guard challenges fail closed and require explicit account authorization before retry.
- Workshop content is read from the authenticated Steam user's persistent library under `/srv/game-server/home/Steam/steamapps/workshop`.

For a vanilla session, set `vanilla:true` on `/session configure`, upload only
the mission, and then use `/session start`. The vanilla selection is immutable
after the session leaves `DRAFT`, like the rest of the session configuration.

## Host layout and health boundary

The persistent volume mounts at `/srv/game-server`. Arma, SteamCMD, Workshop content, configuration, logs, swap, and stage markers live there. Arma runs as the unprivileged `steam` user under systemd. TeamSpeak 3.13.8 is installed only when enabled and runs as a separate user. No SSH or public query administration port is opened.

Game hosts use the official Canonical Ubuntu Server 24.04 LTS AMI. Bootstrap enables the `i386` package architecture and installs the 32-bit C/C++ runtime required by SteamCMD. The short SSM launcher installs AWS CLI v2 from the official distribution when the image does not already provide it.

Phase 6 marks the session playable only when the Arma service is active and UDP `2302` is listening; optional TeamSpeak also requires UDP `9987`. Phase 7 adds continuous player, process, and application-level monitoring.
