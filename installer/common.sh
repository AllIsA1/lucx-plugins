# shellcheck shell=bash disable=SC2034
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Shared by install.sh and uninstall.sh. Sourced, not run; the variables are used by the scripts that source it.
# LUCX_ROOT prefixes every path the scripts write (empty on a real server); the tests set it.

ROOT=${LUCX_ROOT:-}
SERVICE=x-ui
PANEL=
RESTART_AT=04:00
NO_RESTART=0
PURGE=0

BIN_DIR=$ROOT/usr/local/lucx-plugins
SYNC=$BIN_DIR/lucx-plugins-sync
CONF_DIR=$ROOT/etc/lucx-plugins
CONF=$CONF_DIR/config
STATE_DIR=$ROOT/var/lib/lucx-plugins
UNIT_DIR=$ROOT/etc/systemd/system
DROPIN_DIR=$UNIT_DIR/$SERVICE.service.d
SYSTEMCTL=${LUCX_SYSTEMCTL:-systemctl}

red=$'\033[0;31m'
green=$'\033[0;32m'
yellow=$'\033[0;33m'
plain=$'\033[0m'

say() { echo -e "${green}$*${plain}"; }
warn() { echo -e "${yellow}$*${plain}" >&2; }
die() { echo -e "${red}error: $*${plain}" >&2; exit 1; }

need_root() {
  [[ -n $ROOT || $EUID -eq 0 ]] || die "run this as root (for example: sudo bash install.sh)"
}

need_systemd() {
  [[ -n $ROOT ]] && return 0
  command -v "$SYSTEMCTL" > /dev/null 2>&1 && [[ -d /run/systemd/system ]] ||
    die "this host does not run systemd. The plugin build is installed by a systemd drop-in; other init systems are not supported yet."
}

parse_args() {
  while (($#)); do
    case $1 in
      --service) SERVICE=${2:?--service needs a name}; shift 2 ;;
      --panel) PANEL=${2:?--panel needs the path of the x-ui binary}; shift 2 ;;
      --restart-at) RESTART_AT=${2:?--restart-at needs HH:MM}; shift 2 ;;
      --no-restart) NO_RESTART=1; shift ;;
      --purge) PURGE=1; shift ;;
      -h | --help) usage; exit 0 ;;
      *) die "unknown option $1 (try --help)" ;;
    esac
  done
  [[ $RESTART_AT =~ ^([01][0-9]|2[0-3]):[0-5][0-9]$ ]] || die "--restart-at must look like 04:00"
  [[ $SERVICE =~ ^[A-Za-z0-9_.@-]+$ ]] || die "--service must be a plain unit name"
  DROPIN_DIR=$UNIT_DIR/$SERVICE.service.d
}

# panel_path prints where the service's binary is: --panel, else the unit's ExecStart, else the default.
panel_path() {
  if [[ -n $PANEL ]]; then echo "$PANEL"; return; fi
  local exec_start
  exec_start=$("$SYSTEMCTL" cat "$SERVICE" 2> /dev/null | sed -n 's/^ExecStart=//p' | head -n1)
  exec_start=${exec_start#[-@+!:]}
  exec_start=${exec_start#[-@+!:]}
  exec_start=${exec_start%% *}
  echo "${exec_start:-/usr/local/x-ui/x-ui}"
}
