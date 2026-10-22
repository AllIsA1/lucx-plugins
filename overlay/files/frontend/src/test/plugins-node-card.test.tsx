// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import NodePluginsCard from '@/pages/plugins/NodePluginsCard';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type { NodePlugin, NodePluginStatus, PluginEntry } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: {
    nodes: vi.fn(),
    nodeRefresh: vi.fn(),
    nodeInstall: vi.fn(),
    nodeSetEnabled: vi.fn(),
    nodeRestart: vi.fn(),
    nodeUninstall: vi.fn(),
    nodeUpdate: vi.fn(),
    nodeLogs: vi.fn(),
  },
}));

const COMMIT = '0123456789abcdef0123456789abcdef01234567';

const blockAds: PluginEntry = {
  id: 'blockads',
  name: 'Block ads',
  version: '1.2.0',
  type: 'process',
  scope: 'both',
  source: { owner: 'acme', repo: 'blockads' },
  commit: COMMIT,
  enabled: true,
  installedAt: '2026-10-18T12:00:00Z',
  permissions: ['network', 'pipeline:xray.config'],
};

function report(allow: boolean, plugins: NodePlugin[]) {
  return {
    available: true,
    allowMasterManagement: allow,
    processesAvailable: true,
    processesError: '',
    safeMode: false,
    error: '',
    plugins,
  };
}

const nodes: NodePluginStatus[] = [
  { nodeId: 3, nodeName: 'de-fra-1', state: 'ok', fetchedAt: 1, plugins: report(true, []) },
  {
    nodeId: 4,
    nodeName: 'locked-node',
    state: 'locked',
    error: 'plugin management by the master is disabled on this node',
    fetchedAt: 1,
  },
  { nodeId: 5, nodeName: 'old-node', state: 'unsupported', fetchedAt: 0 },
];

function showNodes(list: NodePluginStatus[]) {
  vi.mocked(pluginsApi.nodes).mockResolvedValue(new Msg<NodePluginStatus[]>(true, '', list));
}

const installButtons = () =>
  screen.getAllByRole('button', { name: 'Install a plugin on this node' }) as HTMLButtonElement[];

describe('NodePluginsCard', () => {
  beforeEach(() => {
    for (const fn of Object.values(pluginsApi)) vi.mocked(fn).mockReset();
  });

  it('allows installing only on a node whose owner allowed the master', async () => {
    showNodes(nodes);
    renderWithProviders(<NodePluginsCard masterPlugins={[blockAds]} onToast={vi.fn()} />);
    await screen.findByText('de-fra-1');

    const [allowed, locked, unsupported] = installButtons();
    expect(allowed.disabled).toBe(false);
    expect(locked.disabled).toBe(true);
    expect(unsupported.disabled).toBe(true);
    expect(screen.queryByText('Management by this panel is off on the node')).not.toBeNull();
    expect(screen.queryByText('No plugin support')).not.toBeNull();
    expect(screen.queryByText('Management off')).not.toBeNull();
  });

  it('installs the plugin the master has, only after the permissions were accepted', async () => {
    showNodes(nodes);
    vi.mocked(pluginsApi.nodeInstall).mockResolvedValue(new Msg<null>(true, '', null));
    const onToast = vi.fn();
    renderWithProviders(<NodePluginsCard masterPlugins={[blockAds]} onToast={onToast} />);
    await screen.findByText('de-fra-1');

    fireEvent.click(installButtons()[0]);
    const dialog = await screen.findByRole('dialog');
    const confirm = within(dialog).getByRole('button', { name: 'Install' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(within(dialog).getByRole('checkbox'));
    expect(confirm.disabled).toBe(false);
    fireEvent.click(confirm);

    await waitFor(() => expect(onToast).toHaveBeenCalledTimes(1));
    expect(pluginsApi.nodeInstall).toHaveBeenCalledWith(3, 'blockads', [
      'network',
      'pipeline:xray.config',
    ]);
  });

  it('does not claim success when the node refuses the install', async () => {
    showNodes(nodes);
    vi.mocked(pluginsApi.nodeInstall).mockResolvedValue(new Msg<null>(false, 'refused', null));
    const onToast = vi.fn();
    renderWithProviders(<NodePluginsCard masterPlugins={[blockAds]} onToast={onToast} />);
    await screen.findByText('de-fra-1');

    fireEvent.click(installButtons()[0]);
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('checkbox'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Install' }));

    await waitFor(() => expect(pluginsApi.nodeInstall).toHaveBeenCalled());
    expect(onToast).not.toHaveBeenCalled();
  });

  it('lets the master switch only the plugins it installed, and flags a different version', async () => {
    showNodes([
      {
        nodeId: 3,
        nodeName: 'de-fra-1',
        state: 'ok',
        fetchedAt: 1,
        plugins: report(true, [
          {
            id: 'blockads',
            name: 'Block ads',
            version: '1.0.0',
            type: 'process',
            scope: 'both',
            commit: 'f'.repeat(40),
            enabled: true,
            managedByMaster: true,
            permissions: [],
          },
          {
            id: 'mine',
            name: 'Mine',
            version: '1.0.0',
            type: 'process',
            scope: 'node',
            commit: COMMIT,
            enabled: true,
            permissions: [],
          },
        ]),
      },
    ]);
    vi.mocked(pluginsApi.nodeSetEnabled).mockResolvedValue(new Msg<null>(true, '', null));
    renderWithProviders(<NodePluginsCard masterPlugins={[blockAds]} onToast={vi.fn()} />);
    await screen.findByText('de-fra-1');
    fireEvent.click(screen.getByRole('button', { name: /expand row/i }));

    await screen.findByText('Different version');
    const switches = screen.getAllByRole('switch') as HTMLButtonElement[];
    expect(switches).toHaveLength(2);
    expect(switches[1].disabled).toBe(true);
    fireEvent.click(switches[0]);
    await waitFor(() =>
      expect(pluginsApi.nodeSetEnabled).toHaveBeenCalledWith(3, 'blockads', false),
    );
  });

  it('offers to bring a master-managed plugin on a different version up to date, and only that', async () => {
    const plugin = (id: string, overrides: Partial<NodePlugin> = {}): NodePlugin => ({
      id,
      name: id,
      version: '1.0.0',
      type: 'process',
      scope: 'both',
      commit: 'f'.repeat(40),
      enabled: true,
      managedByMaster: true,
      permissions: [],
      ...overrides,
    });
    showNodes([
      {
        nodeId: 3,
        nodeName: 'de-fra-1',
        state: 'ok',
        fetchedAt: 1,
        plugins: report(true, [
          plugin('blockads'),
          plugin('current', { commit: COMMIT }),
          plugin('local', { managedByMaster: false }),
        ]),
      },
    ]);
    const masters = ['blockads', 'current', 'local'].map((id) => ({ ...blockAds, id, name: id }));
    renderWithProviders(<NodePluginsCard masterPlugins={masters} onToast={vi.fn()} />);
    await screen.findByText('de-fra-1');
    fireEvent.click(screen.getByRole('button', { name: /expand row/i }));
    await screen.findAllByText('blockads');

    const buttons = screen.getAllByRole('button', { name: 'Update on node' });
    expect(buttons).toHaveLength(1);
    fireEvent.click(buttons[0]);
    expect((await screen.findAllByText(/Update blockads on de-fra-1/)).length).toBeGreaterThan(0);
  });
});
