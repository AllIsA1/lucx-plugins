import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import CoresTab from '@/pages/settings/CoresTab';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';

const PLUGIN = '/panel/api/plugins/x/better-cores/api/cores';

function state(overrides: Record<string, unknown> = {}) {
  return {
    checkedAt: '2026-10-29T12:00:00Z',
    nextCheckAt: '2026-10-30T12:00:00Z',
    checking: false,
    cores: [
      {
        id: 'mieru',
        name: 'mieru',
        latest: {
          tag: 'v3.40.0',
          publishedAt: '2026-10-28T00:00:00Z',
          url: 'https://example.com/r',
        },
        installed: { tag: 'v3.39.0', at: '2026-10-29T12:00:00Z' },
        updateAvailable: true,
        installing: false,
      },
      {
        id: 'openflux',
        name: 'OpenFlux',
        latest: { tag: 'v0.13.0' },
        updateAvailable: false,
        installing: false,
      },
    ],
    ...overrides,
  };
}

function mockGets(plugin: () => Msg<unknown>) {
  return vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (String(url) === PLUGIN) return plugin();
    return new Msg(true, '', {
      core: 'naive',
      binaryExists: true,
      binaryPath: 'bin/x',
      probe: { running: false },
    });
  });
}

function renderCores() {
  return render(
    <MemoryRouter>
      <QueryClientProvider client={makeTestQueryClient()}>
        <CoresTab />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function cardOf(title: string) {
  const el = screen.getAllByText(title, { selector: '.ant-card-head-title' })[0];
  return el.closest('.ant-card') as HTMLElement;
}

beforeEach(() => {
  vi.spyOn(HttpUtil, 'post').mockImplementation(async () => new Msg(true, '', state()));
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('core updates on the Cores page', () => {
  it('shows nothing of it where the plugin is not installed', async () => {
    mockGets(() => new Msg(false, 'plugin not found'));
    renderCores();
    await screen.findAllByText('NaiveProxy');
    await waitFor(() =>
      expect(HttpUtil.get).toHaveBeenCalledWith(PLUGIN, undefined, { silent: true }),
    );
    expect(screen.queryByText('Core updates')).toBeNull();
    expect(screen.queryByRole('button', { name: /update to|install v/i })).toBeNull();
  });

  it('shows nothing when whatever answers there is not the plugin', async () => {
    mockGets(() => new Msg(true, '', { core: 'naive', binaryExists: true }));
    renderCores();
    await screen.findAllByText('NaiveProxy');
    await waitFor(() =>
      expect(HttpUtil.get).toHaveBeenCalledWith(PLUGIN, undefined, { silent: true }),
    );
    expect(screen.queryByText('Core updates')).toBeNull();
  });

  it('offers the update on the card of the core and the check at the top', async () => {
    mockGets(() => new Msg(true, '', state()));
    renderCores();
    expect(await screen.findByText('Core updates')).toBeTruthy();
    expect(screen.getByText('1 update(s)')).toBeTruthy();
    expect(screen.getByText(/Last checked:/)).toBeTruthy();

    const mieru = cardOf('mieru');
    expect(within(mieru).getByText(/Installed by Better Cores: v3\.39\.0/)).toBeTruthy();
    expect(within(mieru).getByText(/Latest release: v3\.40\.0/)).toBeTruthy();
    expect(within(mieru).getByRole('button', { name: 'Update to v3.40.0' })).toBeTruthy();

    const openflux = cardOf('OpenFlux');
    expect(within(openflux).getByText('Copy that came with the panel')).toBeTruthy();
    expect(within(openflux).getByRole('button', { name: 'Install v0.13.0' })).toBeTruthy();

    // a core the plugin does not manage has no such line
    expect(within(cardOf('NaiveProxy')).queryByText(/Latest release/)).toBeNull();
  });

  it('installs the release of one core when asked', async () => {
    mockGets(() => new Msg(true, '', state()));
    const post = HttpUtil.post as ReturnType<typeof vi.spyOn>;
    renderCores();
    const user = userEvent.setup();
    await user.click(
      await within(await waitForCard('mieru')).findByRole('button', { name: 'Update to v3.40.0' }),
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`${PLUGIN}/mieru/install`, undefined, { silent: true }),
    );
  });

  it('checks for updates on the button', async () => {
    mockGets(() => new Msg(true, '', state()));
    const post = HttpUtil.post as ReturnType<typeof vi.spyOn>;
    renderCores();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /check now/i }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`${PLUGIN}/check`, undefined, { silent: true }),
    );
  });

  it('says so while a core is being installed and shows a failure', async () => {
    mockGets(
      () =>
        new Msg(
          true,
          '',
          state({
            cores: [
              {
                id: 'mieru',
                name: 'mieru',
                latest: { tag: 'v3.40.0' },
                updateAvailable: false,
                installing: true,
                error: 'tunnel: sha256 mismatch',
              },
            ],
          }),
        ),
    );
    renderCores();
    const mieru = await waitForCard('mieru');
    expect(await within(mieru).findByText('tunnel: sha256 mismatch')).toBeTruthy();
    const button = within(mieru).getByRole('button', { name: /install v3\.40\.0/i });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it('does not take a look that never happened for a date', async () => {
    mockGets(
      () =>
        new Msg(
          true,
          '',
          state({ checkedAt: '0001-01-01T00:00:00Z', nextCheckAt: '0001-01-02T00:00:00Z' }),
        ),
    );
    renderCores();
    expect(await screen.findByText(/Not checked yet/)).toBeTruthy();
    expect(screen.queryByText(/next check/)).toBeNull();
  });
});

async function waitForCard(title: string) {
  return await waitFor(() => {
    const card = cardOf(title);
    expect(within(card).queryByText(/Latest release/)).not.toBeNull();
    return card;
  });
}
