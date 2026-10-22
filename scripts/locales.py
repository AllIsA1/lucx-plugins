#!/usr/bin/env python3
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
"""Locale files: `diff` keeps only what the plugin system added to an upstream file, `merge` puts it back.

Both read and write the exact layout upstream uses (2-space JSON, UTF-8, trailing newline), so a merged file
only differs from upstream by the added keys.
"""
import collections
import json
import sys


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f, object_pairs_hook=collections.OrderedDict)


def dump(data, path):
    with open(path, "w", encoding="utf-8") as f:
        f.write(json.dumps(data, indent=2, ensure_ascii=False) + "\n")


def diff(base, ours):
    """Entries of `ours` that are new or changed against `base`, as a nested mapping."""
    out = collections.OrderedDict()
    for key, value in ours.items():
        if key not in base:
            out[key] = value
        elif isinstance(value, dict) and isinstance(base[key], dict):
            nested = diff(base[key], value)
            if nested:
                out[key] = nested
        elif value != base[key]:
            out[key] = value
    return out


def merge(target, extra, path=""):
    for key, value in extra.items():
        here = f"{path}.{key}" if path else key
        if isinstance(value, dict) and isinstance(target.get(key), dict):
            merge(target[key], value, here)
        else:
            target[key] = value


def main(argv):
    if len(argv) == 4 and argv[0] == "diff":
        dump(diff(load(argv[1]), load(argv[2])), argv[3])
    elif len(argv) == 3 and argv[0] == "merge":
        data = load(argv[1])
        merge(data, load(argv[2]))
        dump(data, argv[1])
    else:
        sys.exit("usage: locales.py diff <upstream.json> <ours.json> <out.json> | merge <target.json> <overlay.json>")


if __name__ == "__main__":
    main(sys.argv[1:])
