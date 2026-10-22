#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# Licensed under the PolyForm Noncommercial License 1.0.0.
# Commercial use (including VPN resale) requires explicit written permission from the author.
# SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
# Rebuilds the overlay from a working tree that holds the plugin system on top of an upstream release.
#
#   scripts/export.sh <tree> [<base-ref>]      (base-ref defaults to the tag recorded in overlay/BASE)
#
# New files go to overlay/files, changed upstream files to overlay/modified (with their upstream
# version in overlay/base, the common ancestor of the three-way merge), added locale keys to
# overlay/locales. OVERLAY_OUT redirects the output (the round-trip test uses it).
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

tree=$(cd "${1:?usage: export.sh <tree> [<base-ref>]}" && pwd)
base=${2:-$(cut -d' ' -f1 "$ROOT/overlay/BASE")}
out=${OVERLAY_OUT:-$ROOT/overlay}
[[ -d $tree/.git ]] || die "$tree is not a git checkout"
git -C "$tree" rev-parse --verify -q "$base^{commit}" > /dev/null || die "$base is not a commit of $tree"

mkdir -p "$out"
[[ $out == "$ROOT/overlay" ]] || cp "$ROOT/overlay/exclude.txt" "$ROOT/overlay/BASE" "$out/"
rm -rf "$out/files" "$out/base" "$out/modified" "$out/locales"
mkdir -p "$out/files" "$out/base" "$out/modified" "$out/locales"

# Intent-to-add makes untracked files visible to `git diff` without committing anything.
git -C "$tree" add -A -N .
added=0 changed=0 locales=0
while IFS=$'\t' read -r status path; do
  excluded "$path" && continue
  case $status in
    A)
      mkdir -p "$out/files/$(dirname "$path")"
      cp "$tree/$path" "$out/files/$path"
      added=$((added + 1)) ;;
    M)
      if is_locale "$path"; then
        git -C "$tree" show "$base:$path" > "$out/locales/.upstream.json"
        python3 "$ROOT/scripts/locales.py" diff "$out/locales/.upstream.json" "$tree/$path" "$out/locales/$(basename "$path")"
        rm -f "$out/locales/.upstream.json"
        locales=$((locales + 1))
      else
        mkdir -p "$out/base/$(dirname "$path")" "$out/modified/$(dirname "$path")"
        git -C "$tree" show "$base:$path" > "$out/base/$path"
        cp "$tree/$path" "$out/modified/$path"
        changed=$((changed + 1))
      fi ;;
    *) die "unsupported change '$status' for $path: the overlay only adds files and edits existing ones" ;;
  esac
done < <(git -C "$tree" diff --name-status --no-renames "$base" | sort -k2)
echo "exported: $added new files, $changed changed upstream files, $locales locale files"
