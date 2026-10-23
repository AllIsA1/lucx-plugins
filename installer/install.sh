#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Installs the plugin system next to a LucX-UI panel that is already running.
#
#   curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/install.sh | sudo bash
#
# What it does: puts a small program (lucx-plugins-sync) in /usr/local/lucx-plugins, a systemd drop-in
# that runs it before the panel starts, and two timers. The program recognises the panel binary by its
# hash and, if the signed list has the matching build with plugins, swaps it in. It also installs the
# plugin system service (lucx-plugins-host.service), from the signed list too. Nothing else of the panel
# is touched. Options (after `bash -s --`): --service NAME  --panel PATH  --restart-at HH:MM  --no-restart
# --gateway 127.0.0.1:PORT (where plugins reach the panel API)
set -euo pipefail

usage() {
  sed -n '2,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

if [[ -n ${BASH_SOURCE[0]:-} && -f $(dirname "${BASH_SOURCE[0]}")/common.sh ]]; then
  # shellcheck source=common.sh
  source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
else
  common=$(mktemp)
  curl -fsSL "${LUCX_PLUGINS_RAW:-https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main}/installer/common.sh" -o "$common" ||
    { echo "cannot download the installer helpers" >&2; exit 1; }
  # shellcheck disable=SC1090
  source "$common"
  rm -f "$common"
fi

parse_args "$@"
need_root
need_systemd

case $(uname -m) in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m): builds exist for amd64 and arm64" ;;
esac
command -v curl > /dev/null 2>&1 || die "curl is required"
command -v sha256sum > /dev/null 2>&1 || die "sha256sum is required"

PANEL=$(panel_path)
[[ -f $ROOT$PANEL ]] || die "the panel binary $PANEL does not exist; pass --panel PATH if it lives elsewhere"
[[ -n $ROOT ]] || "$SYSTEMCTL" cat "$SERVICE" > /dev/null 2>&1 || die "no systemd service named $SERVICE; pass --service NAME"

