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
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    await waitFor(() => expect(pluginsApi.systemUpdate).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(/Updating to host-2/)).not.toBeNull();
    await waitFor(
      () => expect(onToast).toHaveBeenCalledWith('The plugin system was updated to host-2'),
      {
        timeout: 6000,
      },
    );
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
        current = {
          ...base,
          rolledBack: 'host-2',
          error: 'host-2 did not start properly and was rolled back',
        };
      }, 200);
      return done();
    });
    show({ ...base, available: 'host-2' });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Update' })[1]);
    expect(
      await screen.findByText(
        'host-2 did not start properly and was rolled back to the previous release.',
        {},
        { timeout: 6000 },
      ),
    ).not.toBeNull();
    expect(screen.queryByText(/Updating to host-2/)).toBeNull();
  }, 15000);

  it('shows the last error of the service', async () => {
    show({ ...base, error: 'the published host list is refused' });
    expect(await screen.findByText('the published host list is refused')).not.toBeNull();
  });
});

const panelBase = {
  managed: true,
  release: 'v3.10.2-lucx.312-plugins.1',
  available: '',
  checkedAt: '2026-10-25T12:00:00Z',
  busy: false,
  error: '',
};
const NEW = 'v3.10.2-lucx.314-plugins.1';

describe('SystemCard: the panel build', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.system).mockReset();
    vi.mocked(pluginsApi.systemCheck).mockReset();
    vi.mocked(pluginsApi.systemUpdate).mockReset();
    vi.mocked(pluginsApi.system).mockImplementation(async () => ok(current));
  });

  it('shows the panel build next to the plugin system', async () => {
    show({ ...base, panel: panelBase });
    expect(await screen.findByText('Installed release: host-1')).not.toBeNull();
    expect(screen.getByText('Panel build: v3.10.2-lucx.312-plugins.1')).not.toBeNull();
    expect(screen.getByText('The panel build is up to date.')).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
  });

  it('offers the update when only the panel build is newer', async () => {
    show({ ...base, panel: { ...panelBase, available: NEW } });
    expect(await screen.findByText('A newer panel build is available: ' + NEW)).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Update' }));
    expect(await screen.findByText('Update the panel build to ' + NEW + '?')).not.toBeNull();
  });

  it('updates the plugin system first and the panel after it', async () => {
    const order: string[] = [];
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async (part) => {
      order.push(part ?? 'host');
      if (!part) {
        setTimeout(() => {
          current = { ...current, version: 'host-2', available: '' };
        }, 200);
      } else {
        setTimeout(() => {
          current = { ...current, panel: { ...panelBase, release: NEW } };
        }, 200);
      }
      return done();
    });
    const { onToast, onUpdated } = show({
      ...base,
      available: 'host-2',
      panel: { ...panelBase, available: NEW },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    expect(
      await screen.findByText(
        'Update the plugin system to host-2 and the panel build to ' + NEW + '?',
      ),
    ).not.toBeNull();
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    await waitFor(() => expect(onToast).toHaveBeenCalledWith('The panel was updated to ' + NEW), {
      timeout: 12000,
    });
    expect(order).toEqual(['host', 'panel']);
    expect(onToast).toHaveBeenCalledWith('The plugin system was updated to host-2');
    expect(onUpdated).toHaveBeenCalledTimes(2);
    expect(await screen.findByRole('button', { name: 'Reload the page' })).not.toBeNull();
  }, 20000);

  it('does not touch the panel when the plugin system update fails', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockResolvedValue(new Msg<null>(false, 'no', null));
    show({ ...base, available: 'host-2', panel: { ...panelBase, available: NEW } });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    await waitFor(() => expect(pluginsApi.systemUpdate).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(pluginsApi.systemUpdate).toHaveBeenCalledTimes(1);
    expect(vi.mocked(pluginsApi.systemUpdate).mock.calls[0]).toEqual([]);
  });

  it('keeps the card while the panel restarts and the answer never comes', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      current = { ...current };
      // the panel is away: the status call fails for a while, then it is back with the new build
      vi.mocked(pluginsApi.system).mockImplementation(
        async () => new Msg<PluginSystem>(false, 'network error', null),
      );
      setTimeout(() => {
        current = { ...base, panel: { ...panelBase, release: NEW } };
        vi.mocked(pluginsApi.system).mockImplementation(async () => ok(current));
      }, 2500);
      return new Msg<null>(false, 'network error', null);
    });
    const { onToast } = show({ ...base, panel: { ...panelBase, available: NEW } });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    expect(await screen.findByText(/Updating the panel to /)).not.toBeNull();
    await new Promise((resolve) => setTimeout(resolve, 1800));
    expect(screen.queryByText('Plugin system')).not.toBeNull();
    await waitFor(() => expect(onToast).toHaveBeenCalledWith('The panel was updated to ' + NEW), {
      timeout: 8000,
    });
  }, 20000);

  it('says why the panel update failed instead of waiting for ever', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      setTimeout(() => {
        current = {
          ...base,
          panel: { ...panelBase, available: NEW, error: 'no verified builds list is available' },
        };
      }, 200);
      return new Msg<null>(false, 'plugins: cannot update the panel build', null);
    });
    show({ ...base, panel: { ...panelBase, available: NEW } });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    expect(
      await screen.findByText('no verified builds list is available', {}, { timeout: 6000 }),
    ).not.toBeNull();
    expect(screen.queryByText(/Updating the panel to /)).toBeNull();
  }, 15000);

  it('does not go on to the panel when the plugin system was rolled back', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      setTimeout(() => {
        current = {
          ...base,
          rolledBack: 'host-2',
          error: 'host-2 was rolled back',
          panel: { ...panelBase, available: NEW },
        };
      }, 200);
      return done();
    });
    show({ ...base, available: 'host-2', panel: { ...panelBase, available: NEW } });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    await screen.findByText(/did not start properly and was rolled back/, {}, { timeout: 6000 });
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(pluginsApi.systemUpdate).toHaveBeenCalledTimes(1);
  }, 15000);

  it('waits while the sync tool is still busy even if the release already shows', async () => {
    vi.mocked(pluginsApi.systemUpdate).mockImplementation(async () => {
      current = { ...base, panel: { ...panelBase, release: NEW, busy: true } };
      setTimeout(() => {
        current = { ...base, panel: { ...panelBase, release: NEW, busy: false } };
      }, 2500);
      return done();
    });
    const { onToast } = show({ ...base, panel: { ...panelBase, available: NEW } });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    fireEvent.click(
      (await screen.findAllByRole('button', { name: 'Update' })).at(-1) as HTMLElement,
    );
    await screen.findByText(/Updating the panel to /);
    await new Promise((resolve) => setTimeout(resolve, 1800));
    expect(onToast).not.toHaveBeenCalled();
    await waitFor(() => expect(onToast).toHaveBeenCalled(), { timeout: 6000 });
  }, 20000);

  it('shows the panel build on its own when the plugin system cannot update itself', async () => {
    show({ ...base, managed: false, panel: panelBase });
    expect(await screen.findByText('Panel build: v3.10.2-lucx.312-plugins.1')).not.toBeNull();
    expect(screen.queryByText(/Installed release/)).toBeNull();
  });

  it('check also reports a problem the check itself had', async () => {
    vi.mocked(pluginsApi.systemCheck).mockResolvedValue(
      new Msg<null>(false, 'panel build: no verified builds list', null),
    );
    show({ ...base, panel: panelBase });
    fireEvent.click(await screen.findByRole('button', { name: 'Check for updates' }));
    expect(await screen.findByText('panel build: no verified builds list')).not.toBeNull();
  });
});
