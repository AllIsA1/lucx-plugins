#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Removes the plugin system's installer parts and puts the original panel binary back. The panel's own
# data is never touched. Plugins and their data stay unless --purge is given.
#   bash uninstall.sh [--service NAME] [--purge]
set -euo pipefail

usage() {
  sed -n '2,6p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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
[[ -x $SYNC ]] || die "the plugin system is not installed here ($SYNC is missing)"

"$SYSTEMCTL" disable --now lucx-plugins-sync.timer lucx-plugins-apply.timer > /dev/null 2>&1 || true
# The service first, so it cannot start plugins again; then the plugins it started (units of their own).
"$SYSTEMCTL" disable --now "$HOST_UNIT_NAME" > /dev/null 2>&1 || true
"$SYSTEMCTL" stop 'lucx-plugin-*.service' > /dev/null 2>&1 || true
# The drop-in goes first: otherwise the next start of the panel would swap the build straight back in.
rm -f "$DROPIN_DIR/30-lucx-plugins.conf"
rmdir "$DROPIN_DIR" 2> /dev/null || true
"$SYSTEMCTL" daemon-reload

restored=0
out=$("$SYNC" -config "$CONF" restore) || die "could not restore the original panel binary. The timers and the start hook are already off, the build with plugins is still installed: run \"$SYNC -config $CONF restore\" and then this script again"
echo "$out"
[[ $out == restored* ]] && restored=1

rm -f "$HOST_UNIT" "$UNIT_DIR"/lucx-plugins-sync.service "$UNIT_DIR"/lucx-plugins-sync.timer \
  "$UNIT_DIR"/lucx-plugins-apply.service "$UNIT_DIR"/lucx-plugins-apply.timer
"$SYSTEMCTL" daemon-reload
[[ -L $LINK && $(readlink "$LINK") == "$SYNC" ]] && rm -f "$LINK"
rm -rf "$BIN_DIR" "$CONF_DIR" "$STATE_DIR"

if ((PURGE)); then
  rm -rf "$HOST_STATE" "$ROOT/var/lib/x-ui-plugins" "$ROOT/etc/x-ui/plugins" "$ROOT/etc/x-ui/plugins-data" "$ROOT/etc/x-ui/plugins.json" "$ROOT/etc/x-ui/plugins.key" "$ROOT/etc/x-ui/plugins-catalog.json"
  say "Plugins and their data were removed."
fi

if ((restored)) && ((! NO_RESTART)); then
  say "Restarting $SERVICE with the original panel binary..."
  "$SYSTEMCTL" restart "$SERVICE" || warn "could not restart $SERVICE; restart it yourself"
fi
say "Done. The panel is back to its original state."
