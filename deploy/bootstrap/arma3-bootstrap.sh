#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

decode() { printf '%s' "$1" | base64 -d; }
SESSION_ID="$(decode "$SESSION_ID_B64")"
DISPLAY_NAME="$(decode "$DISPLAY_NAME_B64")"
DATA_VOLUME_ID="$(decode "$DATA_VOLUME_ID_B64")"
MISSION_KEY="$(decode "$MISSION_KEY_B64")"
MISSION_TEMPLATE="$(decode "$MISSION_TEMPLATE_B64")"
SERVER_CONFIG_KEY="$(decode "$SERVER_CONFIG_KEY_B64")"
SERVER_CONFIG_SHA256="$(decode "$SERVER_CONFIG_SHA_B64")"
SERVER_CONFIG_REVISION="$(decode "$SERVER_CONFIG_REV_B64")"
PRESET_KEY="$(decode "$PRESET_KEY_B64")"
PRESET_REVISION="$(decode "$PRESET_REVISION_B64")"
PRESET_ROLLBACK="$(decode "$PRESET_ROLLBACK_B64")"
CREATOR_DLC_MODS="$(decode "$CREATOR_DLC_MODS_B64")"
MOD_CONFIG_REVISION="$(decode "$MOD_CONFIG_REVISION_B64")"
ASSETS_BUCKET="$(decode "$ASSETS_BUCKET_B64")"
METADATA_TABLE="$(decode "$METADATA_TABLE_B64")"
STEAM_AUTH_SECRET_ID="$(decode "$STEAM_AUTH_SECRET_B64")"
AWS_REGION="$(decode "$AWS_REGION_B64")"
TEAMSPEAK_VERSION="$(decode "$TEAMSPEAK_VERSION_B64")"
: "${VANILLA_MODE:=false}"
ROOT=/srv/game-server
STATE_DIR="$ROOT/state"
LOG_DIR="$ROOT/logs"
STEAM_AUTH_ROOT=""
STEAM_AUTH_LOCK_OWNER=""
STEAM_AUTH_USERNAME=""
STEAM_AUTH_SOURCE_VERSION=""
STEAM_AUTH_ENROLLED_AT=""
STEAM_AUTH_INITIAL_SHA=""
STEAM_AUTH_ACTIVE=false
STEAM_AUTH_VALID=false
STEAM_AUTH_FINALIZED=false
STEAM_AUTH_PERSIST_ATTEMPTED=false
STEAM_AUTH_LOCK_HEARTBEAT_PID=""
STEAM_AUTH_LOCK_LEASE_SECONDS=900
STEAM_AUTH_LOCK_HEARTBEAT_SECONDS=300

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
checkpoint() { printf 'GSP_CHECKPOINT:%s\n' "$1"; }

prepare_host() {
  command -v apt-get >/dev/null 2>&1 || { log "bootstrap requires the approved Ubuntu game-host image"; return 1; }
  dpkg --add-architecture i386
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y bzip2 ca-certificates curl findutils gzip jq libc6:i386 libgcc-s1:i386 libstdc++6:i386 tar util-linux xfsprogs
  local serial="${DATA_VOLUME_ID//-/}"
  local device=""
  for _ in $(seq 1 30); do
    device="$(lsblk -ndo PATH,SERIAL | awk -v serial="$serial" '$2 == serial {print $1; exit}')"
    [ -n "$device" ] && break
    sleep 2
  done
  [ -b "$device" ] || { log "persistent EBS device not found"; return 1; }
  if ! blkid "$device" >/dev/null 2>&1; then mkfs.xfs -f "$device"; fi
  mkdir -p "$ROOT"
  local uuid
  uuid="$(blkid -s UUID -o value "$device")"
  grep -q "UUID=$uuid " /etc/fstab || printf 'UUID=%s %s xfs defaults,nofail 0 2\n' "$uuid" "$ROOT" >> /etc/fstab
  mountpoint -q "$ROOT" || mount "$ROOT"
  mkdir -p "$STATE_DIR" "$LOG_DIR" "$ROOT/config" "$ROOT/arma3" "$ROOT/steamcmd" "$ROOT/workshop"
  id steam >/dev/null 2>&1 || useradd --home-dir "$ROOT/home" --create-home --shell /bin/bash steam
  chown -R steam:steam "$ROOT/home" "$ROOT/arma3" "$ROOT/steamcmd" "$ROOT/workshop" "$LOG_DIR"
  if [ ! -f "$ROOT/swapfile" ]; then
    dd if=/dev/zero of="$ROOT/swapfile" bs=1M count=4096 status=none
    chmod 600 "$ROOT/swapfile"
    mkswap "$ROOT/swapfile" >/dev/null
  fi
  swapon --show=NAME | grep -qx "$ROOT/swapfile" || swapon "$ROOT/swapfile"
  scrub_persistent_steam_auth
}

