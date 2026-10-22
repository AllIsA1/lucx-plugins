// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import CatalogCard from '@/pages/plugins/CatalogCard';
import PluginPreviewModal from '@/pages/plugins/PluginPreviewModal';
import UpdateModal from '@/pages/plugins/UpdateModal';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type {
  CatalogPlugin,
  CatalogView,
  PluginEntry,
  PluginPreview,
  UpdatePreview,
} from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: {
    catalog: vi.fn(),
    catalogRefresh: vi.fn(),
    catalogPreview: vi.fn(),
    catalogUpdatePreview: vi.fn(),
    catalogSetBase: vi.fn(),
    updatePreview: vi.fn(),
    update: vi.fn(),
    install: vi.fn(),
  },
}));

const COMMIT = '0123456789abcdef0123456789abcdef01234567';

function entry(id: string, overrides: Partial<CatalogPlugin> = {}): CatalogPlugin {
  return {
    id,
    name: `Plugin ${id}`,
    summary: `Summary of ${id}`,
    repo: `https://github.com/acme/${id}`,
    commit: COMMIT,
    version: '1.2.0',
    manifestSha256: 'ab'.repeat(32),
    type: 'process',
    scope: 'both',
    permissions: ['api:clients:read', 'network'],
    license: 'MIT',
    author: 'acme',
    verified: { by: 'LucX', at: '2026-10-10', notes: 'reviewed' },
    status: 'available',
    ...overrides,
  };
}

function view(plugins: CatalogPlugin[], overrides: Partial<CatalogView> = {}): CatalogView {
  return {
    state: 'ok',
    base: 'https://raw.example/index',
    customBase: '',
    fetchedAt: '2026-10-19T12:00:00Z',
    generatedAt: '2026-10-19T11:00:00Z',
    sequence: 7,
    plugins,
    ...overrides,
  };
}

const serve = (v: CatalogView) =>
  vi.mocked(pluginsApi.catalog).mockResolvedValue(new Msg<CatalogView>(true, '', v));

const installedEntry = (id: string): PluginEntry => ({
  id,
  name: `Plugin ${id}`,
  version: '1.0.0',
  type: 'process',
  scope: 'both',
  source: { owner: 'acme', repo: id },
  commit: 'f'.repeat(40),
  enabled: true,
  installedAt: '2026-10-18T12:00:00Z',
  permissions: [],
});

function renderCard(props: Partial<React.ComponentProps<typeof CatalogCard>> = {}) {
  const onInstall = vi.fn();
  const onUpdate = vi.fn();
  renderWithProviders(
    <CatalogCard available installed={[]} onInstall={onInstall} onUpdate={onUpdate} {...props} />,
  );
  return { onInstall, onUpdate };
}

describe('CatalogCard', () => {
  beforeEach(() => {
    for (const fn of Object.values(pluginsApi)) vi.mocked(fn).mockReset();
  });

  it('lists the plugins with what the catalog vouches for', async () => {
    serve(view([entry('block-ads'), entry('no-review', { verified: null })]));
    renderCard();
    await screen.findByText('Plugin block-ads');
    expect(screen.queryByText('Summary of block-ads')).not.toBeNull();
    expect(screen.queryAllByText('Verified')).toHaveLength(1);
    expect(screen.queryAllByText('network').length).toBeGreaterThan(0);
  });

  it('previews a catalog plugin through the catalog and hands it to the install dialog', async () => {
    serve(view([entry('block-ads')]));
    const preview = { token: 'tok-1' } as unknown as PluginPreview;
    vi.mocked(pluginsApi.catalogPreview).mockResolvedValue(
      new Msg<PluginPreview>(true, '', preview),
    );
    const { onInstall } = renderCard();
    fireEvent.click(await screen.findByRole('button', { name: 'Install' }));
    await waitFor(() => expect(onInstall).toHaveBeenCalledWith(preview));
    expect(pluginsApi.catalogPreview).toHaveBeenCalledWith('block-ads');
  });

  it('does not open the install dialog when the preview is refused', async () => {
    serve(view([entry('block-ads')]));
    vi.mocked(pluginsApi.catalogPreview).mockResolvedValue(
      new Msg<PluginPreview>(false, 'does not match', null),
    );
    const { onInstall } = renderCard();
    fireEvent.click(await screen.findByRole('button', { name: 'Install' }));
    await waitFor(() => expect(pluginsApi.catalogPreview).toHaveBeenCalled());
    expect(onInstall).not.toHaveBeenCalled();
  });

  it('offers an update for an installed older commit and nothing for the current or a clashing one', async () => {
    serve(
      view([
        entry('old-one', {
          status: 'update-available',
          installed: { version: '1.0.0', commit: 'f'.repeat(40) },
        }),
        entry('current', { status: 'installed' }),
        entry('clash', { status: 'id-conflict' }),
      ]),
    );
    const { onUpdate } = renderCard({ installed: [installedEntry('old-one')] });
    fireEvent.click(await screen.findByRole('button', { name: 'Update' }));
    expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ id: 'old-one' }));
    expect(screen.queryAllByRole('button', { name: 'Install' })).toHaveLength(0);
    expect(screen.queryByText('Installed')).not.toBeNull();
    expect(screen.queryByText(/different plugin with this id/)).not.toBeNull();
  });

  it('cannot install while plugins are unavailable', async () => {
    serve(view([entry('block-ads')]));
    renderCard({ available: false });
    expect(
      (await screen.findByRole('button', { name: 'Install' })) as HTMLButtonElement,
    ).toHaveProperty('disabled', true);
  });

  it('filters by what the operator types', async () => {
    serve(view([entry('block-ads'), entry('traffic-alerts')]));
    renderCard();
    await screen.findByText('Plugin traffic-alerts');
    fireEvent.change(screen.getByPlaceholderText('Search the catalog'), {
      target: { value: 'alerts' },
    });
    expect(screen.queryByText('Plugin block-ads')).toBeNull();
    expect(screen.queryByText('Plugin traffic-alerts')).not.toBeNull();
  });

  it.each([
    ['unconfigured', view([], { state: 'unconfigured' }), /no catalog signing key/],
    ['not published', view([], { state: 'not-published' }), /has not been published yet/],
    ['error', view([], { state: 'error', error: 'HTTP 500' }), /could not be loaded: HTTP 500/],
    [
      'stale',
      view([entry('block-ads')], { state: 'stale', error: 'HTTP 500' }),
      /last good catalog: HTTP 500/,
    ],
  ])('says what is wrong when the catalog is %s', async (_name, v, text) => {
    serve(v);
    renderCard();
    await screen.findByText(text);
  });

  it('asks the panel to fetch the catalog again on request', async () => {
    serve(view([entry('block-ads')]));
    vi.mocked(pluginsApi.catalogRefresh).mockResolvedValue(
      new Msg<CatalogView>(true, '', view([entry('fresh-one')])),
    );
    renderCard();
    await screen.findByText('Plugin block-ads');
    fireEvent.click(screen.getByRole('button', { name: /Refresh/ }));
    await waitFor(() => expect(pluginsApi.catalogRefresh).toHaveBeenCalledTimes(1));
    await screen.findByText('Plugin fresh-one');
  });

  it('saves the catalog mirror the operator types', async () => {
    serve(view([]));
    vi.mocked(pluginsApi.catalogSetBase).mockResolvedValue(new Msg<null>(true, '', null));
    renderCard();
    const input = await screen.findByPlaceholderText('https://raw.example/index');
    fireEvent.change(input, { target: { value: 'https://mirror.example/index' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(pluginsApi.catalogSetBase).toHaveBeenCalledWith('https://mirror.example/index'),
    );
  });
});

