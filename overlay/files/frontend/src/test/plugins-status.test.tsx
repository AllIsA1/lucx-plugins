// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';

import PluginStatus from '@/pages/plugins/PluginStatus';
import type { PluginEntry } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

function plugin(overrides: Partial<PluginEntry> = {}): PluginEntry {
  return {
    id: 'traffic-alerts',
    name: 'Traffic Alerts',
    version: '1.2.0',
    type: 'process',
    scope: 'master',
    source: { owner: 'acme', repo: 'alerts' },
    commit: '0123456789abcdef0123456789abcdef01234567',
    enabled: true,
    installedAt: '2026-10-18T12:00:00Z',
    permissions: [],
    runtime: { state: 'running', healthy: true, pid: 42, restarts: 0 },
    ...overrides,
  };
}

describe('PluginStatus', () => {
  it('shows a healthy running process as Running', () => {
    renderWithProviders(<PluginStatus plugin={plugin()} />);
    expect(screen.queryByText('Running')).not.toBeNull();
    expect(screen.queryByText(/not responding/)).toBeNull();
  });

  it('flags a running process that fails its health check', () => {
    renderWithProviders(
      <PluginStatus
        plugin={plugin({
          runtime: {
            state: 'running',
            healthy: false,
            healthError: 'refused',
            pid: 42,
            restarts: 1,
          },
        })}
      />,
    );
    expect(screen.queryByText(/Running · not responding/)).not.toBeNull();
  });

  it('explains why the panel switched a plugin off', () => {
    renderWithProviders(
      <PluginStatus
        plugin={plugin({
          enabled: false,
          disabledReason: 'the process keeps failing: start-limit-hit',
          runtime: { state: 'stopped', healthy: false, pid: 0, restarts: 5 },
        })}
      />,
    );
    expect(screen.queryByText('Stopped')).not.toBeNull();
    expect(
      screen.queryByText(/Disabled: the process keeps failing: start-limit-hit/),
    ).not.toBeNull();
  });

  it('does not invent a process for an interface-only plugin', () => {
    renderWithProviders(<PluginStatus plugin={plugin({ type: 'ui', runtime: null })} />);
    expect(screen.queryByText('Interface only')).not.toBeNull();
    expect(screen.queryByText('Stopped')).toBeNull();
  });

  it('treats a missing runtime as stopped', () => {
    renderWithProviders(<PluginStatus plugin={plugin({ enabled: false, runtime: undefined })} />);
    expect(screen.queryByText('Stopped')).not.toBeNull();
  });

  it('summarises event delivery and shows the last problem', () => {
    renderWithProviders(
      <PluginStatus
        plugin={plugin({
          runtime: {
            state: 'running',
            healthy: true,
            pid: 42,
            restarts: 0,
            eventsSent: 5,
            eventsFailed: 1,
            eventsDropped: 2,
            lastError: 'pipeline xray.config: plugin answered HTTP 500',
          },
        })}
      />,
    );
    expect(screen.queryByText('Events: 5 delivered, 1 failed, 2 dropped')).not.toBeNull();
    expect(
      screen.queryByText(/Last problem: pipeline xray.config: plugin answered HTTP 500/),
    ).not.toBeNull();
  });

  it('stays quiet when nothing was delivered and nothing failed', () => {
    renderWithProviders(<PluginStatus plugin={plugin()} />);
    expect(screen.queryByText(/Events:/)).toBeNull();
    expect(screen.queryByText(/Last problem/)).toBeNull();
  });
});