scrub_persistent_steam_auth() {
  local path
  for path in "$ROOT/steamcmd/config" "$ROOT/home/Steam/config" "$ROOT/home/.local/share/Steam/config"; do
    if [ -L "$path" ]; then
      rm -f -- "$path"
    elif [ -d "$path" ]; then
      rm -f -- "$path/config.vdf" "$path/loginusers.vdf"
      find "$path" -maxdepth 1 -type f -name 'ssfn*' -delete
    fi
  done
  for path in "$ROOT/steamcmd" "$ROOT/home/Steam" "$ROOT/home/.local/share/Steam"; do
    [ -d "$path" ] || continue
    find "$path" -maxdepth 1 -type f \( -name 'ssfn*' -o -name 'loginusers.vdf' \) -delete
  done
  rm -rf -- "$ROOT/steamcmd/logs" "$ROOT/home/Steam/logs" "$ROOT/home/.local/share/Steam/logs"
}

steam_auth_key() { printf '{"pk":{"S":"STEAM_AUTH#CACHE"},"sk":{"S":"STATE"}}'; }

stop_steam_auth_lock_heartbeat() {
  [ -n "$STEAM_AUTH_LOCK_HEARTBEAT_PID" ] || return 0
  kill "$STEAM_AUTH_LOCK_HEARTBEAT_PID" >/dev/null 2>&1 || true
  wait "$STEAM_AUTH_LOCK_HEARTBEAT_PID" 2>/dev/null || true
  STEAM_AUTH_LOCK_HEARTBEAT_PID=""
}

release_steam_auth_lock() {
  stop_steam_auth_lock_heartbeat
  [ -n "$STEAM_AUTH_LOCK_OWNER" ] || return 0
  local values
  values="$(jq -cn --arg owner "$STEAM_AUTH_LOCK_OWNER" '{":owner":{"S":$owner}}')"
  aws dynamodb update-item --region "$AWS_REGION" --table-name "$METADATA_TABLE" --key "$(steam_auth_key)" \
    --update-expression 'REMOVE lease_owner, lease_expires_at' \
    --condition-expression 'lease_owner = :owner' --expression-attribute-values "$values" >/dev/null 2>&1 || true
  STEAM_AUTH_LOCK_OWNER=""
}

mark_steam_reauthorization_required() {
  local values now
  stop_steam_auth_lock_heartbeat
  [ -n "$STEAM_AUTH_LOCK_OWNER" ] || return 0
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  values="$(jq -cn --arg owner "$STEAM_AUTH_LOCK_OWNER" --arg now "$now" '{":owner":{"S":$owner},":status":{"S":"REAUTH_REQUIRED"},":code":{"S":"ERR_STEAM_REAUTH_REQUIRED"},":now":{"S":$now}}')"
  aws dynamodb update-item --region "$AWS_REGION" --table-name "$METADATA_TABLE" --key "$(steam_auth_key)" \
    --update-expression 'SET #status = :status, last_error_code = :code, updated_at = :now REMOVE lease_owner, lease_expires_at' \
    --condition-expression 'lease_owner = :owner' --expression-attribute-names '{"#status":"status"}' \
    --expression-attribute-values "$values" >/dev/null 2>&1 || true
  STEAM_AUTH_LOCK_OWNER=""
}

refresh_steam_auth_lock() {
  local expires_epoch values
  [ -n "$STEAM_AUTH_LOCK_OWNER" ] || return 1
  expires_epoch="$(($(date -u +%s) + STEAM_AUTH_LOCK_LEASE_SECONDS))"
  values="$(jq -cn --arg owner "$STEAM_AUTH_LOCK_OWNER" --argjson expires "$expires_epoch" '{":owner":{"S":$owner},":expires":{"N":($expires|tostring)}}')"
  aws dynamodb update-item --region "$AWS_REGION" --table-name "$METADATA_TABLE" --key "$(steam_auth_key)" \
    --update-expression 'SET lease_expires_at = :expires' \
    --condition-expression 'lease_owner = :owner' --expression-attribute-values "$values" >/dev/null 2>&1
}

