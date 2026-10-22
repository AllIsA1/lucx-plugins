// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { pluginIcon } from '@/pages/plugins/pluginIcons';
import { AppstoreOutlined } from '@ant-design/icons';

const examples = fileURLToPath(new URL('../../../examples/', import.meta.url));

describe('example plugin menu entries', () => {
  it('use icons the sidebar knows, not the generic fallback', () => {
    const seen: string[] = [];
    for (const dir of readdirSync(examples)) {
      const manifest = `${examples}${dir}/lucx-plugin.json`;
      if (!existsSync(manifest)) continue;
      const { ui } = JSON.parse(readFileSync(manifest, 'utf8')) as {
        ui?: { menu?: { icon?: string }[] };
      };
      for (const item of ui?.menu ?? []) {
        seen.push(`${dir}:${item.icon}`);
        expect(pluginIcon(item.icon ?? ''), `${dir} icon ${item.icon}`).not.toBe(AppstoreOutlined);
      }
    }
    expect(seen.length).toBeGreaterThanOrEqual(2);
  });
});
