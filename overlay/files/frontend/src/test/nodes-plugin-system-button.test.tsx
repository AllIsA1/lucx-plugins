// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import NodePluginSystemButton, {
  PLUGIN_SYSTEM_INSTALL_COMMAND,
  pluginSystemStep,
} from '@/pages/nodes/NodePluginSystemButton';
import { pluginsApi } from '@/api/plugins';
import type { NodePluginStatus } from '@/schemas/plugins';
import { Msg } from '@/utils';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({ pluginsApi: { nodes: vi.fn() } }));

const report0 = (over: { available: boolean; allowMasterManagement: boolean }) => ({
  ...over,
  processesAvailable: true,
  processesError: '',
  safeMode: false,
  error: '',
  plugins: [],
});

const status = (over: Partial<NodePluginStatus>): NodePluginStatus => ({
  nodeId: 1,
  nodeName: 'pl-warsaw',
  state: 'ok',
  fetchedAt: 0,
  plugins: report0({ available: true, allowMasterManagement: true }),
  ...over,
});

const report = (list: NodePluginStatus[]) =>
  vi.mocked(pluginsApi.nodes).mockResolvedValue(new Msg<NodePluginStatus[]>(true, '', list));

const open = () => fireEvent.click(screen.getByRole('button', { name: /plugin system on the node/i }));

describe('pluginSystemStep', () => {
  it('says what the node needs next', () => {
    expect(pluginSystemStep(undefined)).toBe('wait');
    expect(pluginSystemStep(status({ state: 'unsupported', plugins: null }))).toBe('install');
    expect(pluginSystemStep(status({ state: 'locked' }))).toBe('allow');
    expect(pluginSystemStep(status({ state: 'ok' }))).toBe('ready');
    expect(
      pluginSystemStep(status({ plugins: report0({ available: true, allowMasterManagement: false }) })),
    ).toBe('allow');
    expect(pluginSystemStep(status({ state: 'pending' }))).toBe('wait');
    expect(pluginSystemStep(status({ state: 'offline' }))).toBe('failed');
    expect(pluginSystemStep(status({ state: 'error', error: 'x' }))).toBe('failed');
  });
});

describe('NodePluginSystemButton', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.nodes).mockReset();
  });

  it('does not ask the node until the dialog is opened', () => {
    report([]);
    renderWithProviders(<NodePluginSystemButton nodeId={1} nodeName="pl-warsaw" />);
    expect(pluginsApi.nodes).not.toHaveBeenCalled();
  });

  it('offers the install command for a node without a plugin system', async () => {
    report([status({ state: 'unsupported', plugins: null })]);
    renderWithProviders(<NodePluginSystemButton nodeId={1} nodeName="pl-warsaw" />);
    open();
    await waitFor(() => expect(screen.queryByText(PLUGIN_SYSTEM_INSTALL_COMMAND)).not.toBeNull());
    expect(screen.queryByText(/has no plugin system/i)).not.toBeNull();
    expect(screen.queryByRole('button', { name: /copy the command/i })).not.toBeNull();
  });

  it('copies exactly the command', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    report([status({ state: 'unsupported', plugins: null })]);
    renderWithProviders(<NodePluginSystemButton nodeId={1} nodeName="pl-warsaw" />);
    open();
    fireEvent.click(await screen.findByRole('button', { name: /copy the command/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(PLUGIN_SYSTEM_INSTALL_COMMAND));
  });

  it('tells a node that has the system but is locked to allow the master, without the command', async () => {
    report([status({ state: 'locked' })]);
    renderWithProviders(<NodePluginSystemButton nodeId={1} nodeName="pl-warsaw" />);
    open();
    await waitFor(() => expect(screen.queryByText(/does not let this panel manage/i)).not.toBeNull());
    expect(screen.queryByText(PLUGIN_SYSTEM_INSTALL_COMMAND)).toBeNull();
  });

  it('says a ready node is ready, and looks only at its own row', async () => {
    report([status({ nodeId: 2, state: 'unsupported', plugins: null }), status({ nodeId: 1 })]);
    renderWithProviders(<NodePluginSystemButton nodeId={1} nodeName="pl-warsaw" />);
    open();
    await waitFor(() => expect(screen.queryByText(/node is ready/i)).not.toBeNull());
    expect(screen.queryByText(PLUGIN_SYSTEM_INSTALL_COMMAND)).toBeNull();
  });
});
