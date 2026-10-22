// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { HttpError, type HttpResponse } from '@/api/http-init';

const PLUGIN_ID = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/;
const FORBIDDEN_ENCODED = /%(2e|2f|5c)/i;
const ALLOWED_METHODS = new Set(['GET', 'POST', 'PUT', 'DELETE']);
const MAX_PATH_LENGTH = 2000;
const MAX_BODY_CHARS = 1024 * 1024;
const MAX_ID_LENGTH = 64;
const RATE_LIMIT = 20;
const RATE_WINDOW_MS = 1000;
const MAX_IN_FLIGHT = 8;

export interface BridgeContext {
  theme: { dark: boolean; ultra: boolean; palette: string };
  lang: string;
}

export interface BridgeOptions {
  pluginId: string;
  frame: () => Window | null;
  context: () => BridgeContext;
  request: (
    method: string,
    url: string,
    data?: unknown,
    options?: { headers?: Record<string, string> },
  ) => Promise<HttpResponse>;
  now?: () => number;
}

export interface Bridge {
  handleMessage: (event: MessageEvent) => Promise<void>;
  pushContext: () => void;
  dispose: () => void;
}

function hasForbiddenChar(text: string): boolean {
  for (let i = 0; i < text.length; i += 1) {
    const code = text.charCodeAt(i);
    if (code < 0x20 || code === 0x5c || code === 0x23) return true;
  }
  return false;
}

// A page may only call its own plugin's /api/ routes; anything else answers null.
export function pluginRequestUrl(pluginId: string, path: string): string | null {
  if (!PLUGIN_ID.test(pluginId) || typeof path !== 'string' || path.length > MAX_PATH_LENGTH) {
    return null;
  }
  const query = path.indexOf('?');
  const pathPart = query === -1 ? path : path.slice(0, query);
  const queryPart = query === -1 ? '' : path.slice(query);
  if (!pathPart.startsWith('/api/') || pathPart === '/api/') return null;
  if (hasForbiddenChar(path) || FORBIDDEN_ENCODED.test(pathPart)) return null;
  if (
    pathPart
      .split('/')
      .some((segment, i) => i > 0 && (segment === '' || segment === '.' || segment === '..'))
  ) {
    return null;
  }
  return `/panel/api/plugins/x/${pluginId}${pathPart}${queryPart}`;
}

function refusal(id: string | number, status: number, error: string) {
  return { lucx: 1, type: 'fetch-result', id, ok: false, status, error };
}

function usableId(id: unknown): id is string | number {
  return (
    (typeof id === 'number' && Number.isFinite(id)) ||
    (typeof id === 'string' && id.length > 0 && id.length <= MAX_ID_LENGTH)
  );
}

function bodyTooLarge(body: unknown): boolean | 'invalid' {
  if (body === undefined) return false;
  try {
    const text = typeof body === 'string' ? body : JSON.stringify(body);
    return (text ?? '').length > MAX_BODY_CHARS;
  } catch {
    return 'invalid';
  }
}

export function createBridge(options: BridgeOptions): Bridge {
  const now = options.now ?? Date.now;
  let disposed = false;
  let inFlight = 0;
  let recent: number[] = [];

  const post = (message: unknown) => {
    const target = options.frame();
    // The sandboxed page has an opaque origin, so '*' is the only target it can be given.
    if (!disposed && target) target.postMessage(message, '*');
  };

  const overRate = () => {
    const t = now();
    recent = recent.filter((at) => t - at < RATE_WINDOW_MS);
    if (recent.length >= RATE_LIMIT) return true;
    recent.push(t);
    return false;
  };

  const handleFetch = async (data: Record<string, unknown>) => {
    const { id } = data;
    if (!usableId(id)) return;
    if (overRate() || inFlight >= MAX_IN_FLIGHT) {
      post(refusal(id, 429, 'too many requests'));
      return;
    }
    const method = typeof data.method === 'string' ? data.method.toUpperCase() : '';
    if (!ALLOWED_METHODS.has(method)) {
      post(refusal(id, 400, 'method not allowed'));
      return;
    }
    const url = pluginRequestUrl(options.pluginId, data.path as string);
    if (!url) {
      post(refusal(id, 403, 'path not allowed'));
      return;
    }
    const hasBody = method !== 'GET';
    const tooLarge = hasBody ? bodyTooLarge(data.body) : false;
    if (tooLarge === 'invalid') {
      post(refusal(id, 400, 'invalid body'));
      return;
    }
    if (tooLarge) {
      post(refusal(id, 413, 'body too large'));
      return;
    }

    inFlight += 1;
    try {
      const res = await options.request(
        method,
        url,
        hasBody ? data.body : undefined,
        hasBody ? { headers: { 'Content-Type': 'application/json' } } : {},
      );
      post({ lucx: 1, type: 'fetch-result', id, ok: true, status: res.status, body: res.data });
    } catch (error) {
      if (error instanceof HttpError) {
        post({
          lucx: 1,
          type: 'fetch-result',
          id,
          ok: false,
          status: error.status,
          body: error.response.data,
        });
      } else {
        post(refusal(id, 0, 'request failed'));
      }
    } finally {
      inFlight -= 1;
    }
  };

  return {
    async handleMessage(event) {
      const frame = options.frame();
      const data: unknown = event.data;
      if (disposed || !frame || event.source !== frame) return;
      if (typeof data !== 'object' || data === null || Array.isArray(data)) return;
      const message = data as Record<string, unknown>;
      if (message.lucx !== 1) return;
      if (message.type === 'ready') {
        post({ lucx: 1, type: 'init', pluginId: options.pluginId, ...options.context() });
      } else if (message.type === 'fetch') {
        await handleFetch(message);
      }
    },
    pushContext() {
      post({ lucx: 1, type: 'context', ...options.context() });
    },
    dispose() {
      disposed = true;
    },
  };
}
