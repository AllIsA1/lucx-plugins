// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import NodePluginsCard from '@/pages/plugins/NodePluginsCard';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type { NodePlugin, NodePluginStatus, PluginEntry, PluginPreview } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: {
    nodes: vi.fn(),
    nodeRefresh: vi.fn(),
    nodeInstall: vi.fn(),
    nodeInstallLink: vi.fn(),
    preview: vi.fn(),
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
  {
    nodeId: 3,
    nodeName: 'de-fra-1',
    state: 'ok',
    fetchedAt: 1,
    plugins: report(true, []),
  },
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
  screen.getAllByRole('button', {
    name: 'Install a plugin on this node',
  }) as HTMLButtonElement[];

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
    const confirm = within(dialog).getByRole('button', {
      name: 'Install',
    }) as HTMLButtonElement;
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
    const masters = ['blockads', 'current', 'local'].map((id) => ({
      ...blockAds,
      id,
      name: id,
    }));
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

function previewOf(scope: string): PluginPreview {
  return {
    token: 'tok-1',
    source: { owner: 'acme', repo: 'cores' },
    commit: COMMIT,
    manifest: {
      id: 'cores',
      name: 'Cores',
      version: '1.0.0',
      apiVersion: '1',
      type: 'process',
      scope,
      permissions: ['network', 'api:server:write'],
    },
    dangerous: ['api:server:write'],
    installed: false,
    expiresAt: '2026-10-25T13:00:00Z',
  };
}

describe('NodePluginsCard: install by link', () => {
  beforeEach(() => {
    for (const fn of Object.values(pluginsApi)) vi.mocked(fn).mockReset();
  });

  async function openLinkInstall(onToast = vi.fn()) {
    showNodes(nodes);
    renderWithProviders(<NodePluginsCard masterPlugins={[]} onToast={onToast} />);
    await screen.findByText('de-fra-1');
    // the node can take a plugin by link even when the master has none of its own to offer
    expect(installButtons()[0].disabled).toBe(false);
    fireEvent.click(installButtons()[0]);
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Plugin repository link' }), {
      target: { value: 'https://github.com/acme/cores' },
    });
    return { dialog, onToast };
  }

  it('checks the link on this panel, then installs exactly what was checked on the node', async () => {
    vi.mocked(pluginsApi.preview).mockResolvedValue(new Msg(true, '', previewOf('both')));
    vi.mocked(pluginsApi.nodeInstallLink).mockResolvedValue(new Msg<null>(true, '', null));
    const { dialog, onToast } = await openLinkInstall();
    const confirm = within(dialog).getByRole('button', {
      name: 'Install',
    }) as HTMLButtonElement;

    fireEvent.click(within(dialog).getByRole('button', { name: 'Check plugin' }));
    await within(dialog).findByText('Cores 1.0.0');
    expect(pluginsApi.preview).toHaveBeenCalledWith('https://github.com/acme/cores');
    expect(confirm.disabled).toBe(true); // the permissions were not accepted yet
    fireEvent.click(within(dialog).getByRole('checkbox'));
    expect(confirm.disabled).toBe(false);
    fireEvent.click(confirm);

    await waitFor(() => expect(onToast).toHaveBeenCalledTimes(1));
    expect(pluginsApi.nodeInstallLink).toHaveBeenCalledWith(3, 'tok-1', [
      'network',
      'api:server:write',
    ]);
    expect(pluginsApi.nodeInstall).not.toHaveBeenCalled();
  });

  it('does not offer a plugin made for the master panel only', async () => {
    vi.mocked(pluginsApi.preview).mockResolvedValue(new Msg(true, '', previewOf('master')));
    const { dialog } = await openLinkInstall();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check plugin' }));
    await within(dialog).findByText(/made for the master panel only/);
    expect(within(dialog).queryByRole('checkbox')).toBeNull();
    expect(
      (
        within(dialog).getByRole('button', {
          name: 'Install',
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it('shows nothing to accept when the link could not be checked', async () => {
    vi.mocked(pluginsApi.preview).mockResolvedValue(
      new Msg<PluginPreview>(false, 'not a plugin', null),
    );
    const { dialog } = await openLinkInstall();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check plugin' }));
    await waitFor(() => expect(pluginsApi.preview).toHaveBeenCalled());
    expect(within(dialog).queryByRole('checkbox')).toBeNull();
    expect(
      (
        within(dialog).getByRole('button', {
          name: 'Install',
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it('does not claim success when the node refuses, and a new link needs a new check', async () => {
    vi.mocked(pluginsApi.preview).mockResolvedValue(new Msg(true, '', previewOf('node')));
    vi.mocked(pluginsApi.nodeInstallLink).mockResolvedValue(new Msg<null>(false, 'refused', null));
    const { dialog, onToast } = await openLinkInstall();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check plugin' }));
    await within(dialog).findByText('Cores 1.0.0');
    fireEvent.click(within(dialog).getByRole('checkbox'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Install' }));
    await waitFor(() => expect(pluginsApi.nodeInstallLink).toHaveBeenCalled());
    expect(onToast).not.toHaveBeenCalled();

    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Plugin repository link' }), {
      target: { value: 'https://github.com/acme/other' },
    });
    expect(within(dialog).queryByText('Cores 1.0.0')).toBeNull();
    await waitFor(() =>
      expect(
        (
          within(dialog).getByRole('button', {
            name: /Install/,
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true),
    );
  });
});
