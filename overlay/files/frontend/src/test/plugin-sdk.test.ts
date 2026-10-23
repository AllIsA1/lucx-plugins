// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';

import { createBridge, type BridgeContext } from '@/pages/plugins/pluginBridge';

// A copy of the SDK plugins ship (examples/sdk/lucx-ui.js in the plugin system repository, which checks
// that this copy is current).
const SDK = readFileSync(
  fileURLToPath(new URL('./fixtures/lucx-ui-sdk.js', import.meta.url)),
  'utf8',
);

type Listener = (event: { source: unknown; data: unknown }) => void;

interface FakeWindow {
  listeners: Listener[];
  parent: { postMessage: Mock<(data: unknown, target?: string) => void> };
  document: { documentElement: { setAttribute: ReturnType<typeof vi.fn>; lang: string } };
  addEventListener: (type: string, listener: Listener) => void;
  setTimeout: typeof setTimeout;
  clearTimeout: typeof clearTimeout;
  lucx?: Sdk;
}

interface Sdk {
  ready: () => Promise<BridgeContext & { pluginId: string }>;
  context: () => (BridgeContext & { pluginId: string }) | null;
  onContext: (cb: (ctx: BridgeContext) => void) => () => void;
  api: (
    path: string,
    options?: { method?: string; body?: unknown },
  ) => Promise<Record<string, unknown>>;
  applyTheme: (root?: unknown) => void;
}

function loadSdk(): FakeWindow {
  const win: FakeWindow = {
    listeners: [],
    parent: { postMessage: vi.fn() },
    document: { documentElement: { setAttribute: vi.fn(), lang: '' } },
    addEventListener: (type, listener) => {
      if (type === 'message') win.listeners.push(listener);
    },
    setTimeout: ((...args: Parameters<typeof setTimeout>) =>
      setTimeout(...args)) as typeof setTimeout,
    clearTimeout: (id) => clearTimeout(id),
  };
  new Function('window', SDK)(win);
  return win;
}

const fromHost = (win: FakeWindow, data: unknown, source: unknown = win.parent) =>
  win.listeners.forEach((l) => l({ source, data }));

const dark: BridgeContext = { theme: { dark: true, ultra: false, palette: 'warm' }, lang: 'ru-RU' };

