// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import { setupHttp } from '@/api/http-init';
import { pluginsApi } from '@/api/plugins';
import i18n from 'i18next';
import PluginFrame from '@/pages/plugins/PluginFrame';
import PluginFramePage from '@/pages/plugins/PluginFramePage';
import ReplaceablePage from '@/pages/plugins/ReplaceablePage';
import { Msg } from '@/utils';
import type { PluginFrameToken, PluginUi } from '@/schemas/plugins';

import { renderWithProviders } from './test-utils';

vi.mock('@/layouts/AppSidebar', () => ({ default: () => <div data-testid="sidebar" /> }));
vi.mock('@/api/plugins', () => ({ pluginsApi: { ui: vi.fn(), frameToken: vi.fn() } }));

const ui: PluginUi = {
  menu: [
    {
      pluginId: 'page-a',
      pluginName: 'Page A',
      title: 'Page A',
      icon: 'bell',
      path: '/ui/index.html',
    },
  ],
  replaces: {
    '/clients': { pluginId: 'page-a', pluginName: 'Page A', path: '/ui/replace/clients/' },
  },
};

function serveUi(value: PluginUi | null) {
  vi.mocked(pluginsApi.ui).mockResolvedValue(
    value ? new Msg<PluginUi>(true, '', value) : new Msg<PluginUi>(false, 'boom', null),
  );
}

const frame = () => document.querySelector('iframe') as HTMLIFrameElement | null;

function sendFromFrame(data: unknown, source: unknown) {
  window.dispatchEvent(new MessageEvent('message', { data, source: source as MessageEventSource }));
}

beforeEach(() => {
  vi.mocked(pluginsApi.ui).mockReset();
  vi.mocked(pluginsApi.frameToken).mockReset();
  vi.mocked(pluginsApi.frameToken).mockResolvedValue(
    new Msg<PluginFrameToken>(true, '', { token: 'lxf.tok', expiresAt: '2026-10-18T14:00:00Z' }),
  );
});

const frameShown = () => waitFor(() => expect(frame()).not.toBeNull());

afterEach(() => {
  cleanup();
  delete window.X_UI_BASE_PATH;
  setupHttp();
});

describe('PluginFrame', () => {
  it('puts the page in a sandbox without the panel origin, at its token address', async () => {
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    const el = frame()!;
    expect(el.getAttribute('sandbox')).toBe('allow-scripts');
    expect(el.getAttribute('src')).toBe('/panel/plugin-frame/lxf.tok/ui/index.html');
    expect(el.getAttribute('referrerpolicy')).toBe('no-referrer');
  });

  it('keeps the panel base path in the address', async () => {
    window.X_UI_BASE_PATH = '/secret';
    setupHttp();
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    expect(frame()!.getAttribute('src')).toBe('/secret/panel/plugin-frame/lxf.tok/ui/index.html');
  });

  it('asks for the token of the plugin it shows, and shows no page without one', async () => {
    vi.mocked(pluginsApi.frameToken).mockResolvedValue(
      new Msg<PluginFrameToken>(false, 'off', null),
    );
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await screen.findByText('This plugin page is not available');
    expect(frame()).toBeNull();
    expect(pluginsApi.frameToken).toHaveBeenCalledWith('page-a');
  });

  it('does not reload the page when the panel refocuses', async () => {
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    const el = frame();
    act(() => {
      window.dispatchEvent(new Event('focus'));
      window.dispatchEvent(new Event('visibilitychange'));
    });
    await act(async () => {});
    expect(pluginsApi.frameToken).toHaveBeenCalledTimes(1);
    expect(frame()).toBe(el);
  });

  it('greets a page that says ready, and only that page', async () => {
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    const target = frame()!.contentWindow!;
    const post = vi.spyOn(target, 'postMessage');

    sendFromFrame({ lucx: 1, type: 'ready' }, window);
    await act(async () => {});
    expect(post).not.toHaveBeenCalled();

    sendFromFrame({ lucx: 1, type: 'ready' }, target);
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post.mock.calls[0][0]).toMatchObject({
      lucx: 1,
      type: 'init',
      pluginId: 'page-a',
      lang: i18n.language,
      theme: { dark: expect.any(Boolean), ultra: expect.any(Boolean), palette: expect.any(String) },
    });
    expect(post.mock.calls[0][1]).toBe('*');
  });

  it('tells the page when the language changes', async () => {
    renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    const post = vi.spyOn(frame()!.contentWindow!, 'postMessage');
    const before = i18n.language;
    await act(async () => {
      await i18n.changeLanguage('ru-RU');
    });
    try {
      await waitFor(() => expect(post).toHaveBeenCalled());
      expect(post.mock.calls.at(-1)![0]).toMatchObject({ type: 'context', lang: 'ru-RU' });
    } finally {
      await act(async () => {
        await i18n.changeLanguage(before);
      });
    }
  });

  it('lets only the plugin now shown answer after switching plugins', async () => {
    function Switcher() {
      const [id, setId] = useState('page-a');
      return (
        <>
          <button onClick={() => setId('page-b')}>switch</button>
          <PluginFrame pluginId={id} path="/ui/index.html" />
        </>
      );
    }
    vi.mocked(pluginsApi.frameToken).mockImplementation(
      async (id) =>
        new Msg<PluginFrameToken>(true, '', {
          token: `lxf.${id}`,
          expiresAt: '2026-10-18T14:00:00Z',
        }),
    );
    renderWithProviders(<Switcher />);
    await waitFor(() => expect(frame()?.getAttribute('src')).toContain('lxf.page-a'));
    fireEvent.click(screen.getByText('switch'));
    await waitFor(() => expect(frame()?.getAttribute('src')).toContain('lxf.page-b'));
    const target = frame()!.contentWindow!;
    const post = vi.spyOn(target, 'postMessage');
    sendFromFrame({ lucx: 1, type: 'ready' }, target);
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toMatchObject({ pluginId: 'page-b' });
  });

  it('stops listening once the page is gone', async () => {
    const view = renderWithProviders(<PluginFrame pluginId="page-a" path="/ui/index.html" />);
    await frameShown();
    const target = frame()!.contentWindow!;
    const post = vi.spyOn(target, 'postMessage');
    view.unmount();
    sendFromFrame({ lucx: 1, type: 'ready' }, target);
    await act(async () => {});
    expect(post).not.toHaveBeenCalled();
  });
});

