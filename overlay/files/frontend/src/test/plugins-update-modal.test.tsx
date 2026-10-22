// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import UpdateModal from '@/pages/plugins/UpdateModal';
import RollbackModal from '@/pages/plugins/RollbackModal';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type { PluginEntry, UpdatePreview } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: { updatePreview: vi.fn(), update: vi.fn(), rollback: vi.fn() },
}));

const installed: PluginEntry = {
  id: 'traffic-alerts',
  name: 'Traffic Alerts',
  version: '1.2.0',
  type: 'process',
  scope: 'master',
  source: { owner: 'acme', repo: 'alerts' },
  commit: '0123456789abcdef0123456789abcdef01234567',
  enabled: true,
  installedAt: '2026-10-18T12:00:00Z',
  permissions: ['api:clients:read'],
  previous: { version: '1.1.0', commit: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' },
};

function preview(overrides: Partial<UpdatePreview> = {}): UpdatePreview {
  return {
    token: 'tok-1',
    commit: '2222222222222222222222222222222222222222',
    manifest: {
      id: 'traffic-alerts',
      name: 'Traffic Alerts',
      version: '1.3.0',
      apiVersion: '1',
      type: 'process',
      scope: 'master',
      permissions: ['api:clients:read', 'network'],
    },
    current: { version: '1.2.0', commit: installed.commit },
    upToDate: false,
    diff: {
      added: ['network'],
      removed: [],
      kept: ['api:clients:read'],
      dangerousAdded: ['network'],
    },
    typeChanged: false,
    scopeChanged: false,
    needsConfirmation: true,
    ...overrides,
  };
}

const serve = (p: UpdatePreview) =>
  vi.mocked(pluginsApi.updatePreview).mockResolvedValue(new Msg<UpdatePreview>(true, '', p));
const applyButton = () => screen.getByRole('button', { name: 'Update' }) as HTMLButtonElement;

describe('UpdateModal', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.updatePreview).mockReset();
    vi.mocked(pluginsApi.update).mockReset();
    vi.mocked(pluginsApi.rollback).mockReset();
  });

  it('shows what changes and keeps Update disabled until grown permissions are accepted', async () => {
    serve(preview());
    vi.mocked(pluginsApi.update).mockResolvedValue(new Msg<PluginEntry>(true, '', null));
    const onUpdated = vi.fn();
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={onUpdated} />);

    await screen.findByText(/1\.2\.0 → 1\.3\.0/);
    expect(screen.queryByText('network')).not.toBeNull();
    expect(applyButton().disabled).toBe(true);
    fireEvent.click(screen.getByRole('checkbox'));
    expect(applyButton().disabled).toBe(false);
    fireEvent.click(applyButton());

    await waitFor(() => expect(onUpdated).toHaveBeenCalledTimes(1));
    expect(pluginsApi.update).toHaveBeenCalledWith('traffic-alerts', 'tok-1', [
      'api:clients:read',
      'network',
    ]);
  });

  it('updates with one click when no permission is added', async () => {
    serve(
      preview({
        needsConfirmation: false,
        manifest: { ...preview().manifest, permissions: ['api:clients:read'] },
        diff: { added: [], removed: [], kept: ['api:clients:read'], dangerousAdded: [] },
      }),
    );
    vi.mocked(pluginsApi.update).mockResolvedValue(new Msg<PluginEntry>(true, '', null));
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await screen.findByText(/1\.2\.0 → 1\.3\.0/);
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(applyButton().disabled).toBe(false);
    fireEvent.click(applyButton());
    await waitFor(() =>
      expect(pluginsApi.update).toHaveBeenCalledWith('traffic-alerts', 'tok-1', [
        'api:clients:read',
      ]),
    );
  });

  it('asks for confirmation when only the type or scope changes', async () => {
    serve(
      preview({
        needsConfirmation: true,
        scopeChanged: true,
        manifest: { ...preview().manifest, scope: 'both', permissions: ['api:clients:read'] },
        diff: { added: [], removed: [], kept: ['api:clients:read'], dangerousAdded: [] },
      }),
    );
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await screen.findByText(/The plugin scope changes from master to both/);
    expect(applyButton().disabled).toBe(true);
  });

  it('says so when there is nothing newer and offers no Update', async () => {
    serve(preview({ upToDate: true, token: '' }));
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await screen.findByText(/is up to date/);
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
  });

  it('looks at the ref the operator types', async () => {
    serve(preview());
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await screen.findByText(/1\.2\.0 → 1\.3\.0/);
    expect(pluginsApi.updatePreview).toHaveBeenLastCalledWith('traffic-alerts', undefined);
    fireEvent.change(screen.getByPlaceholderText('branch, tag or commit (optional)'), {
      target: { value: 'v2.0.0' },
    });
    fireEvent.click(screen.getByText('Check'));
    await waitFor(() =>
      expect(pluginsApi.updatePreview).toHaveBeenLastCalledWith('traffic-alerts', 'v2.0.0'),
    );
  });

  it('does not report success when the update fails', async () => {
    serve(
      preview({
        needsConfirmation: false,
        diff: { added: [], removed: [], kept: [], dangerousAdded: [] },
      }),
    );
    vi.mocked(pluginsApi.update).mockResolvedValue(
      new Msg<PluginEntry>(false, 'rolled back', null),
    );
    const onUpdated = vi.fn();
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={onUpdated} />);
    await screen.findByText(/1\.2\.0 → 1\.3\.0/);
    fireEvent.click(applyButton());
    await waitFor(() => expect(pluginsApi.update).toHaveBeenCalled());
    expect(onUpdated).not.toHaveBeenCalled();
  });

  it('shows nothing to apply when the check fails', async () => {
    vi.mocked(pluginsApi.updatePreview).mockResolvedValue(
      new Msg<UpdatePreview>(false, 'no network', null),
    );
    renderWithProviders(<UpdateModal plugin={installed} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await screen.findByText('no network');
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
  });
});

describe('RollbackModal', () => {
  it('names the version it goes back to and calls rollback once confirmed', async () => {
    vi.mocked(pluginsApi.rollback).mockResolvedValue(new Msg<PluginEntry>(true, '', null));
    const onDone = vi.fn();
    renderWithProviders(<RollbackModal plugin={installed} onClose={vi.fn()} onDone={onDone} />);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).queryByText(/1\.1\.0/)).not.toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Roll back' }));
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(pluginsApi.rollback).toHaveBeenCalledWith('traffic-alerts');
  });

  it('does not report success when the rollback fails', async () => {
    vi.mocked(pluginsApi.rollback).mockResolvedValue(new Msg<PluginEntry>(false, 'gone', null));
    const onDone = vi.fn();
    renderWithProviders(<RollbackModal plugin={installed} onClose={vi.fn()} onDone={onDone} />);
    fireEvent.click(screen.getByRole('button', { name: 'Roll back' }));
    await waitFor(() => expect(pluginsApi.rollback).toHaveBeenCalled());
    expect(onDone).not.toHaveBeenCalled();
  });
});
