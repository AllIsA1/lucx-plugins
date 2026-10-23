#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Builds the plugin host for both architectures the way a release is built.
#
#   scripts/build-host.sh host-<n> <out-dir>
#
# Writes <out-dir>/lucx-plugin-host-linux-{amd64,arm64} and a .sha256 file beside each. The binaries are
# static and reproducible (-trimpath, no build id), and the one that runs here must report the version.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

version=${1:-}
out=${2:-}
[[ $version =~ ^host-[1-9][0-9]{0,8}$ ]] || die "the version must look like host-<n>, got '$version'"
[[ -n $out ]] || die "usage: build-host.sh host-<n> <out-dir>"
mkdir -p "$out"
out=$(cd "$out" && pwd)

for arch in amd64 arm64; do
  name=lucx-plugin-host-linux-$arch
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X main.version=$version" -o "$out/$name" ./cmd/lucx-plugin-host)
  (cd "$out" && sha256sum "$name" | cut -d' ' -f1 > "$name.sha256")
done

case $(uname -m) in
  x86_64) native=amd64 ;;
  aarch64) native=arm64 ;;
  *) native= ;;
esac
if [[ -n $native ]]; then
  got=$("$out/lucx-plugin-host-linux-$native" -v)
  [[ $got == "$version" ]] || die "the $native binary reports '$got', expected $version"
fi
echo "built $version in $out"