start_steam_auth_lock_heartbeat() {
  local bootstrap_pid="$$"
  (
    trap 'exit 0' INT TERM
    while sleep "$STEAM_AUTH_LOCK_HEARTBEAT_SECONDS"; do
      if ! refresh_steam_auth_lock; then
        sleep 5
        if ! refresh_steam_auth_lock; then
          log "Steam authorization lease renewal failed; stopping bootstrap"
          kill -TERM "$bootstrap_pid" >/dev/null 2>&1 || true
          exit 1
        fi
      fi
    done
  ) &
  STEAM_AUTH_LOCK_HEARTBEAT_PID="$!"
}

acquire_steam_auth_lock() {
  local now_epoch expires_epoch values state_file status
  STEAM_AUTH_LOCK_OWNER="$SESSION_ID:$$:$(cat /proc/sys/kernel/random/uuid)"
  now_epoch="$(date -u +%s)"
  expires_epoch="$((now_epoch + STEAM_AUTH_LOCK_LEASE_SECONDS))"
  values="$(jq -cn --arg owner "$STEAM_AUTH_LOCK_OWNER" --argjson now "$now_epoch" --argjson expires "$expires_epoch" '{":owner":{"S":$owner},":now":{"N":($now|tostring)},":expires":{"N":($expires|tostring)}}')"
  state_file="$STEAM_AUTH_ROOT/lock.json"
  if ! aws dynamodb update-item --region "$AWS_REGION" --table-name "$METADATA_TABLE" --key "$(steam_auth_key)" \
    --update-expression 'SET lease_owner = :owner, lease_expires_at = :expires' \
    --condition-expression 'attribute_not_exists(lease_owner) OR lease_expires_at < :now OR lease_owner = :owner' \
    --expression-attribute-values "$values" --return-values ALL_NEW >"$state_file" 2>/dev/null; then
    STEAM_AUTH_LOCK_OWNER=""
    log "Steam authorization cache is busy; retry after the active download finishes"
    return 1
  fi
  status="$(jq -r '.Attributes.status.S // ""' "$state_file")"
  if [ "$status" = REAUTH_REQUIRED ]; then
    release_steam_auth_lock
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization requires operator re-enrollment.\n' >&2
    return 42
  fi
  start_steam_auth_lock_heartbeat
}

link_ephemeral_steam_path() {
  local path="$1" target="$2"
  rm -rf -- "$path"
  mkdir -p "$(dirname "$path")"
  ln -s "$target" "$path"
}

