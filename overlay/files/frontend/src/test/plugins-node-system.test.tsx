// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import NodeSystemModal from '@/pages/plugins/NodeSystemModal';
import { pluginsApi, type PluginSystem } from '@/api/plugins';
import { Msg } from '@/utils';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: { nodeSystem: vi.fn(), nodeSystemCheck: vi.fn(), nodeSystemUpdate: vi.fn() },
}));

function system(over: Partial<PluginSystem> = {}): PluginSystem {
  return {
    managed: true,
    version: 'host-6',
    available: '',
    checkedAt: '2026-10-27T10:00:00Z',
    busy: false,
    pending: '',
    rolledBack: '',
    error: '',
    ...over,
  };
}

const reply = (s: PluginSystem) => new Msg<PluginSystem>(true, '', s);

function open() {
  const onDone = vi.fn();
  const onClose = vi.fn();
  renderWithProviders(
    <NodeSystemModal nodeId={3} nodeName="de-fra-1" onClose={onClose} onDone={onDone} />,
  );
  return { onDone, onClose };
}

const updateButton = () => screen.getByRole('button', { name: 'Update' }) as HTMLButtonElement;

describe('NodeSystemModal', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.nodeSystem).mockReset();
    vi.mocked(pluginsApi.nodeSystemCheck).mockReset();
    vi.mocked(pluginsApi.nodeSystemUpdate).mockReset();
  });

  it('shows what the node runs and has nothing to update when it is current', async () => {
    vi.mocked(pluginsApi.nodeSystem).mockResolvedValue(reply(system()));
    open();
    await screen.findByText(/host-6/);
    expect(updateButton().disabled).toBe(true);
  });

  it('offers the newer release and asks the node to update itself', async () => {
    vi.mocked(pluginsApi.nodeSystem)
      .mockResolvedValueOnce(reply(system({ available: 'host-7' })))
      .mockResolvedValue(reply(system({ version: 'host-7' })));
    vi.mocked(pluginsApi.nodeSystemUpdate).mockResolvedValue(new Msg<null>(true, '', null));
    const { onDone } = open();
    await screen.findByText(/host-7/);
    expect(updateButton().disabled).toBe(false);
    fireEvent.click(updateButton());
    await waitFor(() => expect(pluginsApi.nodeSystemUpdate).toHaveBeenCalledWith(3), {
      timeout: 1000,
    });
    await waitFor(() => expect(onDone).toHaveBeenCalled(), { timeout: 8000 });
    expect(pluginsApi.nodeSystemUpdate).toHaveBeenCalledTimes(1); // no panel build to move
  }, 15000);

  it("shows the node's refusal and stops", async () => {
    vi.mocked(pluginsApi.nodeSystem).mockResolvedValue(reply(system({ available: 'host-7' })));
    vi.mocked(pluginsApi.nodeSystemUpdate).mockResolvedValue(
      new Msg<null>(false, 'plugin management by the master is disabled on this node', null),
    );
    const { onDone } = open();
    await screen.findByText(/host-7/);
    fireEvent.click(updateButton());
    await screen.findByText(/disabled on this node/);
    expect(onDone).not.toHaveBeenCalled();
  });

  it('says so when the node does not answer', async () => {
    vi.mocked(pluginsApi.nodeSystem).mockResolvedValue(
      new Msg<PluginSystem>(false, 'the node did not answer', null),
    );
    open();
    await screen.findByText(/the node did not answer/);
    expect(updateButton().disabled).toBe(true);
  });

  it('checks for a newer release on the node', async () => {
    vi.mocked(pluginsApi.nodeSystem).mockResolvedValue(reply(system()));
    vi.mocked(pluginsApi.nodeSystemCheck).mockResolvedValue(new Msg<null>(true, '', null));
    open();
    await screen.findByText(/host-6/);
    fireEvent.click(screen.getByRole('button', { name: 'Check for updates' }));
    await waitFor(() => expect(pluginsApi.nodeSystemCheck).toHaveBeenCalledWith(3));
  });
});