SYNC_BASE=${LUCX_PLUGINS_SYNC_URL:-https://github.com/AllIsA1/lucx-plugins/releases/download/sync-latest}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say "Downloading lucx-plugins-sync ($ARCH)..."
curl -fL --retry 3 --connect-timeout 15 -o "$tmp/sync" "$SYNC_BASE/lucx-plugins-sync-linux-$ARCH"
curl -fL --retry 3 --connect-timeout 15 -o "$tmp/sync.sha256" "$SYNC_BASE/lucx-plugins-sync-linux-$ARCH.sha256"
want=$(cut -d' ' -f1 "$tmp/sync.sha256")
got=$(sha256sum "$tmp/sync" | cut -d' ' -f1)
[[ $want == "$got" && ${#want} -eq 64 ]] || die "the downloaded program does not match its checksum"
chmod 755 "$tmp/sync"
"$tmp/sync" version > /dev/null || die "the downloaded program does not run on this host"

mkdir -p "$BIN_DIR"
install -m 755 "$tmp/sync" "$SYNC.new"
mv -f "$SYNC.new" "$SYNC"

# The plugin system service comes from the signed list, checked by the program just installed. If that
# fails nothing else has been changed yet: fix the cause and run this again.
say "Installing the plugin system service..."
"$SYNC" -host-exe "$HOST_EXE" -host-state "$HOST_STATE" host-install ||
  die "the plugin system service could not be installed (see above). Nothing else was changed; run this again when the problem is fixed."

mkdir -p "$CONF_DIR" "$STATE_DIR" "$DROPIN_DIR" "$HOST_STATE"

# An old layout (the first plugin system, built into the panel) is not carried over.
if [[ -e $ROOT/etc/x-ui/plugins.json ]]; then
  warn "Found plugins of the earlier, built-in plugin system ($ROOT/etc/x-ui/plugins.json). They are not migrated: install them again from the Plugins page."
fi

# An existing configuration is the operator's: only a missing one is created.
if [[ ! -f $CONF ]]; then
  cat > "$CONF" << CONFEOF
# Read by lucx-plugins-sync.
PANEL_BINARY=$ROOT$PANEL
SERVICE=$SERVICE
STATE_DIR=$STATE_DIR
RESTART_AT=$RESTART_AT
CONFEOF
fi

cat > "$DROPIN_DIR/30-lucx-plugins.conf" << DROPEOF
# Installed by lucx-plugins. Before each start of the panel, swap in the build with the plugin system
# when this binary is a known original. "-" lets the panel start even if that fails; "+" runs it unrestricted.
# The runtime directory is where the panel opens the socket the plugin system service calls it on.
[Service]
ExecStartPre=-+$SYNC -config $CONF at-start
RuntimeDirectory=lucx-panel
RuntimeDirectoryMode=0755
DROPEOF

# Where plugins reach the panel's API (the service's loopback gateway); the operator's file is kept.
if [[ ! -f $HOST_ENV || -n $GATEWAY ]]; then
  {
    echo "# Read by the plugin system service. The gateway must stay on a loopback address."
    if [[ -n $GATEWAY ]]; then echo "LUCX_HOST_GATEWAY=$GATEWAY"; else echo "#LUCX_HOST_GATEWAY=127.0.0.1:19480"; fi
  } > "$HOST_ENV"
fi

cat > "$HOST_UNIT" << HOSTEOF
[Unit]
Description=LucX-UI plugin system
Documentation=https://github.com/AllIsA1/lucx-plugins
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-$HOST_ENV
# A freshly updated binary that keeps failing to start is replaced by the previous one here, from outside it.
ExecStartPre=-+$SYNC -host-exe $HOST_EXE -host-state $HOST_STATE host-guard
ExecStart=$HOST_EXE -state-dir $HOST_STATE
Restart=always
RestartSec=2
# Plugin processes are units of their own and keep running while the service restarts or updates itself.
KillMode=process
RuntimeDirectory=lucx-plugin-host
RuntimeDirectoryMode=0755
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
ReadWritePaths=$BIN_DIR $HOST_STATE /var/lib/x-ui-plugins /run

[Install]
WantedBy=multi-user.target
HOSTEOF

cat > "$UNIT_DIR/lucx-plugins-sync.service" << UNITEOF
[Unit]
Description=Check for a LucX-UI build with the plugin system
After=network-online.target

[Service]
Type=oneshot
ExecStart=$SYNC -config $CONF check
NoNewPrivileges=yes
PrivateTmp=yes
UNITEOF

cat > "$UNIT_DIR/lucx-plugins-sync.timer" << UNITEOF
[Unit]
Description=Check for a LucX-UI build with the plugin system

[Timer]
OnBootSec=5min
OnUnitActiveSec=30min
RandomizedDelaySec=300

[Install]
WantedBy=timers.target
UNITEOF

cat > "$UNIT_DIR/lucx-plugins-apply.service" << UNITEOF
[Unit]
Description=Restart LucX-UI to pick up the build with the plugin system

[Service]
Type=oneshot
ExecStart=$SYNC -config $CONF apply
UNITEOF

cat > "$UNIT_DIR/lucx-plugins-apply.timer" << UNITEOF
[Unit]
Description=Restart LucX-UI at a quiet hour when a build with the plugin system is waiting

[Timer]
OnCalendar=*-*-* $RESTART_AT:00
Persistent=true

[Install]
WantedBy=timers.target
UNITEOF

"$SYSTEMCTL" daemon-reload
"$SYSTEMCTL" enable --now lucx-plugins-sync.timer lucx-plugins-apply.timer > /dev/null 2>&1 || warn "could not enable the timers; run: systemctl enable --now lucx-plugins-sync.timer lucx-plugins-apply.timer"
# restart, not just start: an installer run again must pick up a changed unit or gateway at once.
"$SYSTEMCTL" enable "$HOST_UNIT_NAME" > /dev/null 2>&1 && "$SYSTEMCTL" restart "$HOST_UNIT_NAME" ||
  warn "could not start the plugin system service; run: systemctl enable --now $HOST_UNIT_NAME (and see journalctl -u $HOST_UNIT_NAME)"

if ((NO_RESTART)); then
  say "Looking for a build for this panel (no restart)..."
  "$SYNC" -config "$CONF" check || warn "the check did not finish; the timer will retry"
else
  say "Looking for a build for this panel (the panel restarts if one is found)..."
  "$SYNC" -config "$CONF" apply || warn "the check did not finish; the timer will retry"
fi
echo
"$SYNC" -config "$CONF" status
echo
say "Installed. The plugin system service is running; the panel gets its side of it at its next start, and again after every panel update, automatically."
say "Check it with: systemctl status $HOST_UNIT_NAME"
say "Remove it with: curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/uninstall.sh | sudo bash"
