#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# Licensed under the PolyForm Noncommercial License 1.0.0.
# Commercial use (including VPN resale) requires explicit written permission from the author.
# SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
# Starts a built panel binary on a scratch port and checks that it serves its page and that the plugin
# system answers (log in, GET /panel/api/plugins/list). Usage: scripts/smoke.sh <binary> [<port>]
set -euo pipefail
BIN=$1; PORT=${2:-18080}
d=$(mktemp -d)
export XUI_PORT=$PORT XUI_DB_FOLDER="$d/db" XUI_BIN_FOLDER="$d/bin" XUI_LOG_FOLDER="$d/log" \
  XUI_PLUGINS_DIR="$d/plugins" XUI_PLUGINS_DATA_DIR="$d/data" XUI_PLUGINS_RUNNER=process XUI_PLUGINS_RUNTIME_DIR="$d/run"
mkdir -p "$XUI_DB_FOLDER" "$XUI_BIN_FOLDER" "$XUI_LOG_FOLDER"
"$BIN" > "$d/panel.log" 2>&1 &
pid=$!
trap 'kill $pid 2> /dev/null || true' EXIT
base=http://127.0.0.1:$PORT
for _ in $(seq 60); do curl -fsS -o /dev/null "$base/" 2> /dev/null && break; sleep 0.5; done
curl -fsS -o /dev/null "$base/"
jar="$d/jar"
csrf() { curl -fsS -b "$jar" -c "$jar" "$base/csrf-token" | jq -r .obj; }
curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(csrf)" -H 'X-Requested-With: XMLHttpRequest' \
  --data 'username=admin&password=admin' "$base/login" | jq -e .success > /dev/null
curl -fsS -b "$jar" -c "$jar" -H "X-CSRF-Token: $(csrf)" -H 'X-Requested-With: XMLHttpRequest' \
  "$base/panel/api/plugins/list" | jq -e '.success and .obj.available' > /dev/null
echo "smoke ok: the panel answers and the plugin system is available"
