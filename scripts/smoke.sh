#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Starts a built panel binary on a scratch port and checks it the way a server would meet it:
#   - without the plugin system service the panel serves its page, logs in, and the plugin routes say
#     "not running" without breaking anything (empty menu, no plugins feature announced);
#   - with the service (optional third argument: a plugin host binary) the two find each other: the
#     Plugins page answers through the panel, the node announces plugins, the gateway for plugins listens,
#     the panel's internal socket is private to root's user, and stopping the service takes it all away again.
# Usage: scripts/smoke.sh <panel-binary> [<port> [<host-binary>]]   (the gateway uses <port>+1)
set -euo pipefail
BIN=$1; PORT=${2:-18080}; HOST=${3:-}
d=$(mktemp -d)
export XUI_PORT=$PORT XUI_DB_FOLDER="$d/db" XUI_BIN_FOLDER="$d/bin" XUI_LOG_FOLDER="$d/log" \
  XUI_PLUGIN_HOST_SOCKET="$d/host.sock" XUI_PLUGIN_INTERNAL_SOCKET="$d/internal.sock"
mkdir -p "$XUI_DB_FOLDER" "$XUI_BIN_FOLDER" "$XUI_LOG_FOLDER"
# Newer panels invent a random login on the first start; this one fixes it.
"$BIN" setting -username admin -password admin > /dev/null
"$BIN" > "$d/panel.log" 2>&1 &
pid=$!
hostpid=
trap 'kill $pid $hostpid 2> /dev/null || true' EXIT
base=http://127.0.0.1:$PORT
for _ in $(seq 60); do curl -fsS -o /dev/null "$base/" 2> /dev/null && break; sleep 0.5; done
curl -fsS -o /dev/null "$base/"
jar="$d/jar"
csrf() { curl -fsS -b "$jar" -c "$jar" "$base/csrf-token" | jq -r .obj; }
api() { curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(csrf)" -H 'X-Requested-With: XMLHttpRequest' "$@"; }
api --data 'username=admin&password=admin' "$base/login" | jq -e .success > /dev/null

# wait_for <what> <command...>: the command must succeed within 40 s.
wait_for() {
  local what=$1
  shift
  for _ in $(seq 80); do "$@" > /dev/null 2>&1 && return 0; sleep 0.5; done
  echo "smoke failed: $what" >&2
  return 1
}
list_says() { api "$base/panel/api/plugins/list" | jq -e "$1"; }
announces_plugins() { api "$base/panel/api/lucx/hello" | jq -e '.obj.features | index("plugins")'; }

# 1. without the service
list_says '.success and (.obj.available == false) and (.obj.plugins | length == 0) and (.obj.error | length > 0)' > /dev/null
api "$base/panel/api/plugins/menu" | jq -e '.success and (.obj.menu | length == 0)' > /dev/null
if announces_plugins > /dev/null 2>&1; then echo "smoke failed: a panel without the service announces plugins" >&2; exit 1; fi
echo "smoke ok: without the plugin system service the panel behaves as one without plugins"

# 2. with the service
[[ -n $HOST ]] || exit 0
export XUI_PLUGINS_RUNNER=process XUI_PLUGINS_RUNTIME_DIR="$d/run"
"$HOST" -state-dir "$d/hoststate" -socket "$d/host.sock" -gateway "127.0.0.1:$((PORT + 1))" -panel-socket "$d/internal.sock" > "$d/host.log" 2>&1 &
hostpid=$!
wait_for "the Plugins page does not become available through the panel" list_says '.success and .obj.available'
wait_for "the node does not announce plugins while the service runs" announces_plugins
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$((PORT + 1))/panel/api/clients/list") == 401 ]] ||
  { echo "smoke failed: the gateway for plugins does not answer 401 without a token" >&2; exit 1; }
[[ $(stat -c %a "$d/internal.sock") == 600 ]] || { echo "smoke failed: the internal socket is not private" >&2; exit 1; }
[[ $(curl -s -o /dev/null -w '%{http_code}' -H 'X-LucX-Allowed-Routes: GET /clients/list' "$base/panel/api/clients/list") != 200 ]] ||
  { echo "smoke failed: the public listener trusts the host's headers" >&2; exit 1; }

# 3. and without it again
kill "$hostpid"
wait "$hostpid" 2> /dev/null || true
hostpid=
wait_for "the panel does not notice that the service is gone" list_says '.success and (.obj.available == false)'
api "$base/panel/api/plugins/menu" | jq -e '.success and (.obj.menu | length == 0)' > /dev/null
echo "smoke ok: the panel and the plugin system service find each other and part cleanly"
