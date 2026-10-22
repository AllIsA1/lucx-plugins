// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';

import ProcessesNotice from '@/pages/plugins/ProcessesNotice';
import type { Isolation, PluginsState } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

function state(overrides: Partial<PluginsState> = {}): PluginsState {
  return {
    available: true,
    allowMasterManagement: false,
    processesAvailable: true,
    processesError: '',
    safeMode: false,
    error: '',
    archiveBase: '',
    customArchiveBase: '',
    defaultArchiveBase: '',
    plugins: [],
    ...overrides,
  };
}

const iso = (overrides: Partial<Isolation>): Isolation => ({
  runner: 'systemd',
  level: 'strong',
  network: true,
  filesystem: true,
  user: true,
  ...overrides,
});

describe('ProcessesNotice', () => {
  it('says nothing about isolation when the runner enforces everything', () => {
    renderWithProviders(<ProcessesNotice state={state({ processesIsolation: iso({}) })} />);
    expect(screen.queryByText(/systemd services/)).not.toBeNull();
    expect(screen.queryByText(/not enforced/)).toBeNull();
    expect(screen.queryByText(/not isolated/)).toBeNull();
  });

  it('describes child-process plugins and warns that the network permission is not enforced', () => {
    renderWithProviders(
      <ProcessesNotice
        state={state({
          processesIsolation: iso({
            runner: 'process',
            level: 'partial',
            network: false,
            filesystem: false,
          }),
        })}
      />,
    );
    expect(screen.queryByText(/child processes of the panel/)).not.toBeNull();
    expect(screen.queryByText(/systemd services/)).toBeNull();
    expect(screen.queryByText(/The network permission is not enforced/)).not.toBeNull();
  });

  it('calls a runner without a per-plugin user not isolated', () => {
    renderWithProviders(
      <ProcessesNotice
        state={state({
          processesIsolation: iso({
            runner: 'systemd-user',
            level: 'partial',
            network: false,
            user: false,
          }),
        })}
      />,
    );
    expect(screen.queryByText(/not isolated from the panel/)).not.toBeNull();
  });

  it('shows only the unavailable reason, no isolation advice, when processes cannot run', () => {
    renderWithProviders(
      <ProcessesNotice
        state={state({
          processesAvailable: false,
          processesError: 'needs root',
          processesIsolation: iso({ runner: 'none', level: 'none', network: false, user: false }),
        })}
      />,
    );
    expect(screen.queryByText(/needs root/)).not.toBeNull();
    expect(screen.queryByText(/not isolated/)).toBeNull();
  });
});
