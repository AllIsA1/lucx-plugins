// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');

describe('replaceable pages', () => {
  it('are the same ones the backend lets a plugin replace', () => {
    const catalog = read('../../../internal/lucx/plugins/ui_catalog.go');
    const goRoutes = /replaceableRoutes = \[\]string\{([^}]*)\}/
      .exec(catalog)?.[1]
      .match(/"[^"]*"/g)
      ?.map((r) => r.slice(1, -1));
    const uiRoutes = Array.from(read('../routes.tsx').matchAll(/replaceable\('([^']*)'/g)).map(
      (m) => m[1],
    );

    expect(goRoutes?.length).toBeGreaterThan(0);
    expect([...uiRoutes].sort()).toEqual([...(goRoutes ?? [])].sort());
  });
});
