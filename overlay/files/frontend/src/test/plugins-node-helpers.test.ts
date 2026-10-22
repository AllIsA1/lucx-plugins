// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { describe, expect, it } from 'vitest';

import { canManageNode, installCandidates, pluginDrift } from '@/pages/plugins/nodePlugins';
import type { NodePlugin, NodePluginStatus, PluginEntry } from '@/schemas/plugins';

const COMMIT = '0123456789abcdef0123456789abcdef01234567';

function master(id: string, overrides: Partial<PluginEntry> = {}): PluginEntry {
  return {
    id,
    name: id,
    version: '1.0.0',
    type: 'process',
    scope: 'both',
    source: { owner: 'acme', repo: id },
    commit: COMMIT,
    enabled: true,
    installedAt: '2026-10-18T12:00:00Z',
    permissions: [],
    ...overrides,
  };
}

function onNode(id: string, overrides: Partial<NodePlugin> = {}): NodePlugin {
  return {
    id,
    name: id,
    version: '1.0.0',
    type: 'process',
    scope: 'both',
    commit: COMMIT,
    enabled: false,
    permissions: [],
    ...overrides,
  };
}

function status(overrides: Partial<NodePluginStatus> = {}): NodePluginStatus {
  return {
    nodeId: 1,
    nodeName: 'edge',
    state: 'ok',
    fetchedAt: 1,
    plugins: {
      available: true,
      allowMasterManagement: true,
      processesAvailable: true,
      processesError: '',
      safeMode: false,
      error: '',
      plugins: [],
    },
    ...overrides,
  };
}

describe('pluginDrift', () => {
  it('tells a node on the master commit from one on another commit', () => {
    expect(pluginDrift([master('a')], onNode('a'))).toBe('same');
    expect(pluginDrift([master('a')], onNode('a', { commit: 'f'.repeat(40) }))).toBe('differs');
  });

  it('has nothing to compare a plugin the master does not know', () => {
    expect(pluginDrift([master('a')], onNode('b'))).toBe('unknown');
  });
});

describe('canManageNode', () => {
  it('needs a readable node whose owner allowed the master', () => {
    expect(canManageNode(status())).toBe(true);
    expect(canManageNode(status({ state: 'offline' }))).toBe(false);
    expect(canManageNode(status({ state: 'pending', plugins: null }))).toBe(false);
    const locked = status();
    if (locked.plugins) locked.plugins.allowMasterManagement = false;
    expect(canManageNode(locked)).toBe(false);
  });
});

describe('installCandidates', () => {
  it('offers only plugins that can run on a node and are not there yet', () => {
    const plugins = [
      master('both'),
      master('node', { scope: 'node' }),
      master('only', { scope: 'master' }),
      master('there'),
    ];
    const s = status();
    s.plugins?.plugins.push(onNode('there'));
    expect(installCandidates(plugins, s).map((p) => p.id)).toEqual(['both', 'node']);
  });
});