describe('lucx-ui.js', () => {
  let win: FakeWindow;
  beforeEach(() => {
    win = loadSdk();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('tells the panel it is ready, to the parent only', () => {
    expect(win.parent.postMessage).toHaveBeenCalledWith({ lucx: 1, type: 'ready' }, '*');
    expect(win.lucx).toBeDefined();
  });

  it('resolves ready() with what the panel sent and keeps the context current', async () => {
    const seen: BridgeContext[] = [];
    win.lucx!.onContext((c) => seen.push(c));
    expect(win.lucx!.context()).toBeNull();

    fromHost(win, { lucx: 1, type: 'init', pluginId: 'page-a', ...dark });
    await expect(win.lucx!.ready()).resolves.toMatchObject({ pluginId: 'page-a', lang: 'ru-RU' });
    fromHost(win, {
      lucx: 1,
      type: 'context',
      theme: { dark: false, ultra: false, palette: 'default' },
      lang: 'en-US',
    });

    expect(win.lucx!.context()).toMatchObject({
      pluginId: 'page-a',
      lang: 'en-US',
      theme: { dark: false },
    });
    expect(seen.map((c) => c.lang)).toEqual(['ru-RU', 'en-US']);
  });

  it('stops calling a listener that unsubscribed', () => {
    const cb = vi.fn();
    const off = win.lucx!.onContext(cb);
    fromHost(win, { lucx: 1, type: 'init', pluginId: 'page-a', ...dark });
    off();
    fromHost(win, { lucx: 1, type: 'context', ...dark });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('listens only to its parent and to version 1', async () => {
    const cb = vi.fn();
    win.lucx!.onContext(cb);
    fromHost(win, { lucx: 1, type: 'init', pluginId: 'x', ...dark }, { other: true });
    fromHost(win, { lucx: 2, type: 'init', pluginId: 'x', ...dark });
    fromHost(win, null);
    fromHost(win, 'init');
    expect(cb).not.toHaveBeenCalled();
    expect(win.lucx!.context()).toBeNull();
  });

  it('sends api calls with a fresh id and resolves each with its own answer', async () => {
    const a = win.lucx!.api('/api/a');
    const b = win.lucx!.api('/api/b', { method: 'post', body: { x: 1 } });
    const calls = win.parent.postMessage.mock.calls
      .map((c) => c[0] as Record<string, unknown>)
      .filter((m) => m.type === 'fetch');
    expect(calls).toHaveLength(2);
    expect(calls[0]).toMatchObject({ lucx: 1, method: 'GET', path: '/api/a' });
    expect(calls[1]).toMatchObject({ lucx: 1, method: 'POST', path: '/api/b', body: { x: 1 } });
    expect(calls[0].id).not.toBe(calls[1].id);

    fromHost(win, {
      lucx: 1,
      type: 'fetch-result',
      id: calls[1].id,
      ok: true,
      status: 201,
      body: 'second',
    });
    fromHost(win, {
      lucx: 1,
      type: 'fetch-result',
      id: calls[0].id,
      ok: false,
      status: 404,
      body: { msg: 'no' },
    });
    await expect(b).resolves.toEqual({ ok: true, status: 201, body: 'second' });
    await expect(a).resolves.toEqual({ ok: false, status: 404, body: { msg: 'no' } });
  });

  it('passes the refusal reason along', async () => {
    const p = win.lucx!.api('/panel/api/clients/list');
    const id = (win.parent.postMessage.mock.calls.at(-1)![0] as { id: number }).id;
    fromHost(win, {
      lucx: 1,
      type: 'fetch-result',
      id,
      ok: false,
      status: 403,
      error: 'path not allowed',
    });
    await expect(p).resolves.toEqual({ ok: false, status: 403, error: 'path not allowed' });
  });

  it('ignores an answer nobody asked for and an answer from anyone but the panel', async () => {
    const p = win.lucx!.api('/api/a');
    const id = (win.parent.postMessage.mock.calls.at(-1)![0] as { id: number }).id;
    fromHost(win, { lucx: 1, type: 'fetch-result', id: 9999, ok: true, status: 200 });
    fromHost(
      win,
      { lucx: 1, type: 'fetch-result', id, ok: true, status: 200, body: 'forged' },
      { evil: true },
    );
    let settled = false;
    void p.then(() => {
      settled = true;
    });
    await Promise.resolve();
    expect(settled).toBe(false);
    fromHost(win, { lucx: 1, type: 'fetch-result', id, ok: true, status: 200, body: 'real' });
    await expect(p).resolves.toMatchObject({ body: 'real' });
  });

  it('gives up on a call the panel never answers', async () => {
    vi.useFakeTimers();
    const p = win.lucx!.api('/api/slow');
    vi.advanceTimersByTime(35_000);
    await expect(p).resolves.toEqual({ ok: false, status: 0, error: 'timeout' });
  });

  it('rejects a call that is not even a path', async () => {
    await expect(win.lucx!.api(42 as unknown as string)).rejects.toThrow(TypeError);
    const sent = win.parent.postMessage.mock.calls.filter(
      (c) => (c[0] as { type: string }).type === 'fetch',
    );
    expect(sent).toHaveLength(0);
  });

  it('applies the panel theme and language to a document', () => {
    fromHost(win, { lucx: 1, type: 'init', pluginId: 'page-a', ...dark });
    win.lucx!.applyTheme();
    expect(win.document.documentElement.setAttribute).toHaveBeenCalledWith('data-theme', 'dark');
    expect(win.document.documentElement.setAttribute).toHaveBeenCalledWith('data-palette', 'warm');
    expect(win.document.documentElement.setAttribute).toHaveBeenCalledWith('lang', 'ru-RU');
  });
});

describe('lucx-ui.js against the panel bridge', () => {
  it('speaks the same protocol: init, a call through the bridge, and a refusal', async () => {
    const win = loadSdk();
    const context: BridgeContext = dark;
    const request = vi
      .fn()
      .mockResolvedValue({ ok: true, status: 200, statusText: 'OK', data: { pong: true } });
    // The "iframe window" the bridge posts to is the SDK's own message channel.
    const frame = {
      postMessage: (data: unknown) => queueMicrotask(() => fromHost(win, data)),
    } as unknown as Window;
    const bridge = createBridge({
      pluginId: 'page-a',
      frame: () => frame,
      context: () => context,
      request,
    });
    win.parent.postMessage.mockImplementation((data: unknown) => {
      void bridge.handleMessage({ source: frame, data } as unknown as MessageEvent);
    });
    // Re-announce readiness now that the bridge is listening.
    win.parent.postMessage({ lucx: 1, type: 'ready' }, '*');

    await expect(win.lucx!.ready()).resolves.toMatchObject({ pluginId: 'page-a', lang: 'ru-RU' });
    await expect(win.lucx!.api('/api/ping')).resolves.toEqual({
      ok: true,
      status: 200,
      body: { pong: true },
    });
    expect(request).toHaveBeenCalledWith(
      'GET',
      '/panel/api/plugins/x/page-a/api/ping',
      undefined,
      {},
    );
    await expect(win.lucx!.api('/panel/api/clients/list')).resolves.toMatchObject({
      ok: false,
      status: 403,
    });
    expect(request).toHaveBeenCalledTimes(1);
  });
});
