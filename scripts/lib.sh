#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# Licensed under the PolyForm Noncommercial License 1.0.0.
# Commercial use (including VPN resale) requires explicit written permission from the author.
# SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
# Shared helpers for the overlay scripts.

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

die() { echo "error: $*" >&2; exit 1; }

# excluded <path>: true when overlay/exclude.txt lists a glob that matches it.
excluded() {
  local path=$1 glob
  while IFS= read -r glob; do
    [[ -z $glob || $glob == \#* ]] && continue
    # shellcheck disable=SC2254
    case $path in $glob) return 0 ;; esac
  done < "$ROOT/overlay/exclude.txt"
  return 1
}

is_locale() { [[ $1 == internal/web/translation/*.json ]]; }
