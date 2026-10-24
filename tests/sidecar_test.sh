#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Tests for scripts/build-sidecar.sh and scripts/sidecar-list.sh: what the sidecar release workflow runs.
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ — $2}"; fails=$((fails + 1)); }

ctl=$work/buildsctl
go build -o "$ctl" ./cmd/buildsctl

# A stand-in for the upstream repository: a tiny Go program with an annotated tag.
up=$work/upstream
git init -q "$up"
cat > "$up/go.mod" <<'MOD'
module example.com/fakeflux

go 1.21
MOD
cat > "$up/main.go" <<'GO'
package main

import "fmt"

func main() { fmt.Println("fakeflux v0.13.0") }
GO
git -C "$up" add -A
git -C "$up" -c user.name=t -c user.email=t@example.com commit -q -m one
git -C "$up" -c user.name=t -c user.email=t@example.com tag -a v0.13.0 -m v0.13.0
commit=$(git -C "$up" rev-parse HEAD)
export SIDECAR_REPO_URL=$up

# 1. both CPUs are built from the commit, statically, with matching checksums.
dist=$work/dist
if scripts/build-sidecar.sh openflux v0.13.0 "$commit" "$dist" > "$work/build.out" 2>&1; then ok "the sidecar builds"; else bad "the sidecar builds" "$(tail -3 "$work/build.out")"; fi
for arch in amd64 arm64; do
  f=$dist/openflux-linux-$arch
  [[ -x $f ]] && [[ $(cut -d' ' -f1 "$f.sha256") == "$(sha256sum "$f" | cut -d' ' -f1)" ]] && ok "$arch binary and checksum agree" || bad "$arch binary and checksum agree"
done
if file "$dist/openflux-linux-amd64" | grep -q "statically linked"; then ok "the binary is static"; else bad "the binary is static"; fi
case $(uname -m) in
  x86_64) [[ $("$dist/openflux-linux-amd64") == "fakeflux v0.13.0" ]] && ok "the binary runs" || bad "the binary runs" ;;
  *) ok "the binary runs (skipped on this CPU)" ;;
esac

# 2. a tag that does not point at the named commit, a short commit, an unknown program and a bad version are refused.
second=$(git -C "$up" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m two && git -C "$up" rev-parse HEAD)
if scripts/build-sidecar.sh openflux v0.13.0 "$second" "$work/none" > /dev/null 2>&1; then bad "a commit the tag does not point at is refused"; else ok "a commit the tag does not point at is refused"; fi
if scripts/build-sidecar.sh openflux v0.13.0 "${commit:0:12}" "$work/none" > /dev/null 2>&1; then bad "a short commit is refused"; else ok "a short commit is refused"; fi
if scripts/build-sidecar.sh rm v0.13.0 "$commit" "$work/none" > /dev/null 2>&1; then bad "an unknown program is refused"; else ok "an unknown program is refused"; fi
for v in 0.13.0 v0.13 main "v0.13.0 "; do
  if scripts/build-sidecar.sh openflux "$v" "$commit" "$work/none" > /dev/null 2>&1; then bad "version '$v' is refused"; else ok "version '$v' is refused"; fi
done

# 3. the list: signed with a trusted key, the sequence moves forward, a foreign key produces nothing.
key=$work/key
pub=$("$ctl" keygen -out "$key")
site=$work/site
mkdir -p "$site"
export HOST_LIST_TRUSTED_KEYS=$pub
rel=sidecar-openflux-v0.13.0-1
BUILDS_SIGNING_KEY=$(cat "$key") scripts/sidecar-list.sh openflux v0.13.0 "$commit" "$rel" "$dist" "$site" > /dev/null 2>&1 && ok "a release is listed and signed" || bad "a release is listed and signed"
"$ctl" sidecar-verify -index "$site/sidecars.json" -sig "$site/sidecars.json.sig" -pub "$pub" > /dev/null && ok "the signature verifies" || bad "the signature verifies"
grep -q "releases/download/$rel/openflux-linux-arm64" "$site/sidecars.json" && grep -q "\"commit\": \"$commit\"" "$site/sidecars.json" && ok "the list names the downloads and the commit" || bad "the list names the downloads and the commit"

before=$(sha256sum "$site/sidecars.json")
other=$work/other
"$ctl" keygen -out "$other" > /dev/null
if BUILDS_SIGNING_KEY=$(cat "$other") scripts/sidecar-list.sh openflux v0.13.0 "$commit" "$rel" "$dist" "$site" > /dev/null 2>&1; then bad "a key nobody trusts is refused"; else ok "a key nobody trusts is refused"; fi
[[ $before == "$(sha256sum "$site/sidecars.json")" ]] && ok "a refused signature leaves the site untouched" || bad "a refused signature leaves the site untouched"
if BUILDS_SIGNING_KEY="" scripts/sidecar-list.sh openflux v0.13.0 "$commit" "$rel" "$dist" "$site" > /dev/null 2>&1; then bad "a missing key is refused"; else ok "a missing key is refused"; fi

((fails == 0)) || { echo "$fails test(s) failed"; exit 1; }
echo "all passed"
