#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Rebuilds the generated files the overlay does not carry (OpenAPI document and Zod schemas) in a tree
# that has the overlay applied. Needs Go and, in frontend/, installed npm dependencies on Node 26.
set -euo pipefail
tree=$(cd "${1:?usage: generate.sh <tree>}" && pwd)
(cd "$tree/frontend" && npm run gen)