begin_steam_auth() {
  local response payload config_b64 expected_sha actual_sha config_size
  [ -n "$METADATA_TABLE" ] && [ -n "$STEAM_AUTH_SECRET_ID" ] || { printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache is not configured.\n' >&2; return 42; }
  STEAM_AUTH_ROOT="$(mktemp -d /run/gsp-steam-auth.XXXXXX)"
  chmod 700 "$STEAM_AUTH_ROOT"
  STEAM_AUTH_ACTIVE=true
  acquire_steam_auth_lock
  response="$STEAM_AUTH_ROOT/secret-response.json"
  payload="$STEAM_AUTH_ROOT/cache.json"
  if ! aws secretsmanager get-secret-value --region "$AWS_REGION" --secret-id "$STEAM_AUTH_SECRET_ID" --version-stage AWSCURRENT >"$response" 2>/dev/null; then
    if aws secretsmanager describe-secret --region "$AWS_REGION" --secret-id "$STEAM_AUTH_SECRET_ID" --query 'VersionIdsToStages' --output json 2>/dev/null | jq -e 'any(.[]; index("AWSCURRENT"))' >/dev/null 2>&1; then
      release_steam_auth_lock
      log "Steam authorization cache could not be read"
      return 1
    fi
    mark_steam_reauthorization_required
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization requires operator enrollment.\n' >&2
    return 42
  fi
  jq -er '.SecretString | fromjson | select(.schema_version == 1 and .cache_format == "steamcmd-config-vdf" and .status == "ACTIVE")' "$response" >"$payload" || {
    mark_steam_reauthorization_required
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache is invalid.\n' >&2
    return 42
  }
  if ! STEAM_AUTH_USERNAME="$(jq -er '.username | select(type == "string" and length >= 1 and length <= 64 and test("^[^[:space:]\"\\\\]+$"))' "$payload")"; then
    mark_steam_reauthorization_required
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization identity is invalid.\n' >&2
    return 42
  fi
  if ! config_b64="$(jq -er '.config_vdf_base64 | select(type == "string" and length > 0 and length <= 1048576)' "$payload")" ||
     ! expected_sha="$(jq -er '.config_sha256 | select(type == "string" and test("^[0-9a-f]{64}$"))' "$payload")" ||
     ! STEAM_AUTH_ENROLLED_AT="$(jq -er '.enrolled_at | select(type == "string" and length > 0 and length <= 64)' "$payload")" ||
     ! STEAM_AUTH_SOURCE_VERSION="$(jq -er '.VersionId | select(type == "string" and length > 0 and length <= 128)' "$response")"; then
    mark_steam_reauthorization_required
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache fields are invalid.\n' >&2
    return 42
  fi
  STEAM_AUTH_INITIAL_SHA="$expected_sha"
  mkdir -p "$STEAM_AUTH_ROOT/config" "$STEAM_AUTH_ROOT/logs" "$STEAM_AUTH_ROOT/home/Steam" "$STEAM_AUTH_ROOT/home/.local/share/Steam"
  if ! printf '%s' "$config_b64" | base64 -d >"$STEAM_AUTH_ROOT/config/config.vdf"; then
    mark_steam_reauthorization_required
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache encoding is invalid.\n' >&2
    return 42
  fi
  unset config_b64
  config_size="$(stat -c '%s' "$STEAM_AUTH_ROOT/config/config.vdf")"
  [ "$config_size" -gt 0 ] && [ "$config_size" -le 524288 ] || { mark_steam_reauthorization_required; printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache size is invalid.\n' >&2; return 42; }
  actual_sha="$(sha256sum "$STEAM_AUTH_ROOT/config/config.vdf" | awk '{print $1}')"
  [ "$actual_sha" = "$expected_sha" ] || { mark_steam_reauthorization_required; printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache checksum failed.\n' >&2; return 42; }
  mkdir -p "$ROOT/home/Steam/steamapps"
  ln -s "$ROOT/home/Steam/steamapps" "$STEAM_AUTH_ROOT/home/Steam/steamapps"
  ln -s "$ROOT/home/Steam/steamapps" "$STEAM_AUTH_ROOT/home/.local/share/Steam/steamapps"
  ln -s "$STEAM_AUTH_ROOT/config" "$STEAM_AUTH_ROOT/home/Steam/config"
  ln -s "$STEAM_AUTH_ROOT/config" "$STEAM_AUTH_ROOT/home/.local/share/Steam/config"
  link_ephemeral_steam_path "$ROOT/steamcmd/config" "$STEAM_AUTH_ROOT/config"
  link_ephemeral_steam_path "$ROOT/steamcmd/logs" "$STEAM_AUTH_ROOT/logs"
  link_ephemeral_steam_path "$ROOT/home/Steam/config" "$STEAM_AUTH_ROOT/config"
  link_ephemeral_steam_path "$ROOT/home/Steam/logs" "$STEAM_AUTH_ROOT/logs"
  link_ephemeral_steam_path "$ROOT/home/.local/share/Steam/config" "$STEAM_AUTH_ROOT/config"
  link_ephemeral_steam_path "$ROOT/home/.local/share/Steam/logs" "$STEAM_AUTH_ROOT/logs"
  chown -R steam:steam "$STEAM_AUTH_ROOT" "$ROOT/home/Steam"
}

persist_steam_auth() {
  local config_file config_size config_sha config_b64 payload result version now token values
  $STEAM_AUTH_ACTIVE || return 0
  $STEAM_AUTH_VALID || return 0
  STEAM_AUTH_PERSIST_ATTEMPTED=true
  config_file="$STEAM_AUTH_ROOT/config/config.vdf"
  [ -f "$config_file" ] || { log "Steam authorization cache update is missing"; return 1; }
  config_size="$(stat -c '%s' "$config_file")"
  [ "$config_size" -gt 0 ] && [ "$config_size" -le 524288 ] || { log "Steam authorization cache update exceeded its bound"; return 1; }
  config_sha="$(sha256sum "$config_file" | awk '{print $1}')"
  config_b64="$(base64 -w0 "$config_file")"
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload="$STEAM_AUTH_ROOT/updated-cache.json"
  jq -cn --arg username "$STEAM_AUTH_USERNAME" --arg config "$config_b64" --arg sha "$config_sha" --arg enrolled "$STEAM_AUTH_ENROLLED_AT" --arg updated "$now" --arg source "$STEAM_AUTH_SOURCE_VERSION" \
    '{schema_version:1,cache_format:"steamcmd-config-vdf",status:"ACTIVE",username:$username,config_vdf_base64:$config,config_sha256:$sha,enrolled_at:$enrolled,updated_at:$updated,source_version_id:$source}' >"$payload"
  unset config_b64
  if [ "$config_sha" = "$STEAM_AUTH_INITIAL_SHA" ]; then
    version="$STEAM_AUTH_SOURCE_VERSION"
  else
    result="$STEAM_AUTH_ROOT/put-result.json"
    token="$(cat /proc/sys/kernel/random/uuid)"
    aws secretsmanager put-secret-value --region "$AWS_REGION" --secret-id "$STEAM_AUTH_SECRET_ID" --client-request-token "$token" --secret-string "file://$payload" >"$result" 2>/dev/null || { log "Steam authorization cache update failed"; return 1; }
    version="$(jq -er '.VersionId' "$result")"
  fi
  values="$(jq -cn --arg owner "$STEAM_AUTH_LOCK_OWNER" --arg status ACTIVE --arg version "$version" --arg sha "$config_sha" --arg now "$now" '{":owner":{"S":$owner},":status":{"S":$status},":version":{"S":$version},":sha":{"S":$sha},":now":{"S":$now}}')"
  aws dynamodb update-item --region "$AWS_REGION" --table-name "$METADATA_TABLE" --key "$(steam_auth_key)" \
    --update-expression 'SET #status = :status, current_version_id = :version, config_sha256 = :sha, updated_at = :now REMOVE last_error_code' \
    --condition-expression 'lease_owner = :owner' --expression-attribute-names '{"#status":"status"}' \
    --expression-attribute-values "$values" >/dev/null 2>&1 || { log "Steam authorization state update failed"; return 1; }
  STEAM_AUTH_FINALIZED=true
}

cleanup_steam_auth() {
  scrub_persistent_steam_auth
  release_steam_auth_lock
  [ -z "$STEAM_AUTH_ROOT" ] || rm -rf -- "$STEAM_AUTH_ROOT"
  STEAM_AUTH_ROOT=""
  STEAM_AUTH_ACTIVE=false
  STEAM_AUTH_USERNAME=""
  STEAM_AUTH_INITIAL_SHA=""
}

steam_auth_exit() {
  local code=$?
  trap - EXIT INT TERM
  if $STEAM_AUTH_ACTIVE; then
    if $STEAM_AUTH_VALID && ! $STEAM_AUTH_FINALIZED && ! $STEAM_AUTH_PERSIST_ATTEMPTED; then persist_steam_auth || code=1; fi
    cleanup_steam_auth
  fi
  exit "$code"
}
trap steam_auth_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

steam_login_file() {
  local target="$1" escaped_user
  $STEAM_AUTH_ACTIVE || { printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization cache is not active.\n' >&2; return 42; }
  escaped_user="${STEAM_AUTH_USERNAME//\\/\\\\}"; escaped_user="${escaped_user//\"/\\\"}"
  printf 'login "%s"\n' "$escaped_user" >> "$target"
  unset escaped_user
  chmod 600 "$target"
  chown steam:steam "$target"
}

run_steamcmd() {
  local runfile="$1" output_file code
  output_file="${STEAM_AUTH_ROOT:-/run}/steamcmd-output.$$.log"
  set +e
  runuser -u steam -- env HOME="$STEAM_AUTH_ROOT/home" "$ROOT/steamcmd/steamcmd.sh" +runscript "$runfile" >"$output_file" 2>&1
  code=$?
  set -e
  if grep -Eqi 'Steam Guard|two[- ]factor|Account Logon Denied|InvalidPassword|Invalid Password|login failure|password required' "$output_file"; then
    STEAM_AUTH_VALID=false
    mark_steam_reauthorization_required
    rm -f -- "$output_file"
    printf 'ERR_STEAM_REAUTH_REQUIRED: Steam authorization requires operator re-enrollment.\n' >&2
    return 42
  fi
  if grep -Eqi 'Logged in OK|Waiting for user info.*OK' "$output_file"; then STEAM_AUTH_VALID=true; fi
  if [ "$code" -eq 0 ]; then STEAM_AUTH_VALID=true; fi
  rm -f -- "$output_file"
  if [ "$code" -ne 0 ]; then
    log "SteamCMD download failed without exposing its raw output"
    return "$code"
  fi
}

install_steamcmd() {
  if [ ! -x "$ROOT/steamcmd/steamcmd.sh" ]; then
    runuser -u steam -- curl --fail --location --silent --show-error https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz -o "$ROOT/steamcmd/steamcmd.tar.gz"
    runuser -u steam -- tar -xzf "$ROOT/steamcmd/steamcmd.tar.gz" -C "$ROOT/steamcmd"
    rm -f "$ROOT/steamcmd/steamcmd.tar.gz"
  fi
}

install_arma() (
  local runfile
  runfile="$(mktemp /run/gsp-steam.XXXXXX)"
  trap 'rm -f "$runfile"' EXIT
  printf 'force_install_dir %s\n' "$ROOT/arma3" > "$runfile"
  steam_login_file "$runfile"
  if [ "$VANILLA_MODE" = true ]; then
    printf 'app_update 233780 validate\nquit\n' >> "$runfile"
  else
    printf 'app_update 233780 -beta creatordlc validate\nquit\n' >> "$runfile"
  fi
  run_steamcmd "$runfile"
  test -x "$ROOT/arma3/arma3server_x64"
)

lowercase_tree() {
  local root="$1" path target
  while IFS= read -r -d '' path; do
    target="$(dirname "$path")/$(basename "$path" | tr '[:upper:]' '[:lower:]')"
    [ "$path" = "$target" ] || [ -e "$target" ] || mv "$path" "$target"
  done < <(find "$root" -depth -name '*[A-Z]*' -print0)
}

install_workshop() (
  if [ "$VANILLA_MODE" = true ]; then
    : > "$ROOT/config/mods.txt"
    rm -f "$ROOT/config/preset.html"
    chown steam:steam "$ROOT/config/mods.txt"
    return 0
  fi
	local preset_file mods_file mods="" dlc
	mkdir -p "$ROOT/config/presets" "$ROOT/config/mod-revisions"
	preset_file="$ROOT/config/presets/revision-$PRESET_REVISION.html"
	mods_file="$ROOT/config/mod-revisions/revision-$PRESET_REVISION.txt"
	ids=()
	if [ -n "$PRESET_KEY" ]; then
		aws s3 cp "s3://$ASSETS_BUCKET/$PRESET_KEY" "$preset_file" --region "$AWS_REGION" --only-show-errors
		mapfile -t ids < <(grep -Eio "id=[0-9]+|data-publishedfileid=[\"'][0-9]+" "$preset_file" | grep -Eo '[0-9]+' | awk '!seen[$0]++')
	else
		rm -f -- "$preset_file" "$ROOT/config/preset.html"
	fi
	: > "$mods_file"
  IFS=';' read -r -a creator_dlcs <<< "$CREATOR_DLC_MODS"
  for dlc in "${creator_dlcs[@]}"; do
    [ -z "$dlc" ] && continue
    [[ "$dlc" =~ ^[a-z0-9_-]+$ ]] || { log "Creator DLC directory name is invalid"; return 1; }
    [ -d "$ROOT/arma3/$dlc" ] || { log "Selected Creator DLC $dlc was not installed"; return 1; }
    mods="${mods:+$mods;}$dlc"
  done
  local runfile id source link
  if [ "${#ids[@]}" -gt 0 ]; then
  runfile="$(mktemp /run/gsp-steam.XXXXXX)"
  trap 'rm -f "$runfile"' EXIT
  steam_login_file "$runfile"
  for id in "${ids[@]}"; do printf 'workshop_download_item 107410 %s validate\n' "$id" >> "$runfile"; done
  printf 'quit\n' >> "$runfile"
  run_steamcmd "$runfile"
  for id in "${ids[@]}"; do
    source="$ROOT/home/Steam/steamapps/workshop/content/107410/$id"
    [ -d "$source" ] || { log "Workshop item $id was not downloaded"; return 1; }
    lowercase_tree "$source"
    link="$ROOT/arma3/@workshop_$id"
    ln -sfn "$source" "$link"
    mods="${mods:+$mods;}@workshop_$id"
  done
	fi
	printf '%s' "$mods" > "$mods_file"
	if [ -n "$PRESET_KEY" ]; then ln -sfn "presets/revision-$PRESET_REVISION.html" "$ROOT/config/preset.html"; fi
	ln -sfn "mod-revisions/revision-$PRESET_REVISION.txt" "$ROOT/config/mods.txt"
	printf '%s' "$PRESET_REVISION" > "$ROOT/config/active-preset-revision"
  mkdir -p "$ROOT/home/Steam/steamapps/workshop"
  chown -R steam:steam "$ROOT/config" "$ROOT/home/Steam/steamapps/workshop" "$ROOT/arma3"
)

sqf_escape() { printf '%s' "$1" | sed 's/"/""/g'; }

deploy_content() {
  local mission_file mission_template safe_mission_template safe_name
  mission_template="$MISSION_TEMPLATE"
  mkdir -p "$ROOT/arma3/mpmissions" "$ROOT/home/.local/share/Arma 3 - Other Profiles/server"
  if [ -n "$MISSION_KEY" ]; then
    mission_file="$(basename "$MISSION_KEY")"
    if [[ "$mission_file" =~ ^[0-9a-f]{64}-(.+\.[pP][bB][oO])$ ]]; then
      mission_file="${BASH_REMATCH[1]}"
    fi
    aws s3 cp "s3://$ASSETS_BUCKET/$MISSION_KEY" "$ROOT/arma3/mpmissions/$mission_file" --region "$AWS_REGION" --only-show-errors
  fi
  safe_name="$(sqf_escape "$DISPLAY_NAME")"
  safe_mission_template="$(sqf_escape "$mission_template")"
  if [ -n "$SERVER_CONFIG_KEY" ]; then
    [ "$SERVER_CONFIG_REVISION" -ge 1 ] && [ "${#SERVER_CONFIG_SHA256}" -eq 64 ] || { log "custom server configuration snapshot is invalid"; return 1; }
    aws s3 cp "s3://$ASSETS_BUCKET/$SERVER_CONFIG_KEY" "$ROOT/config/server.cfg.pending" --region "$AWS_REGION" --only-show-errors
    printf '%s  %s\n' "$SERVER_CONFIG_SHA256" "$ROOT/config/server.cfg.pending" | sha256sum --check --status || { log "custom server configuration checksum mismatch"; return 1; }
    mv -f "$ROOT/config/server.cfg.pending" "$ROOT/config/server.cfg"
  else
    cat > "$ROOT/config/server.cfg" <<EOF
hostname = "$safe_name";
maxPlayers = 32;
verifySignatures = 2;
kickDuplicate = 1;
BattlEye = 1;
persistent = 1;
EOF
  fi
  awk '
    function structural(value, output, index, character, following) {
      output=""
      for (index=1; index<=length(value); index++) {
        character=substr(value,index,1); following=substr(value,index+1,1)
        if (block_comment) { if (character=="*" && following=="/") { block_comment=0; index++ }; continue }
        if (quoted) { if (character=="\"" && following=="\"") { index++; continue }; if (character=="\"") quoted=0; continue }
        if (character=="/" && following=="/") break
        if (character=="/" && following=="*") { block_comment=1; index++; continue }
        if (character=="\"") { quoted=1; continue }
        output=output character
      }
      return output
    }
    function braces(value, open, close) { open=gsub(/\{/, "{", value); close=gsub(/\}/, "}", value); return open-close }
    BEGIN { skipping=0; depth=0; block_comment=0; quoted=0 }
    { syntax=structural($0); folded=tolower(syntax) }
    !skipping && folded ~ /^[[:space:]]*class[[:space:]]+missions([[:space:]:{]|$)/ { skipping=1; depth=braces(syntax); if (depth<=0 && syntax ~ /;/) skipping=0; next }
    skipping { depth += braces(syntax); if (depth<=0 && syntax ~ /;/) skipping=0; next }
    { print }
  ' "$ROOT/config/server.cfg" > "$ROOT/config/server.cfg.mission"
  cat >> "$ROOT/config/server.cfg.mission" <<EOF
class Missions {
  class Primary {
    template = "$safe_mission_template";
    difficulty = "Regular";
  };
};
EOF
  mv -f "$ROOT/config/server.cfg.mission" "$ROOT/config/server.cfg"
  cat > "$ROOT/config/launch-arma.sh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=/srv/game-server
mods="$(cat "$ROOT/config/mods.txt" 2>/dev/null || true)"
cd "$ROOT/arma3"
args=(-name=server -config="$ROOT/config/server.cfg" -port=2302 -steamQueryPort=2303 -autoInit -noSound)
[ -z "$mods" ] || args+=("-mod=$mods")
exec ./arma3server_x64 "${args[@]}"
EOF
  chmod 755 "$ROOT/config/launch-arma.sh"
  chown -R steam:steam "$ROOT/config" "$ROOT/arma3/mpmissions" "$ROOT/home"
  cat > /etc/systemd/system/arma3-server.service <<'EOF'
[Unit]
Description=Arma 3 dedicated server managed by game-server-platform
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=steam
Group=steam
WorkingDirectory=/srv/game-server/arma3
ExecStart=/srv/game-server/config/launch-arma.sh
Restart=on-failure
RestartSec=10
LimitNOFILE=100000
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable arma3-server.service
}

install_teamspeak() {
  $TEAMSPEAK_ENABLED || return 0
  id teamspeak >/dev/null 2>&1 || useradd --home-dir "$ROOT/teamspeak" --shell /sbin/nologin teamspeak
  if [ ! -x "$ROOT/teamspeak/ts3server" ]; then
    mkdir -p "$ROOT/teamspeak"
    curl --fail --location --silent --show-error "https://files.teamspeak-services.com/releases/server/$TEAMSPEAK_VERSION/teamspeak3-server_linux_amd64-$TEAMSPEAK_VERSION.tar.bz2" -o /tmp/teamspeak.tar.bz2
    tar -xjf /tmp/teamspeak.tar.bz2 -C "$ROOT/teamspeak" --strip-components=1
    rm -f /tmp/teamspeak.tar.bz2
  fi
  touch "$ROOT/teamspeak/.ts3server_license_accepted"
  chown -R teamspeak:teamspeak "$ROOT/teamspeak"
  cat > /etc/systemd/system/teamspeak3-server.service <<'EOF'
[Unit]
Description=TeamSpeak 3 server managed by game-server-platform
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=teamspeak
Group=teamspeak
WorkingDirectory=/srv/game-server/teamspeak
ExecStart=/srv/game-server/teamspeak/ts3server license_accepted=1 voice_ip=0.0.0.0 default_voice_port=9987 query_protocols=raw
Restart=on-failure
RestartSec=5
LimitNOFILE=100000
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable teamspeak3-server.service
}

launch_and_verify() {
  if [ "$VANILLA_MODE" = false ] && { $STEAM_AUTH_ACTIVE || [ -n "$STEAM_AUTH_LOCK_OWNER" ]; }; then
    log "refusing game launch while Steam authorization material is active"
    return 1
  fi
  scrub_persistent_steam_auth
  systemctl restart arma3-server.service
  $TEAMSPEAK_ENABLED && systemctl restart teamspeak3-server.service
  checkpoint HEALTH_VERIFICATION
  for _ in $(seq 1 60); do
    if systemctl is-active --quiet arma3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)2302$'; then
      if ! $TEAMSPEAK_ENABLED || { systemctl is-active --quiet teamspeak3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)9987$'; }; then
        return 0
      fi
    fi
    sleep 10
  done
  systemctl status arma3-server.service --no-pager || true
  journalctl -u arma3-server.service -n 80 --no-pager || true
  $TEAMSPEAK_ENABLED && { systemctl status teamspeak3-server.service --no-pager || true; journalctl -u teamspeak3-server.service -n 80 --no-pager || true; }
  return 1
}

exec 8>/run/gsp-bootstrap-host.lock
flock -w 30 8
checkpoint HOST_PREPARED
prepare_host
mkdir -p "$STATE_DIR" "$LOG_DIR"
exec 9>"$STATE_DIR/bootstrap.lock"
flock -w 30 9
for stage in install_steamcmd install_arma install_workshop deploy_content install_teamspeak; do
	if [ "$stage" = install_arma ] && ! $STEAM_AUTH_ACTIVE; then begin_steam_auth; fi
	case "$stage" in
		install_steamcmd) checkpoint GAME_SERVER_INSTALLED;;
		install_workshop) checkpoint MODS_APPLIED;;
		deploy_content) checkpoint CONFIGURATION_READY;;
	esac
  marker="$STATE_DIR/$stage.complete"
	if [ "$stage" = install_workshop ] && [ "$VANILLA_MODE" = false ]; then
		marker="$STATE_DIR/$stage.revision-$PRESET_REVISION.config-$MOD_CONFIG_REVISION.complete"
		[ "$PRESET_ROLLBACK" = true ] && rm -f -- "$marker"
	fi
  if [ -f "$marker" ]; then
    log "stage $stage already complete"
    if [ "$stage" = install_workshop ] && $STEAM_AUTH_ACTIVE; then persist_steam_auth; cleanup_steam_auth; fi
    continue
  fi
  log "starting stage $stage"
  "$stage"
  touch "$marker"
  log "completed stage $stage"
	if [ "$stage" = install_workshop ] && $STEAM_AUTH_ACTIVE; then persist_steam_auth; cleanup_steam_auth; fi
done
checkpoint SERVICE_STARTED
log "starting stage launch_and_verify"
launch_and_verify
log "completed stage launch_and_verify"
log "bootstrap complete"
