#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Tests for scripts/build-host.sh and scripts/host-list.sh: what the host release workflow runs.
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ — $2}"; fails=$((fails + 1)); }

ctl=$work/buildsctl
go build -o "$ctl" ./cmd/buildsctl

# 1. a release is built for both CPUs, with checksums that match, and says what version it is.
dist=$work/dist
if scripts/build-host.sh host-7 "$dist" > "$work/build.out" 2>&1; then ok "the host builds"; else bad "the host builds" "$(tail -3 "$work/build.out")"; fi
for arch in amd64 arm64; do
  f=$dist/lucx-plugin-host-linux-$arch
  [[ -x $f ]] && [[ $(cut -d' ' -f1 "$f.sha256") == "$(sha256sum "$f" | cut -d' ' -f1)" ]] && ok "$arch binary and checksum agree" || bad "$arch binary and checksum agree"
done
if file "$dist/lucx-plugin-host-linux-amd64" | grep -q "statically linked"; then ok "the binary is static"; else bad "the binary is static" "$(file "$dist/lucx-plugin-host-linux-amd64")"; fi
case $(uname -m) in
  x86_64) [[ $("$dist/lucx-plugin-host-linux-amd64" -v) == host-7 ]] && ok "the binary reports its version" || bad "the binary reports its version" ;;
  *) ok "the binary reports its version (skipped on this CPU)" ;;
esac

# 2. the build is reproducible: the same source and version give the same bytes.
scripts/build-host.sh host-7 "$work/dist2" > /dev/null 2>&1
if cmp -s "$dist/lucx-plugin-host-linux-amd64" "$work/dist2/lucx-plugin-host-linux-amd64" && cmp -s "$dist/lucx-plugin-host-linux-arm64" "$work/dist2/lucx-plugin-host-linux-arm64"; then ok "the build is reproducible"; else bad "the build is reproducible"; fi

# ...also from another checkout path (a CI runner's), which -trimpath is for.
mkdir -p "$work/copy"
cp -r cmd internal keys scripts go.mod go.sum "$work/copy/"
mkdir -p "$work/copy/overlay" && cp overlay/exclude.txt "$work/copy/overlay/"
"$work/copy/scripts/build-host.sh" host-7 "$work/dist3" > /dev/null 2>&1
if cmp -s "$dist/lucx-plugin-host-linux-amd64" "$work/dist3/lucx-plugin-host-linux-amd64"; then ok "the build does not depend on the checkout path"; else bad "the build does not depend on the checkout path"; fi

# 3. only host-<n> is a version.
for v in host-0 host-01 v1.2.3 host- latest "host-7 "; do
  if scripts/build-host.sh "$v" "$work/none" > /dev/null 2>&1; then bad "version '$v' is refused"; else ok "version '$v' is refused"; fi
done

# 4. the list: signed with a trusted key, moves forward only, and a foreign key produces nothing.
key=$work/key
pub=$("$ctl" keygen -out "$key")
site=$work/site
mkdir -p "$site"
export HOST_LIST_TRUSTED_KEYS=$pub
BUILDS_SIGNING_KEY=$(cat "$key") scripts/host-list.sh host-7 "$dist" "$site" > /dev/null 2>&1 && ok "a release is listed and signed" || bad "a release is listed and signed"
"$ctl" host-verify -index "$site/host.json" -sig "$site/host.json.sig" -pub "$pub" > /dev/null && ok "the signature verifies" || bad "the signature verifies"
grep -q '"version": "host-7"' "$site/host.json" && grep -q "releases/download/host-7/lucx-plugin-host-linux-arm64" "$site/host.json" && ok "the list names the release downloads" || bad "the list names the release downloads"

scripts/build-host.sh host-8 "$work/dist8" > /dev/null 2>&1
BUILDS_SIGNING_KEY=$(cat "$key") scripts/host-list.sh host-8 "$work/dist8" "$site" > /dev/null 2>&1 && ok "the next release is listed" || bad "the next release is listed"
grep -q '"sequence": 3' "$site/host.json" && ok "the sequence moves forward" || bad "the sequence moves forward" "$(grep sequence "$site/host.json")"
before=$(sha256sum "$site/host.json")
if BUILDS_SIGNING_KEY=$(cat "$key") scripts/host-list.sh host-7 "$dist" "$site" > /dev/null 2>&1; then bad "an older release is refused"; else ok "an older release is refused"; fi
[[ $before == "$(sha256sum "$site/host.json")" ]] && ok "a refused release leaves the list as it was" || bad "a refused release leaves the list as it was"

other=$work/other
"$ctl" keygen -out "$other" > /dev/null
scripts/build-host.sh host-9 "$work/dist9" > /dev/null 2>&1
sigbefore=$(sha256sum "$site/host.json.sig")
if BUILDS_SIGNING_KEY=$(cat "$other") scripts/host-list.sh host-9 "$work/dist9" "$site" > /dev/null 2>&1; then bad "a key nobody trusts is refused"; else ok "a key nobody trusts is refused"; fi
[[ $before == "$(sha256sum "$site/host.json")" && $sigbefore == "$(sha256sum "$site/host.json.sig")" ]] && ok "a refused signature leaves the site untouched" || bad "a refused signature leaves the site untouched"
if BUILDS_SIGNING_KEY= scripts/host-list.sh host-9 "$work/dist9" "$site" > /dev/null 2>&1; then bad "a missing key is refused"; else ok "a missing key is refused"; fi

((fails == 0)) || { echo "$fails test(s) failed"; exit 1; }
echo "all passed"
