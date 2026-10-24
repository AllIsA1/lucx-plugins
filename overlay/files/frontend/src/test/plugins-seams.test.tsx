// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';

import PermissionList from '@/pages/plugins/PermissionList';
import PluginPreviewModal from '@/pages/plugins/PluginPreviewModal';
import PluginStatus from '@/pages/plugins/PluginStatus';
import SeamList from '@/pages/plugins/SeamList';
import type { PluginEntry, PluginPreview } from '@/api/plugins';

import { renderWithProviders } from './test-utils';

const seams = [
  { engine: 'sub', mode: 'after', route: '/links/:subid', methods: ['GET'] },
  { engine: 'api', mode: 'before', route: '/server/status' },
];

function plugin(overrides: Partial<PluginEntry> = {}): PluginEntry {
  return {
    id: 'seam-demo',
    name: 'Seam Demo',
    version: '1.0.0',
    type: 'process',
    scope: 'both',
    source: { owner: 'acme', repo: 'seam-demo' },
    commit: '0123456789abcdef0123456789abcdef01234567',
    enabled: true,
    installedAt: '2026-10-18T12:00:00Z',
    permissions: ['seam:sub', 'seam:api:server'],
    seams,
    runtime: { state: 'running', healthy: true, pid: 42, restarts: 0 },
    ...overrides,
  };
}

describe('SeamList', () => {
  it('names each place a plugin takes part in a request', () => {
    renderWithProviders(<SeamList seams={seams} />);
    expect(screen.queryByText('Subscription · after · /links/:subid')).not.toBeNull();
    expect(screen.queryByText('API · before · /server/status')).not.toBeNull();
  });

  it('says in a tooltip what the mode does and for which methods', async () => {
    renderWithProviders(<SeamList seams={seams} />);
    fireEvent.mouseOver(screen.getByText('Subscription · after · /links/:subid'));
    expect(await screen.findByText(/Changes the answer the panel gave\. \(GET\)/)).not.toBeNull();
    fireEvent.mouseOver(screen.getByText('API · before · /server/status'));
    expect(await screen.findByText(/before the panel handles it.*\(all methods\)/)).not.toBeNull();
  });

  it('shows nothing for a plugin without seams', () => {
    const { container } = renderWithProviders(<SeamList seams={[]} />);
    expect(container.firstChild).toBeNull();
  });
});

describe('seams on the Plugins page', () => {
  it('shows the functions a running plugin takes part in, with its counters', () => {
    renderWithProviders(
      <PluginStatus
        plugin={plugin({
          runtime: {
            state: 'running',
            healthy: true,
            pid: 42,
            restarts: 0,
            seamCalls: 12,
            seamErrors: 3,
            seamSkipped: 4,
          },
        })}
      />,
    );
    expect(screen.queryByText('Subscription · after · /links/:subid')).not.toBeNull();
    expect(screen.queryByText(/12 calls, 3 failed, 4 skipped/)).not.toBeNull();
  });

  it('does not list a seam the plugin has no permission for', () => {
    renderWithProviders(<PluginStatus plugin={plugin({ permissions: ['seam:sub'] })} />);
    expect(screen.queryByText('Subscription · after · /links/:subid')).not.toBeNull();
    expect(screen.queryByText('API · before · /server/status')).toBeNull();
  });

  it('keeps the counter line away when nothing was called', () => {
    renderWithProviders(<PluginStatus plugin={plugin()} />);
    expect(screen.queryByText(/calls,/)).toBeNull();
  });

  it('marks a seam permission as dangerous', () => {
    renderWithProviders(<PermissionList permissions={['seam:sub']} />);
    expect(screen.queryByText(/Can take part in how the panel answers requests/)).not.toBeNull();
    expect(screen.getByText('seam:sub').closest('.ant-tag')?.className).toMatch(/red/);
  });

  it('shows the operator the seams before installing', () => {
    const preview: PluginPreview = {
      token: 'tok',
      source: { owner: 'acme', repo: 'seam-demo' },
      commit: '0123456789abcdef0123456789abcdef01234567',
      manifest: {
        id: 'seam-demo',
        name: 'Seam Demo',
        version: '1.0.0',
        apiVersion: '1',
        type: 'process',
        scope: 'both',
        permissions: ['seam:sub', 'seam:api:server'],
        seams,
      },
      dangerous: ['seam:sub', 'seam:api:server'],
      installed: false,
      expiresAt: '2030-01-01T00:00:00Z',
    };
    renderWithProviders(
      <PluginPreviewModal preview={preview} onClose={vi.fn()} onInstalled={vi.fn()} />,
    );
    expect(screen.queryByText('Subscription · after · /links/:subid')).not.toBeNull();
    expect(screen.queryByText('API · before · /server/status')).not.toBeNull();
  });
});
