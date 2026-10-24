// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { PluginPreviewSchema, PluginsStateSchema } from '@/schemas/plugins';
import {
  activeSeams,
  isDangerousPermission,
  permissionKind,
  seamPermission,
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
    expect(permissionKind('seam:sub')).toBe('seam');
    expect(permissionKind('seam:api:server')).toBe('seam');
    expect(permissionKind('seam:api:*')).toBe('seam');
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
      'seam:sub',
      'seam:api:server',
      'seam:api:*',
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

describe('plugin seams', () => {
  it('names the permission a seam needs the way the backend does', () => {
    expect(seamPermission({ engine: 'sub', mode: 'after', route: '/links/:subid' })).toBe(
      'seam:sub',
    );
    expect(seamPermission({ engine: 'api', mode: 'before', route: '/server/status' })).toBe(
      'seam:api:server',
    );
    expect(seamPermission({ engine: 'api', mode: 'after', route: '/clients/*' })).toBe(
      'seam:api:clients',
    );
    expect(seamPermission({ engine: 'api', mode: 'after', route: '*' })).toBe('seam:api:*');
  });

  it('lists only the seams whose permission was granted', () => {
    const seams = [
      { engine: 'sub', mode: 'after', route: '/links/:subid' },
      { engine: 'api', mode: 'before', route: '/server/status' },
      { engine: 'api', mode: 'after', route: '/clients/list' },
    ];
    expect(
      activeSeams({ permissions: ['seam:sub', 'seam:api:server'], seams }).map((s) => s.route),
    ).toEqual(['/links/:subid', '/server/status']);
    expect(activeSeams({ permissions: ['seam:api:*'], seams: seams.slice(1) })).toHaveLength(0);
    expect(activeSeams({ permissions: ['seam:sub'] })).toEqual([]);
  });
});

describe('plugin schemas', () => {
  it('keeps the seams and their counters of a plugin', () => {
    const state = PluginsStateSchema.parse({
      available: true,
      allowMasterManagement: false,
      processesAvailable: true,
      processesError: '',
      safeMode: false,
      error: '',
      archiveBase: '',
      customArchiveBase: '',
      defaultArchiveBase: '',
      plugins: [
        {
          id: 'seam-demo',
          name: 'Seam Demo',
          version: '1.0.0',
          type: 'process',
          scope: 'both',
          source: { owner: 'a', repo: 'b' },
          commit: 'c',
          enabled: true,
          installedAt: '2026-10-18T12:00:00Z',
          permissions: ['seam:sub'],
          seams: [{ engine: 'sub', mode: 'after', route: '/links/:subid', methods: ['GET'] }],
          runtime: {
            state: 'running',
            healthy: true,
            pid: 1,
            restarts: 0,
            seamCalls: 7,
            seamErrors: 1,
            seamSkipped: 2,
          },
        },
      ],
    });
    const plugin = state.plugins[0];
    expect(plugin.seams).toEqual([
      { engine: 'sub', mode: 'after', route: '/links/:subid', methods: ['GET'] },
    ]);
    expect(plugin.runtime?.seamCalls).toBe(7);
    expect(plugin.runtime?.seamSkipped).toBe(2);
  });

  it('accepts a plugin that declares no seams', () => {
    const preview = PluginPreviewSchema.parse({
      token: 't',
      source: { owner: 'a', repo: 'b' },
      commit: 'c',
      manifest: {
        id: 'abc',
        name: 'Abc',
        version: '1.0.0',
        apiVersion: '1',
        type: 'process',
        scope: 'master',
        permissions: null,
      },
      dangerous: null,
      installed: false,
      expiresAt: 'x',
    });
    expect(preview.manifest.seams).toBeUndefined();
  });

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
