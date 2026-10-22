import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { afterEach, expect, test, vi } from 'vitest';

import { pluginsApi } from '@/api/plugins';
import AppSidebar from '@/layouts/AppSidebar';
import { Msg } from '@/utils';
import type { PluginUi } from '@/schemas/plugins';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/queries/useAllSettings', () => ({
  useAllSettings: () => ({ allSetting: {} }),
}));

afterEach(() => {
  localStorage.clear();
});

// rc-menu registers its items in a microtask after render; settle it inside act().
async function renderSidebar() {
  const view = renderWithProviders(
    <MemoryRouter>
      <AppSidebar />
    </MemoryRouter>,
  );
  await act(async () => {});
  return view;
}

test('keeps the sidebar expanded after pinning it from the header and restores the choice', async () => {
  const first = await renderSidebar();
  const sidebar = first.container.querySelector('.ant-layout-sider');
  const sidebarRoot = first.container.querySelector('.ant-sidebar');

  expect(sidebar?.classList.contains('ant-layout-sider-collapsed')).toBe(true);

  fireEvent.mouseEnter(sidebarRoot!);

  const pinButton = screen.getByRole('button', { name: 'Pin sidebar' });
  expect(pinButton.closest('.brand-actions')).not.toBeNull();

  fireEvent.click(pinButton);
  fireEvent.mouseLeave(sidebarRoot!);

  expect(sidebar?.classList.contains('ant-layout-sider-collapsed')).toBe(false);
  expect(sidebarRoot?.getAttribute('style')).toContain('--sider-rail: 220px');
  expect(localStorage.getItem('sidebar-pinned')).toBe('true');

  first.unmount();

  const second = await renderSidebar();
  const restoredSidebar = second.container.querySelector('.ant-layout-sider');
  const restoredSidebarRoot = second.container.querySelector('.ant-sidebar');

  expect(restoredSidebar?.classList.contains('ant-layout-sider-collapsed')).toBe(false);
  expect(restoredSidebarRoot?.getAttribute('style')).toContain('--sider-rail: 220px');
  expect(screen.getByRole('button', { name: 'Pin sidebar' })).not.toBeNull();
});

test('returns to the compact rail after unpinning', async () => {
  const view = await renderSidebar();
  const sidebar = view.container.querySelector('.ant-layout-sider');
  const sidebarRoot = view.container.querySelector('.ant-sidebar');

  fireEvent.mouseEnter(sidebarRoot!);
  fireEvent.click(screen.getByRole('button', { name: 'Pin sidebar' }));
  fireEvent.click(screen.getByRole('button', { name: 'Pin sidebar' }));
  fireEvent.mouseLeave(sidebarRoot!);

  expect(sidebar?.classList.contains('ant-layout-sider-collapsed')).toBe(true);
  expect(sidebarRoot?.getAttribute('style')).toContain('--sider-rail: 72px');
  expect(localStorage.getItem('sidebar-pinned')).toBe('false');
});

test('labels the palette shortcut with the modifier the platform actually uses', async () => {
  const view = await renderSidebar();
  const chip = view.container.querySelector('.sidebar-command-kbd');
  expect(chip?.textContent).toBe('CtrlK');
});

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>;
}

async function renderSidebarAt(url: string, ui: PluginUi | null) {
  vi.spyOn(pluginsApi, 'ui').mockResolvedValue(
    ui ? new Msg<PluginUi>(true, '', ui) : new Msg<PluginUi>(false, 'down', null),
  );
  const view = renderWithProviders(
    <MemoryRouter initialEntries={[url]}>
      <AppSidebar />
      <Where />
    </MemoryRouter>,
  );
  await act(async () => {});
  await waitFor(() => expect(pluginsApi.ui).toHaveBeenCalled());
  await act(async () => {});
  return view;
}

const pluginUi: PluginUi = {
  menu: [
    {
      pluginId: 'page-a',
      pluginName: 'Page A',
      title: 'Alerts page',
      icon: 'bell',
      path: '/ui/index.html',
    },
    {
      pluginId: 'page-a',
      pluginName: 'Page A',
      title: 'Odd icon',
      icon: 'no-such-icon',
      path: '/ui/two.html',
    },
  ],
  replaces: {},
};

test('lists what enabled plugins add to the menu right after Plugins, and opens their page', async () => {
  const view = await renderSidebarAt('/', pluginUi);
  await screen.findByText('Alerts page');
  const labels = Array.from(view.container.querySelectorAll('.ant-menu-title-content')).map(
    (el) => el.textContent,
  );
  const at = labels.indexOf('Plugins');
  expect(labels.slice(at, at + 3)).toEqual(['Plugins', 'Alerts page', 'Odd icon']);

  const iconOf = (label: string) =>
    screen.getByText(label).closest('li')?.querySelector('.anticon')?.getAttribute('aria-label');
  expect(iconOf('Alerts page')).toBe('bell');
  expect(iconOf('Odd icon')).toBe('appstore');

  fireEvent.click(screen.getByText('Alerts page'));
  expect(screen.getByTestId('where').textContent).toBe('/plugins/ui/page-a/ui/index.html');
});

test('marks the plugin entry of the page being shown as the current one', async () => {
  const view = await renderSidebarAt('/plugins/ui/page-a/ui/two.html', pluginUi);
  await screen.findByText('Odd icon');
  const selected = Array.from(view.container.querySelectorAll('.ant-menu-item-selected')).map(
    (el) => el.textContent,
  );
  expect(selected).toEqual(['Odd icon']);
});

test('shows no plugin entries when the list cannot be read', async () => {
  const view = await renderSidebarAt('/', null);
  expect(view.container.textContent).not.toContain('Alerts page');
  expect(view.container.textContent).toContain('Plugins');
});
