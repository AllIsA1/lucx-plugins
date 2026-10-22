// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { describe, expect, it } from 'vitest';

import { PluginPreviewSchema, PluginsStateSchema } from '@/schemas/plugins';
import {
  isDangerousPermission,
  permissionKind,
  shortCommit,
  sourceUrl,
} from '@/pages/plugins/permissions';

describe('plugin permissions', () => {
  it('classifies every permission the backend grammar defines', () => {
    expect(permissionKind('network')).toBe('network');
    expect(permissionKind('api:clients:read')).toBe('apiRead');
    expect(permissionKind('api:inbounds:write')).toBe('apiWrite');
    expect(permissionKind('events:client.created')).toBe('events');
    expect(permissionKind('pipeline:xray.config')).toBe('pipeline');
    expect(permissionKind('ui:menu')).toBe('uiMenu');
    expect(permissionKind('ui:replace:/hosts')).toBe('uiReplace');
    expect(permissionKind('root')).toBe('unknown');
  });

  it('flags the same permissions as dangerous as the Go side', () => {
    const dangerous = [
      'network',
      'api:clients:write',
      'api:settings:write',
      'pipeline:sub.link',
      'ui:replace:/hosts',
      'api:settings:read',
      'api:nodes:read',
    ];
    const safe = ['api:clients:read', 'events:xray.crash', 'ui:menu', 'api:server:read'];
    for (const p of dangerous) expect(isDangerousPermission(p), p).toBe(true);
    for (const p of safe) expect(isDangerousPermission(p), p).toBe(false);
  });

  it('builds the canonical GitHub link, with the ref when pinned', () => {
    expect(sourceUrl({ owner: 'acme', repo: 'alerts' })).toBe('https://github.com/acme/alerts');
    expect(sourceUrl({ owner: 'acme', repo: 'alerts', ref: 'v1/x' })).toBe(
      'https://github.com/acme/alerts/tree/v1/x',
    );
    expect(shortCommit('0123456789abcdef')).toBe('0123456');
  });
});

describe('plugin schemas', () => {
  it('turns the null slices Go sends for empty lists into arrays', () => {
    const preview = PluginPreviewSchema.parse({
      token: 't',
      source: { owner: 'a', repo: 'b' },
      commit: 'c',
      manifest: {
        id: 'abc',
        name: 'Abc',
        version: '1.0.0',
        apiVersion: '1',
        type: 'ui',
        scope: 'master',
        permissions: null,
      },
      dangerous: null,
      installed: false,
      expiresAt: '2026-10-18T12:00:00Z',
    });
    expect(preview.manifest.permissions).toEqual([]);
    expect(preview.dangerous).toEqual([]);
  });

  it('rejects a state payload without the availability flags', () => {
    expect(PluginsStateSchema.safeParse({ plugins: [] }).success).toBe(false);
  });
});
