#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# Licensed under the PolyForm Noncommercial License 1.0.0.
# Commercial use (including VPN resale) requires explicit written permission from the author.
# SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
# Starts work on the overlay: clones an upstream release and applies the overlay to it.
#
#   scripts/dev.sh [<tag> [<dir>]]       tag defaults to the one in overlay/BASE
#
# Edit the tree, build and test it as the upstream project does, then run scripts/export.sh <dir>
# to write the result back into overlay/.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"
upstream=${LUCX_UPSTREAM:-https://github.com/AlexeyLCP/lucx-ui.git}
tag=${1:-$(cut -d' ' -f1 "$ROOT/overlay/BASE")}
dir=${2:-$ROOT/work/$tag}
[[ -e $dir ]] && die "$dir exists"
git clone -q --depth 1 --branch "$tag" "$upstream" "$dir"
"$ROOT/scripts/apply.sh" "$dir"
echo "work tree: $dir (base $tag); when done: scripts/export.sh $dir"
