// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { HttpError } from '@/api/http-init';
import {
  createBridge,
  pluginRequestUrl,
  type BridgeContext,
  type BridgeOptions,
} from '@/pages/plugins/pluginBridge';

describe('pluginRequestUrl', () => {
  it('maps a plugin-relative API path onto the plugin own route', () => {
    expect(pluginRequestUrl('page-a', '/api/ping')).toBe('/panel/api/plugins/x/page-a/api/ping');
    expect(pluginRequestUrl('page-a', '/api/items?limit=5&q=a%20b')).toBe(
      '/panel/api/plugins/x/page-a/api/items?limit=5&q=a%20b',
    );
  });

  it('refuses everything that is not below the plugin own /api/', () => {
    for (const path of [
      '/panel/api/clients/list',
      '/ui/index.html',
      'api/ping',
      '/api',
      '/api/../../clients/list',
      '/api/./x',
      '/api//x',
      '/api/%2e%2e/x',
      '/api/%2E%2E/x',
      '/api/a%2fb',
      '/api/a%5cb',
      '/api\\x',
      '//evil.example/api/x',
      'https://evil.example/api/x',
      '/api/x#frag',
      '/api/x\u0000',
      `/api/${'a'.repeat(2100)}`,
      '',
    ]) {
      expect(pluginRequestUrl('page-a', path), path).toBeNull();
    }
  });

  it('refuses a plugin id that could change the route', () => {
    for (const id of ['', 'A', '../x', 'a/b', 'page a', '-ab']) {
      expect(pluginRequestUrl(id, '/api/ping'), id).toBeNull();
    }
  });
});

const dark: BridgeContext = { theme: { dark: true, ultra: false, palette: 'warm' }, lang: 'ru-RU' };

interface Harness {
  frame: { postMessage: ReturnType<typeof vi.fn> };
  request: ReturnType<typeof vi.fn>;
  options: BridgeOptions;
  bridge: ReturnType<typeof createBridge>;
  clock: { now: number };
  send: (data: unknown, source?: unknown) => Promise<void>;
  replies: () => Record<string, unknown>[];
}

function harness(overrides: Partial<BridgeOptions> = {}): Harness {
  const frame = { postMessage: vi.fn() };
  const request = vi.fn();
  const clock = { now: 1000 };
  const options: BridgeOptions = {
    pluginId: 'page-a',
    frame: () => frame as unknown as Window,
    context: () => dark,
    request: request as BridgeOptions['request'],
    now: () => clock.now,
    ...overrides,
  };
  const bridge = createBridge(options);
  return {
    frame,
    request,
    options,
    bridge,
    clock,
    send: async (data, source = frame) => {
      await bridge.handleMessage({ source: source as Window, data } as MessageEvent);
    },
    replies: () => frame.postMessage.mock.calls.map((c) => c[0] as Record<string, unknown>),
  };
}

const call = (over: Record<string, unknown> = {}) => ({
  lucx: 1,
  type: 'fetch',
  id: 7,
  method: 'GET',
  path: '/api/ping',
  ...over,
});

