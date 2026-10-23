#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Records a host release in the signed host.json.
#
#   BUILDS_SIGNING_KEY=... scripts/host-list.sh host-<n> <dist-dir> <site-dir>
#
# <dist-dir> holds the .sha256 files build-host.sh wrote; <site-dir> holds host.json (created when missing)
# and gets host.json.sig. The signature is checked against keys/builds.pub before anything is left behind,
# so a secret that does not belong to a trusted key never produces a list. HOST_LIST_TRUSTED_KEYS (base64
# keys, comma separated) replaces keys/builds.pub for tests; CI never sets it.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

version=${1:-}
dist=${2:-}
site=${3:-}
[[ -n $version && -d $dist && -d $site ]] || die "usage: host-list.sh host-<n> <dist-dir> <site-dir>"
[[ -n ${BUILDS_SIGNING_KEY:-} ]] || die "BUILDS_SIGNING_KEY is not set"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
ctl=$tmp/buildsctl
(cd "$ROOT" && go build -o "$ctl" ./cmd/buildsctl)

amd64=$(cat "$dist/lucx-plugin-host-linux-amd64.sha256")
arm64=$(cat "$dist/lucx-plugin-host-linux-arm64.sha256")
# Work on a copy: the site is touched only once the result verifies.
[[ -f $site/host.json ]] && cp "$site/host.json" "$tmp/host.json"
"$ctl" host-set -index "$tmp/host.json" -version "$version" -amd64-sha256 "$amd64" -arm64-sha256 "$arm64"
"$ctl" host-sign -key-env BUILDS_SIGNING_KEY -index "$tmp/host.json"
"$ctl" host-verify -index "$tmp/host.json" -sig "$tmp/host.json.sig" -pub "${HOST_LIST_TRUSTED_KEYS:-$(paste -sd, "$ROOT/keys/builds.pub")}"
cp "$tmp/host.json" "$site/host.json.new"
cp "$tmp/host.json.sig" "$site/host.json.sig.new"
mv -f "$site/host.json.new" "$site/host.json"
mv -f "$site/host.json.sig.new" "$site/host.json.sig"
