#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Tests for the overlay scripts. Needs git, python3 and an upstream repository:
#   LUCX_UPSTREAM  path or URL of lucx-ui (default: GitHub)
#   LUCX_TEST_TAG  release to test against (default: the tag in overlay/BASE)
#   LUCX_SEED_REPO / LUCX_SEED_REF   optional: a tree the overlay was exported from; the applied
#                  result must equal it (the one-off proof that export and apply agree)
set -euo pipefail
# shellcheck source=../scripts/lib.sh
source "$(dirname "$0")/../scripts/lib.sh"

upstream=${LUCX_UPSTREAM:-https://github.com/AlexeyLCP/lucx-ui.git}
tag=${LUCX_TEST_TAG:-$(cut -d' ' -f1 "$ROOT/overlay/BASE")}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0

ok() { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ — $2}"; fails=$((fails + 1)); }
fresh() { git clone -q --depth 1 --branch "$tag" "$upstream" "$1" 2> /dev/null; }

# 1. apply, then export again: the overlay must come back byte for byte.
fresh "$work/rt"
"$ROOT/scripts/apply.sh" "$work/rt" > /dev/null
OVERLAY_OUT="$work/overlay-again" "$ROOT/scripts/export.sh" "$work/rt" "$tag" > /dev/null
if [[ $tag == "$(cut -d' ' -f1 "$ROOT/overlay/BASE")" ]]; then
  if diff -r "$ROOT/overlay" "$work/overlay-again" > "$work/rt.diff"; then ok "apply then export reproduces the overlay"
  else bad "apply then export reproduces the overlay" "$(head -5 "$work/rt.diff" | tr '\n' ' ')"; fi
fi

# 2. the applied tree has the plugin system, and every locale still parses with the plugin keys.
if [[ -f $work/rt/internal/lucx/hostlink/link.go && -f $work/rt/frontend/src/pages/plugins/PluginsPage.tsx ]]; then ok "plugin files are in place"
else bad "plugin files are in place"; fi
if python3 - "$work/rt" << 'PY'
import json, sys, glob, os
tree = sys.argv[1]
for path in glob.glob(os.path.join(tree, "internal/web/translation/*.json")):
    data = json.load(open(path, encoding="utf-8"))
    if "plugins" not in data["menu"] or "title" not in data["pages"]["plugins"]:
        sys.exit(f"{path} lacks the plugin keys")
PY
then ok "every locale carries the plugin keys"; else bad "every locale carries the plugin keys"; fi

# 3. a file the overlay adds that already exists upstream is reported, not overwritten.
fresh "$work/collision"
first=$(cd "$ROOT/overlay/files" && find . -type f | sort | head -1)
first=${first#./}
mkdir -p "$work/collision/$(dirname "$first")"
echo "upstream wrote this first" > "$work/collision/$first"
if "$ROOT/scripts/apply.sh" "$work/collision" > /dev/null 2> "$work/collision.err"; then bad "a colliding file fails the apply"
elif grep -q "collision: $first" "$work/collision.err" && grep -q "upstream wrote this first" "$work/collision/$first"; then ok "a colliding file fails the apply and is left alone"
else bad "a colliding file fails the apply" "$(cat "$work/collision.err")"; fi

# 4. an upstream change next to ours is a conflict that names the file and leaves it untouched.
fresh "$work/conflict"
sed -i 's/^\tengine, err := s.initRouter()$/\tengine, err := s.initRouter() \/\/ changed upstream/' "$work/conflict/internal/web/web.go"
before=$(sha256sum "$work/conflict/internal/web/web.go")
if "$ROOT/scripts/apply.sh" "$work/conflict" > /dev/null 2> "$work/conflict.err"; then bad "a conflicting upstream change fails the apply"
elif grep -q "conflict: internal/web/web.go" "$work/conflict.err" && [[ $before == "$(sha256sum "$work/conflict/internal/web/web.go")" ]]; then ok "a conflicting upstream change fails the apply and leaves the file untouched"
else bad "a conflicting upstream change fails the apply" "$(cat "$work/conflict.err")"; fi

# 5. an upstream change far from ours merges cleanly and keeps both.
fresh "$work/merge"
sed -i '1i // an unrelated upstream edit at the top of the file' "$work/merge/internal/web/web.go"
if "$ROOT/scripts/apply.sh" "$work/merge" > /dev/null 2>&1 && grep -q "an unrelated upstream edit" "$work/merge/internal/web/web.go" && grep -q "hostlink.Start()" "$work/merge/internal/web/web.go"; then ok "an unrelated upstream change merges with ours"
else bad "an unrelated upstream change merges with ours"; fi

# 6. a file the overlay edits but upstream removed is reported.
fresh "$work/removed"
rm "$work/removed/internal/eventbus/events.go"
if "$ROOT/scripts/apply.sh" "$work/removed" > /dev/null 2> "$work/removed.err"; then bad "a removed upstream file fails the apply"
elif grep -q "missing upstream: internal/eventbus/events.go" "$work/removed.err"; then ok "a removed upstream file fails the apply"
else bad "a removed upstream file fails the apply" "$(cat "$work/removed.err")"; fi

# 6b. export refuses a symlink in the tree instead of exporting half an overlay
ln -s /tmp "$work/rt/frontend/node_modules"
if OVERLAY_OUT="$work/overlay-link" "$ROOT/scripts/export.sh" "$work/rt" "$tag" > /dev/null 2> "$work/link.err"; then bad "export refuses a symlink"
elif grep -q "is a symlink" "$work/link.err"; then ok "export refuses a symlink"
else bad "export refuses a symlink" "$(cat "$work/link.err")"; fi
rm -f "$work/rt/frontend/node_modules"

# 7. the optional seed comparison.
if [[ -n ${LUCX_SEED_REPO:-} && -n ${LUCX_SEED_REF:-} ]]; then
  mismatches=0
  while IFS= read -r path; do
    excluded "$path" && continue
    if is_locale "$path"; then
      python3 - "$work/rt/$path" <(git -C "$LUCX_SEED_REPO" show "$LUCX_SEED_REF:$path") << 'PY' || mismatches=$((mismatches + 1))
import json, sys
sys.exit(0 if json.load(open(sys.argv[1], encoding="utf-8")) == json.load(open(sys.argv[2], encoding="utf-8")) else 1)
PY
    else
      cmp -s "$work/rt/$path" <(git -C "$LUCX_SEED_REPO" show "$LUCX_SEED_REF:$path") || { echo "  differs: $path"; mismatches=$((mismatches + 1)); }
    fi
  done < <(git -C "$LUCX_SEED_REPO" diff --name-only "$tag" "$LUCX_SEED_REF")
  if ((mismatches == 0)); then ok "the applied tree equals the seed tree"; else bad "the applied tree equals the seed tree" "$mismatches files differ"; fi
fi

((fails == 0)) || { echo "$fails test(s) failed"; exit 1; }
echo "all passed"