function renderPage(url: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/plugins/ui/:id/*" element={<PluginFramePage />} />
        <Route path="/plugins" element={<div>plugins list</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('PluginFramePage', () => {
  it('shows a page the plugin declared in its menu', async () => {
    serveUi(ui);
    renderPage('/plugins/ui/page-a/ui/index.html');
    await waitFor(() => expect(frame()).not.toBeNull());
    expect(frame()!.getAttribute('src')).toBe('/panel/plugin-frame/lxf.tok/ui/index.html');
    expect(screen.queryByTestId('sidebar')).not.toBeNull();
  });

  it.each([
    ['a path the plugin did not declare', '/plugins/ui/page-a/ui/other.html'],
    ['a plugin that is not enabled', '/plugins/ui/page-b/ui/index.html'],
    ['a traversal', '/plugins/ui/page-a/ui/../../../x'],
    ['a hostile id', '/plugins/ui/Page%20A/ui/index.html'],
  ])('refuses %s', async (_name, url) => {
    serveUi(ui);
    renderPage(url);
    await screen.findByText('This plugin page is not available');
    expect(frame()).toBeNull();
  });

  it('offers the way back to the plugin list', async () => {
    serveUi(ui);
    renderPage('/plugins/ui/page-b/ui/index.html');
    fireEvent.click(await screen.findByRole('button', { name: 'Open plugins' }));
    expect(await screen.findByText('plugins list')).not.toBeNull();
  });

  it('shows nothing of the plugin while the menu is unknown', async () => {
    vi.mocked(pluginsApi.ui).mockReturnValue(new Promise(() => {}));
    renderPage('/plugins/ui/page-a/ui/index.html');
    await act(async () => {});
    expect(frame()).toBeNull();
    expect(screen.queryByText('This plugin page is not available')).toBeNull();
  });
});

function renderReplaceable(url: string, route = '/clients') {
  return renderWithProviders(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route
          path="/clients"
          element={
            <ReplaceablePage route={route}>
              <div data-testid="original">built-in page</div>
            </ReplaceablePage>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ReplaceablePage', () => {
  it('shows the plugin page in place of the built-in one, with a way back', async () => {
    serveUi(ui);
    renderReplaceable('/clients');
    await waitFor(() => expect(frame()).not.toBeNull());
    expect(frame()!.getAttribute('src')).toBe('/panel/plugin-frame/lxf.tok/ui/replace/clients/');
    expect(screen.queryByTestId('original')).toBeNull();
    expect(screen.queryByText('This page is replaced by the plugin Page A.')).not.toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Show original' }));
    await screen.findByTestId('original');
    expect(frame()).toBeNull();
  });

  it('shows the built-in page when ?original=1 is asked for', async () => {
    serveUi(ui);
    renderReplaceable('/clients?original=1');
    await screen.findByTestId('original');
    expect(frame()).toBeNull();
  });

  it('leaves a page alone that no plugin replaces', async () => {
    serveUi(ui);
    renderReplaceable('/clients', '/hosts');
    await screen.findByTestId('original');
    expect(frame()).toBeNull();
  });

  it('falls back to the built-in page when the plugin list cannot be read', async () => {
    serveUi(null);
    renderReplaceable('/clients');
    await screen.findByTestId('original');
  });

  it('does not flash the built-in page before it knows whether it is replaced', async () => {
    vi.mocked(pluginsApi.ui).mockReturnValue(new Promise(() => {}));
    renderReplaceable('/clients');
    await act(async () => {});
    expect(screen.queryByTestId('original')).toBeNull();
    expect(frame()).toBeNull();
  });
});
