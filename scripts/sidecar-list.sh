#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Records a sidecar release in the signed sidecars.json.
#
#   BUILDS_SIGNING_KEY=... scripts/sidecar-list.sh <name> <version> <commit> <release> <dist-dir> <site-dir>
#
# <dist-dir> holds the .sha256 files the sidecar build wrote (<name>-linux-<arch>.sha256); <site-dir> holds
# sidecars.json (created when missing) and gets sidecars.json.sig. The signature is checked against
# keys/builds.pub before anything is left behind. HOST_LIST_TRUSTED_KEYS (base64 keys, comma separated)
# replaces keys/builds.pub for tests; CI never sets it.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

name=${1:-}
version=${2:-}
commit=${3:-}
release=${4:-}
dist=${5:-}
site=${6:-}
[[ -n $name && -n $version && -n $commit && -n $release && -d $dist && -d $site ]] ||
  die "usage: sidecar-list.sh <name> <version> <commit> <release> <dist-dir> <site-dir>"
[[ -n ${BUILDS_SIGNING_KEY:-} ]] || die "BUILDS_SIGNING_KEY is not set"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
ctl=$tmp/buildsctl
(cd "$ROOT" && go build -o "$ctl" ./cmd/buildsctl)

amd64=$(cat "$dist/$name-linux-amd64.sha256")
arm64=$(cat "$dist/$name-linux-arm64.sha256")
# Work on a copy: the site is touched only once the result verifies.
[[ -f $site/sidecars.json ]] && cp "$site/sidecars.json" "$tmp/sidecars.json"
"$ctl" sidecar-set -index "$tmp/sidecars.json" -name "$name" -version "$version" -commit "$commit" -release "$release" \
  -amd64-sha256 "$amd64" -arm64-sha256 "$arm64"
"$ctl" sidecar-sign -key-env BUILDS_SIGNING_KEY -index "$tmp/sidecars.json"
"$ctl" sidecar-verify -index "$tmp/sidecars.json" -sig "$tmp/sidecars.json.sig" -pub "${HOST_LIST_TRUSTED_KEYS:-$(paste -sd, "$ROOT/keys/builds.pub")}"
cp "$tmp/sidecars.json" "$site/sidecars.json.new"
cp "$tmp/sidecars.json.sig" "$site/sidecars.json.sig.new"
mv -f "$site/sidecars.json.new" "$site/sidecars.json"
mv -f "$site/sidecars.json.sig.new" "$site/sidecars.json.sig"