describe('plugin bridge', () => {
  let h: Harness;
  beforeEach(() => {
    h = harness();
  });

  it('only listens to the frame it created', async () => {
    await h.send(call(), { postMessage: vi.fn() });
    await h.send({ lucx: 1, type: 'ready' }, window);
    await h.send(call(), null);
    expect(h.request).not.toHaveBeenCalled();
    expect(h.frame.postMessage).not.toHaveBeenCalled();
  });

  it('ignores anything that is not a v1 message', async () => {
    for (const data of [
      null,
      'fetch',
      42,
      [],
      {},
      { type: 'fetch' },
      { lucx: 2, type: 'ready' },
      { lucx: 1 },
      { lucx: 1, type: 'nope' },
    ]) {
      await h.send(data);
    }
    expect(h.request).not.toHaveBeenCalled();
    expect(h.frame.postMessage).not.toHaveBeenCalled();
  });

  it('answers ready with the plugin id, theme and language', async () => {
    await h.send({ lucx: 1, type: 'ready' });
    expect(h.replies()).toEqual([{ lucx: 1, type: 'init', pluginId: 'page-a', ...dark }]);
    expect(h.frame.postMessage).toHaveBeenCalledWith(expect.anything(), '*');
  });

  it('pushes a new context when the theme or language changes', () => {
    h.bridge.pushContext();
    expect(h.replies()).toEqual([{ lucx: 1, type: 'context', ...dark }]);
  });

  it('performs an allowed call with the panel session and returns the result', async () => {
    h.request.mockResolvedValue({ ok: true, status: 200, statusText: 'OK', data: { pong: true } });
    await h.send(call({ method: 'post', path: '/api/items?limit=5', body: { a: 1 } }));
    expect(h.request).toHaveBeenCalledWith(
      'POST',
      '/panel/api/plugins/x/page-a/api/items?limit=5',
      { a: 1 },
      { headers: { 'Content-Type': 'application/json' } },
    );
    expect(h.replies()).toEqual([
      { lucx: 1, type: 'fetch-result', id: 7, ok: true, status: 200, body: { pong: true } },
    ]);
  });

  it('sends no body and no content type for a GET', async () => {
    h.request.mockResolvedValue({ ok: true, status: 200, statusText: 'OK', data: '' });
    await h.send(call({ body: { ignored: true } }));
    expect(h.request).toHaveBeenCalledWith(
      'GET',
      '/panel/api/plugins/x/page-a/api/ping',
      undefined,
      {},
    );
  });

  it('returns the plugin error status and body instead of throwing', async () => {
    h.request.mockRejectedValue(new HttpError(404, 'Not Found', { success: false, msg: 'nope' }));
    await h.send(call());
    expect(h.replies()).toEqual([
      {
        lucx: 1,
        type: 'fetch-result',
        id: 7,
        ok: false,
        status: 404,
        body: { success: false, msg: 'nope' },
      },
    ]);
  });

  it('reports a transport failure as status 0', async () => {
    h.request.mockRejectedValue(new TypeError('Failed to fetch'));
    await h.send(call());
    expect(h.replies()).toEqual([
      { lucx: 1, type: 'fetch-result', id: 7, ok: false, status: 0, error: 'request failed' },
    ]);
  });

  it('refuses a path outside the plugin without making any request', async () => {
    for (const path of [
      '/panel/api/clients/list',
      '/api/../../x',
      '//evil.example/api/x',
      '/ui/index.html',
    ]) {
      await h.send(call({ path }));
    }
    expect(h.request).not.toHaveBeenCalled();
    const replies = h.replies();
    expect(replies).toHaveLength(4);
    for (const r of replies)
      expect(r).toMatchObject({ type: 'fetch-result', id: 7, ok: false, status: 403 });
  });

  it('refuses methods other than GET, POST, PUT and DELETE', async () => {
    for (const method of ['PATCH', 'TRACE', 'CONNECT', 'get ', '', 5]) {
      await h.send(call({ method }));
    }
    expect(h.request).not.toHaveBeenCalled();
    expect(h.replies().every((r) => r.status === 400)).toBe(true);
  });

  it('refuses an oversized body', async () => {
    await h.send(call({ method: 'POST', body: 'x'.repeat(1024 * 1024 + 1) }));
    expect(h.request).not.toHaveBeenCalled();
    expect(h.replies()[0]).toMatchObject({ ok: false, status: 413 });
  });

  it('refuses a call without a usable id', async () => {
    await h.send(call({ id: undefined }));
    await h.send(call({ id: { a: 1 } }));
    await h.send(call({ id: 'x'.repeat(65) }));
    expect(h.request).not.toHaveBeenCalled();
    expect(h.frame.postMessage).not.toHaveBeenCalled();
  });

  it('limits how fast a page may call, per second', async () => {
    h.request.mockResolvedValue({ ok: true, status: 200, statusText: 'OK', data: null });
    for (let i = 0; i < 25; i += 1) await h.send(call({ id: i }));
    expect(h.request).toHaveBeenCalledTimes(20);
    expect(h.replies().filter((r) => r.status === 429)).toHaveLength(5);

    h.clock.now += 1001;
    await h.send(call({ id: 99 }));
    expect(h.request).toHaveBeenCalledTimes(21);
  });

  it('limits calls in flight', async () => {
    h.request.mockReturnValue(new Promise(() => {}));
    const sends = Array.from({ length: 10 }, (_, i) => h.send(call({ id: i })));
    await Promise.race([Promise.all(sends), Promise.resolve()]);
    expect(h.request).toHaveBeenCalledTimes(8);
    expect(h.replies().filter((r) => r.status === 429)).toHaveLength(2);
  });

  it('stops answering once disposed', async () => {
    h.bridge.dispose();
    await h.send(call());
    await h.send({ lucx: 1, type: 'ready' });
    h.bridge.pushContext();
    expect(h.request).not.toHaveBeenCalled();
    expect(h.frame.postMessage).not.toHaveBeenCalled();
  });

  it('does nothing while the frame is not there yet', async () => {
    const detached = harness({ frame: () => null });
    detached.bridge.pushContext();
    await detached.bridge.handleMessage({
      source: null,
      data: { lucx: 1, type: 'ready' },
    } as MessageEvent);
    expect(detached.request).not.toHaveBeenCalled();
  });
});
