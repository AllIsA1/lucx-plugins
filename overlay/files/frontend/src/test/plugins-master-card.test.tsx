// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import MasterManagementCard from '@/pages/plugins/MasterManagementCard';
import { pluginsApi } from '@/api/plugins';
import { Msg } from '@/utils';

import { renderWithProviders } from './test-utils';

vi.mock('@/api/plugins', () => ({ pluginsApi: { setMasterManagement: vi.fn() } }));

const toggle = () => screen.getByRole('switch') as HTMLButtonElement;

describe('MasterManagementCard', () => {
  beforeEach(() => {
    vi.mocked(pluginsApi.setMasterManagement).mockReset();
  });

  it('starts off and says nothing alarming while the master is locked out', () => {
    renderWithProviders(<MasterManagementCard allowed={false} onChanged={vi.fn()} />);
    expect(toggle().getAttribute('aria-checked')).toBe('false');
    expect(screen.queryByText(/can now run third-party code/)).toBeNull();
  });

  it('turning it on asks the node to allow the master and then refreshes', async () => {
    vi.mocked(pluginsApi.setMasterManagement).mockResolvedValue(new Msg<null>(true, '', null));
    const onChanged = vi.fn();
    renderWithProviders(<MasterManagementCard allowed={false} onChanged={onChanged} />);
    fireEvent.click(toggle());
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(pluginsApi.setMasterManagement).toHaveBeenCalledWith(true);
  });

  it('warns about third-party code once the master is allowed, and can revoke', async () => {
    vi.mocked(pluginsApi.setMasterManagement).mockResolvedValue(new Msg<null>(true, '', null));
    renderWithProviders(<MasterManagementCard allowed onChanged={vi.fn()} />);
    expect(screen.queryByText(/can now run third-party code/)).not.toBeNull();
    fireEvent.click(toggle());
    await waitFor(() => expect(pluginsApi.setMasterManagement).toHaveBeenCalledWith(false));
  });

  it('does not refresh when the node refuses the change', async () => {
    vi.mocked(pluginsApi.setMasterManagement).mockResolvedValue(new Msg<null>(false, 'no', null));
    const onChanged = vi.fn();
    renderWithProviders(<MasterManagementCard allowed={false} onChanged={onChanged} />);
    fireEvent.click(toggle());
    await waitFor(() => expect(pluginsApi.setMasterManagement).toHaveBeenCalled());
    expect(onChanged).not.toHaveBeenCalled();
  });
});