const preview = (catalog: PluginPreview['catalog']): PluginPreview => ({
  token: 'tok-1',
  source: { owner: 'acme', repo: 'alerts' },
  commit: COMMIT,
  manifest: {
    id: 'traffic-alerts',
    name: 'Traffic Alerts',
    version: '1.2.0',
    apiVersion: '1',
    type: 'process',
    scope: 'master',
    permissions: ['api:clients:read'],
  },
  dangerous: [],
  installed: false,
  expiresAt: '2026-10-18T12:15:00Z',
  catalog,
});

describe('PluginPreviewModal catalog notice', () => {
  it('warns that nobody reviewed a commit the catalog does not list', () => {
    renderWithProviders(
      <PluginPreviewModal preview={preview('unlisted')} onClose={vi.fn()} onInstalled={vi.fn()} />,
    );
    expect(screen.queryByText(/not in the signed catalog/)).not.toBeNull();
  });

  it('says so when the catalog vouches for the commit', () => {
    renderWithProviders(
      <PluginPreviewModal preview={preview('listed')} onClose={vi.fn()} onInstalled={vi.fn()} />,
    );
    expect(screen.queryByText(/Listed in the signed catalog/)).not.toBeNull();
    expect(screen.queryByText(/not in the signed catalog/)).toBeNull();
  });

  it('stays quiet when there is no catalog to compare with', () => {
    renderWithProviders(
      <PluginPreviewModal
        preview={preview('unavailable')}
        onClose={vi.fn()}
        onInstalled={vi.fn()}
      />,
    );
    expect(screen.queryByText(/signed catalog/)).toBeNull();
  });
});

describe('UpdateModal from the catalog', () => {
  it("checks the catalog's commit instead of asking for a ref", async () => {
    const up = {
      token: 'tok-2',
      commit: '2'.repeat(40),
      manifest: { ...preview('listed').manifest, version: '1.3.0' },
      current: { version: '1.2.0', commit: COMMIT },
      upToDate: false,
      diff: { added: [], removed: [], kept: ['api:clients:read'], dangerousAdded: [] },
      typeChanged: false,
      scopeChanged: false,
      needsConfirmation: false,
    } satisfies UpdatePreview;
    vi.mocked(pluginsApi.catalogUpdatePreview).mockResolvedValue(
      new Msg<UpdatePreview>(true, '', up),
    );
    renderWithProviders(
      <UpdateModal
        plugin={installedEntry('traffic-alerts')}
        fromCatalog
        onClose={vi.fn()}
        onUpdated={vi.fn()}
      />,
    );
    await screen.findByText(/1\.2\.0 → 1\.3\.0/);
    expect(pluginsApi.catalogUpdatePreview).toHaveBeenCalledWith('traffic-alerts');
    expect(pluginsApi.updatePreview).not.toHaveBeenCalled();
    expect(screen.queryByPlaceholderText('branch, tag or commit (optional)')).toBeNull();
    expect(
      within(screen.getByRole('dialog')).queryByRole('button', { name: 'Update' }),
    ).not.toBeNull();
  });
});
