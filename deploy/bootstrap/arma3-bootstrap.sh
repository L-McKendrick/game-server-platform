#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

decode() { printf '%s' "$1" | base64 -d; }
SESSION_ID="$(decode "$SESSION_ID_B64")"
DISPLAY_NAME="$(decode "$DISPLAY_NAME_B64")"
DATA_VOLUME_ID="$(decode "$DATA_VOLUME_ID_B64")"
MISSION_KEY="$(decode "$MISSION_KEY_B64")"
PRESET_KEY="$(decode "$PRESET_KEY_B64")"
ASSETS_BUCKET="$(decode "$ASSETS_BUCKET_B64")"
STEAM_SECRET_ID="$(decode "$STEAM_SECRET_ID_B64")"
AWS_REGION="$(decode "$AWS_REGION_B64")"
TEAMSPEAK_VERSION="$(decode "$TEAMSPEAK_VERSION_B64")"
ROOT=/srv/game-server
STATE_DIR="$ROOT/state"
LOG_DIR="$ROOT/logs"

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

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
}

steam_login_file() {
  local target="$1" secret username password escaped_user escaped_password
  secret="$(aws secretsmanager get-secret-value --region "$AWS_REGION" --secret-id "$STEAM_SECRET_ID" --query SecretString --output text)"
  username="$(printf '%s' "$secret" | jq -er '.username | select(type == "string" and length > 0)')"
  password="$(printf '%s' "$secret" | jq -er '.password | select(type == "string" and length > 0)')"
  unset secret
  case "$username$password" in *$'\n'*|*$'\r'*) log "Steam credentials contain unsupported control characters"; return 1;; esac
  escaped_user="${username//\\/\\\\}"; escaped_user="${escaped_user//\"/\\\"}"
  escaped_password="${password//\\/\\\\}"; escaped_password="${escaped_password//\"/\\\"}"
  printf 'login "%s" "%s"\n' "$escaped_user" "$escaped_password" >> "$target"
  unset username password escaped_user escaped_password
  chmod 600 "$target"
  chown steam:steam "$target"
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
  printf 'app_update 233780 -beta creatordlc validate\nquit\n' >> "$runfile"
  runuser -u steam -- "$ROOT/steamcmd/steamcmd.sh" +runscript "$runfile"
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
  aws s3 cp "s3://$ASSETS_BUCKET/$PRESET_KEY" "$ROOT/config/preset.html" --region "$AWS_REGION" --only-show-errors
  mapfile -t ids < <(grep -Eio "id=[0-9]+|data-publishedfileid=[\"'][0-9]+" "$ROOT/config/preset.html" | grep -Eo '[0-9]+' | awk '!seen[$0]++')
  : > "$ROOT/config/mods.txt"
  [ "${#ids[@]}" -gt 0 ] || return 0
  local runfile id source link mods=""
  runfile="$(mktemp /run/gsp-steam.XXXXXX)"
  trap 'rm -f "$runfile"' EXIT
  steam_login_file "$runfile"
  for id in "${ids[@]}"; do printf 'workshop_download_item 107410 %s validate\n' "$id" >> "$runfile"; done
  printf 'quit\n' >> "$runfile"
  runuser -u steam -- "$ROOT/steamcmd/steamcmd.sh" +runscript "$runfile"
  for id in "${ids[@]}"; do
    source="$ROOT/home/Steam/steamapps/workshop/content/107410/$id"
    [ -d "$source" ] || { log "Workshop item $id was not downloaded"; return 1; }
    lowercase_tree "$source"
    link="$ROOT/arma3/@workshop_$id"
    ln -sfn "$source" "$link"
    mods="${mods:+$mods;}@workshop_$id"
  done
  printf '%s' "$mods" > "$ROOT/config/mods.txt"
  chown -R steam:steam "$ROOT/config" "$ROOT/home/Steam/steamapps/workshop" "$ROOT/arma3"
)

sqf_escape() { printf '%s' "$1" | sed 's/"/""/g'; }

deploy_content() {
  local mission_file mission_template safe_name
  mission_file="$(basename "$MISSION_KEY")"
  mission_template="${mission_file%.[Pp][Bb][Oo]}"
  mkdir -p "$ROOT/arma3/mpmissions" "$ROOT/home/.local/share/Arma 3 - Other Profiles/server"
  aws s3 cp "s3://$ASSETS_BUCKET/$MISSION_KEY" "$ROOT/arma3/mpmissions/$mission_file" --region "$AWS_REGION" --only-show-errors
  safe_name="$(sqf_escape "$DISPLAY_NAME")"
  cat > "$ROOT/config/server.cfg" <<EOF
hostname = "$safe_name";
maxPlayers = 32;
verifySignatures = 2;
kickDuplicate = 1;
BattlEye = 1;
persistent = 1;
class Missions {
  class Primary {
    template = "$mission_template";
    difficulty = "Regular";
  };
};
EOF
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
  systemctl restart arma3-server.service
  $TEAMSPEAK_ENABLED && systemctl restart teamspeak3-server.service
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
prepare_host
mkdir -p "$STATE_DIR" "$LOG_DIR"
exec 9>"$STATE_DIR/bootstrap.lock"
flock -w 30 9
for stage in install_steamcmd install_arma install_workshop deploy_content install_teamspeak; do
  marker="$STATE_DIR/$stage.complete"
  if [ -f "$marker" ]; then log "stage $stage already complete"; continue; fi
  log "starting stage $stage"
  "$stage"
  touch "$marker"
  log "completed stage $stage"
done
log "starting stage launch_and_verify"
launch_and_verify
log "completed stage launch_and_verify"
log "bootstrap complete"
