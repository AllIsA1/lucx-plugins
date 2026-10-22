#!/usr/bin/env bash
# Lays the plugin system over an upstream checkout.
#
#   scripts/apply.sh <tree>
#
# New files are copied (a path that already exists upstream is a collision). Changed upstream files
# are three-way merged: base = the upstream version the overlay was made from, ours = the overlay's
# version, theirs = the file in <tree>. Locale files get the plugin keys added. Fails, listing every
# problem, when something does not fit; <tree> is then partly changed and should be thrown away.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

tree=$(cd "${1:?usage: apply.sh <tree>}" && pwd)
ov=$ROOT/overlay
problems=()

while IFS= read -r -d '' f; do
  f=${f#./}
  if [[ -e $tree/$f ]]; then
    cmp -s "$ov/files/$f" "$tree/$f" || problems+=("collision: $f already exists upstream")
  else
    mkdir -p "$tree/$(dirname "$f")"
    cp "$ov/files/$f" "$tree/$f"
  fi
done < <(cd "$ov/files" && find . -type f -print0 | sort -z)

while IFS= read -r -d '' f; do
  f=${f#./}
  up=$tree/$f
  if [[ ! -f $up ]]; then
    problems+=("missing upstream: $f was removed or moved")
  elif cmp -s "$up" "$ov/base/$f"; then
    cp "$ov/modified/$f" "$up"
  else
    tmp=$(mktemp)
    cp "$up" "$tmp"
    if git merge-file -L upstream -L overlay-base -L plugins "$tmp" "$ov/base/$f" "$ov/modified/$f"; then
      cp "$tmp" "$up"
    else
      problems+=("conflict: $f changed upstream where the plugin system also changes it")
    fi
    rm -f "$tmp"
  fi
done < <(cd "$ov/modified" && find . -type f -print0 | sort -z)

for extra in "$ov"/locales/*.json; do
  [[ -e $extra ]] || continue
  target=$tree/internal/web/translation/$(basename "$extra")
  if [[ -f $target ]]; then
    python3 "$ROOT/scripts/locales.py" merge "$target" "$extra"
  else
    problems+=("missing upstream: $(basename "$extra") locale does not exist")
  fi
done

if ((${#problems[@]})); then
  printf '%s\n' "${problems[@]}" >&2
  exit 1
fi
echo "applied the plugin system to $tree"
