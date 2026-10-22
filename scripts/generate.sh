#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# Licensed under the PolyForm Noncommercial License 1.0.0.
# Commercial use (including VPN resale) requires explicit written permission from the author.
# SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0
# Rebuilds the generated files the overlay does not carry (OpenAPI document and Zod schemas) in a tree
# that has the overlay applied. Needs Go and, in frontend/, installed npm dependencies on Node 26.
set -euo pipefail
tree=$(cd "${1:?usage: generate.sh <tree>}" && pwd)
(cd "$tree/frontend" && npm run gen)
