// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import SystemCard from '@/pages/plugins/SystemCard';
import { pluginsApi, type PluginSystem } from '@/api/plugins';
import { Msg } from '@/utils';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: { system: vi.fn(), systemCheck: vi.fn(), systemUpdate: vi.fn() },
}));

const base: PluginSystem = {
  managed: true,
  version: 'host-1',
  available: '',
  checkedAt: '2026-10-23T12:00:00Z',
  busy: false,
  pending: '',
  rolledBack: '',
  error: '',
};

let current: PluginSystem;

const ok = (obj: PluginSystem) => new Msg<PluginSystem>(true, '', obj);
const done = () => new Msg<null>(true, '', null);

function show(status: PluginSystem) {
  current = status;
  const onUpdated = vi.fn();
  const onToast = vi.fn();
  renderWithProviders(<SystemCard onUpdated={onUpdated} onToast={onToast} />);
  return { onUpdated, onToast };
}

describe('SystemCard', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.system).mockReset();
    vi.mocked(pluginsApi.systemCheck).mockReset();
    vi.mocked(pluginsApi.systemUpdate).mockReset();
    vi.mocked(pluginsApi.system).mockImplementation(async () => ok(current));
  });

  it('is not shown for a build that does not update itself', async () => {
    show({ ...base, managed: false });
    await waitFor(() => expect(pluginsApi.system).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(screen.queryByText('Plugin system')).toBeNull();
  });

  it('shows the release and says it is up to date, with no update button', async () => {
    show(base);
    expect(await screen.findByText('Installed release: host-1')).not.toBeNull();
    expect(screen.getByText(/The plugin system is up to date\./)).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
  });

  it('checks on request and shows what the check found', async () => {
    vi.mocked(pluginsApi.systemCheck).mockImplementation(async () => {
      current = { ...base, available: 'host-2' };
      return done();
    });
    show(base);
    fireEvent.click(await screen.findByRole('button', { name: 'Check for updates' }));
    expect(await screen.findByText('A newer release is available: host-2')).not.toBeNull();
    expect(pluginsApi.systemCheck).toHaveBeenCalledTimes(1);
  });

  it('asks before updating, and does nothing when the question is declined', async () => {
    show({ ...base, available: 'host-2' });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    expect(await screen.findByText('Update the plugin system to host-2?')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(pluginsApi.systemUpdate).not.toHaveBeenCalled();
    expect(screen.queryByText(/Updating to host-2/)).toBeNull();
  });

  it('updates, waits for the service to come back as the new release and reports it', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      // The service goes away and returns as host-2 a moment later.
      setTimeout(() => {
        current = { ...base, version: 'host-2' };
      }, 200);
      return done();
    });
    const { onToast, onUpdated } = show({ ...base, available: 'host-2' });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click((await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement);
    await waitFor(() => expect(pluginsApi.systemUpdate).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(/Updating to host-2/)).not.toBeNull();
    await waitFor(() => expect(onToast).toHaveBeenCalledWith('The plugin system was updated to host-2'), {
      timeout: 6000,
    });
    expect(onUpdated).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/Updating to host-2/)).toBeNull();
  }, 15000);

  it('does not wait when the update is refused', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockResolvedValue(new Msg<null>(false, 'no', null));
    show({ ...base, available: 'host-2' });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Update' })[1]);
    await waitFor(() => expect(pluginsApi.systemUpdate).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(screen.queryByText(/Updating to host-2/)).toBeNull();
  });

  it('says so when the new release was rolled back', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      setTimeout(() => {
        current = { ...base, rolledBack: 'host-2', error: 'host-2 did not start properly and was rolled back' };
      }, 200);
      return done();
    });
    show({ ...base, available: 'host-2' });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Update' })[1]);
    expect(
      await screen.findByText('host-2 did not start properly and was rolled back to the previous release.', {}, { timeout: 6000 }),
    ).not.toBeNull();
    expect(screen.queryByText(/Updating to host-2/)).toBeNull();
  }, 15000);

  it('shows the last error of the service', async () => {
    show({ ...base, error: 'the published host list is refused' });
    expect(await screen.findByText('the published host list is refused')).not.toBeNull();
  });
});
