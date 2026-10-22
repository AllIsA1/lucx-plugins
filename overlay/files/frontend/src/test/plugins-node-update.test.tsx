// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import NodeUpdateModal from '@/pages/plugins/NodeUpdateModal';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type { NodePlugin, PluginEntry } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({ pluginsApi: { nodeUpdate: vi.fn() } }));

const master: PluginEntry = {
  id: 'blockads',
  name: 'Block ads',
  version: '1.3.0',
  type: 'process',
  scope: 'both',
  source: { owner: 'acme', repo: 'blockads' },
  commit: '2'.repeat(40),
  enabled: true,
  installedAt: '2026-10-18T12:00:00Z',
  permissions: ['api:clients:read', 'network'],
};

const onNode: NodePlugin = {
  id: 'blockads',
  name: 'Block ads',
  version: '1.2.0',
  type: 'process',
  scope: 'both',
  commit: 'f'.repeat(40),
  enabled: true,
  managedByMaster: true,
  permissions: ['api:clients:read'],
};

const apply = () => screen.getByRole('button', { name: 'Update on node' }) as HTMLButtonElement;

function open(overrides: { node?: NodePlugin; master?: PluginEntry } = {}) {
  const onDone = vi.fn();
  renderWithProviders(
    <NodeUpdateModal
      nodeId={3}
      nodeName="de-fra-1"
      onNode={overrides.node ?? onNode}
      master={overrides.master ?? master}
      onClose={vi.fn()}
      onDone={onDone}
    />,
  );
  return onDone;
}

describe('NodeUpdateModal', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.nodeUpdate).mockReset();
  });

  it('shows the versions and what the permissions do, and needs a tick for added ones', async () => {
    vi.mocked(pluginsApi.nodeUpdate).mockResolvedValue(new Msg<null>(true, '', null));
    const onDone = open();
    expect(screen.queryByText(/1\.2\.0 → 1\.3\.0/)).not.toBeNull();
    expect(screen.queryByText('network')).not.toBeNull();
    expect(apply().disabled).toBe(true);
    fireEvent.click(screen.getByRole('checkbox'));
    expect(apply().disabled).toBe(false);
    fireEvent.click(apply());
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(pluginsApi.nodeUpdate).toHaveBeenCalledWith(3, 'blockads', [
      'api:clients:read',
      'network',
    ]);
  });

  it('updates with one click when the master asks for nothing the node did not already grant', async () => {
    vi.mocked(pluginsApi.nodeUpdate).mockResolvedValue(new Msg<null>(true, '', null));
    open({
      node: { ...onNode, permissions: ['api:clients:read', 'network', 'events:client.created'] },
    });
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(apply().disabled).toBe(false);
    fireEvent.click(apply());
    await waitFor(() =>
      expect(pluginsApi.nodeUpdate).toHaveBeenCalledWith(3, 'blockads', [
        'api:clients:read',
        'network',
      ]),
    );
  });

  it('warns that the plugin is removed and installed again, with no way back', () => {
    open();
    expect(screen.queryByText(/removed from the node and installed again/)).not.toBeNull();
  });

  it('does not report success when the node refuses', async () => {
    vi.mocked(pluginsApi.nodeUpdate).mockResolvedValue(new Msg<null>(false, 'node said no', null));
    const onDone = open({ node: { ...onNode, permissions: master.permissions } });
    fireEvent.click(apply());
    await waitFor(() => expect(pluginsApi.nodeUpdate).toHaveBeenCalled());
    expect(onDone).not.toHaveBeenCalled();
  });
});
