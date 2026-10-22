// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import PluginPreviewModal from '@/pages/plugins/PluginPreviewModal';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';
import type { PluginEntry, PluginPreview } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({
  pluginsApi: { install: vi.fn() },
}));

function preview(overrides: Partial<PluginPreview> = {}): PluginPreview {
  return {
    token: 'tok-1',
    source: { owner: 'acme', repo: 'alerts' },
    commit: '0123456789abcdef0123456789abcdef01234567',
    manifest: {
      id: 'traffic-alerts',
      name: 'Traffic Alerts',
      version: '1.2.0',
      apiVersion: '1',
      type: 'process',
      scope: 'master',
      permissions: ['api:clients:read', 'network'],
    },
    dangerous: ['network'],
    installed: false,
    expiresAt: '2026-10-18T12:15:00Z',
    ...overrides,
  };
}

const installButton = () => screen.getByRole('button', { name: 'Install' }) as HTMLButtonElement;
const acceptBox = () => screen.getByRole('checkbox') as HTMLInputElement;

describe('PluginPreviewModal', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.install).mockReset();
  });

  it('keeps Install disabled until the permissions are accepted, then sends exactly the manifest permissions', async () => {
    vi.mocked(pluginsApi.install).mockResolvedValue(new Msg<PluginEntry>(true, '', null));
    const onInstalled = vi.fn();
    renderWithProviders(
      <PluginPreviewModal preview={preview()} onClose={vi.fn()} onInstalled={onInstalled} />,
    );

    expect(installButton().disabled).toBe(true);
    fireEvent.click(acceptBox());
    expect(installButton().disabled).toBe(false);
    fireEvent.click(installButton());

    await waitFor(() => expect(onInstalled).toHaveBeenCalledTimes(1));
    expect(pluginsApi.install).toHaveBeenCalledWith('tok-1', ['api:clients:read', 'network']);
  });

  it('does not report success when the backend rejects the install', async () => {
    vi.mocked(pluginsApi.install).mockResolvedValue(new Msg<PluginEntry>(false, 'nope', null));
    const onInstalled = vi.fn();
    renderWithProviders(
      <PluginPreviewModal preview={preview()} onClose={vi.fn()} onInstalled={onInstalled} />,
    );
    fireEvent.click(acceptBox());
    fireEvent.click(installButton());

    await waitFor(() => expect(pluginsApi.install).toHaveBeenCalled());
    expect(onInstalled).not.toHaveBeenCalled();
  });

  it('tells the admin when this host cannot enforce the missing network permission', () => {
    const open = {
      runner: 'process',
      level: 'partial',
      network: false,
      filesystem: false,
      user: true,
    } as const;
    const quiet = preview({
      manifest: { ...preview().manifest, permissions: ['api:clients:read'] },
    });
    const props = { onClose: vi.fn(), onInstalled: vi.fn() };
    const first = renderWithProviders(
      <PluginPreviewModal preview={quiet} isolation={open} {...props} />,
    );
    expect(screen.queryByText(/cannot block its network access/)).not.toBeNull();
    first.unmount();

    const enforced = renderWithProviders(
      <PluginPreviewModal preview={quiet} isolation={{ ...open, network: true }} {...props} />,
    );
    expect(screen.queryByText(/cannot block its network access/)).toBeNull();
    enforced.unmount();

    const withNetwork = renderWithProviders(
      <PluginPreviewModal preview={preview()} isolation={open} {...props} />,
    );
    expect(screen.queryByText(/cannot block its network access/)).toBeNull();
    withNetwork.unmount();

    const pageOnly = preview({ manifest: { ...quiet.manifest, type: 'ui' } });
    renderWithProviders(<PluginPreviewModal preview={pageOnly} isolation={open} {...props} />);
    expect(screen.queryByText(/cannot block its network access/)).toBeNull();
  });

  it('warns about dangerous permissions only when there are some', () => {
    const { unmount } = renderWithProviders(
      <PluginPreviewModal preview={preview()} onClose={vi.fn()} onInstalled={vi.fn()} />,
    );
    expect(screen.queryByText(/requests dangerous permissions/)).not.toBeNull();
    unmount();

    renderWithProviders(
      <PluginPreviewModal
        preview={preview({
          dangerous: [],
          manifest: { ...preview().manifest, permissions: ['api:clients:read'] },
        })}
        onClose={vi.fn()}
        onInstalled={vi.fn()}
      />,
    );
    expect(screen.queryByText(/requests dangerous permissions/)).toBeNull();
  });

  it('refuses to install a plugin that is already installed', () => {
    renderWithProviders(
      <PluginPreviewModal
        preview={preview({ installed: true })}
        onClose={vi.fn()}
        onInstalled={vi.fn()}
      />,
    );
    fireEvent.click(acceptBox());
    expect(screen.queryByText(/already installed/)).not.toBeNull();
    expect(installButton().disabled).toBe(true);
  });
});
