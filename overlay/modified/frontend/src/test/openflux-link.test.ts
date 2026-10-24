// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { inflateRawSync } from 'node:zlib';
import { describe, expect, it } from 'vitest';

import { encodeOpenfluxLink } from '@/lib/xray/openflux-link';
import { genOpenfluxLink } from '@/lib/xray/inbound-link';
import type { Inbound } from '@/schemas/api/inbound';

function decode(link: string): string {
  const body = link.slice('openflux://v1/'.length);
  const b64 = body.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
  const raw = Buffer.from(padded, 'base64');
  return inflateRawSync(raw).toString('utf8');
}

describe('encodeOpenfluxLink', () => {
  it('emits a stored-block openflux://v1 link', () => {
    const link = encodeOpenfluxLink({
      secret: '0123456789abcdef',
      host: '203.0.113.5',
      port: 18445,
      remark: 'exit',
      yandexUrl: 'https://docs.example/view?id=1',
    });
    expect(link.startsWith('openflux://v1/')).toBe(true);
    const payload = JSON.parse(decode(link));
    expect(payload.negotiate).toBe(true);
    expect(payload.context).toBe('https://docs.example/view?id=1');
    expect(payload.transports[0]).toEqual({
      type: 'direct',
      priority: 100,
      dial: '203.0.113.5:18445',
    });
    expect(payload.transports[1].type).toBe('yandex');
  });

  it('refuses a short secret', () => {
    expect(encodeOpenfluxLink({ secret: 'short', host: '1.2.3.4', port: 1 })).toBe('');
  });

  it('genOpenfluxLink prefers shareHost', () => {
    const inbound = {
      protocol: 'openflux',
      port: 18445,
      remark: 'n',
      settings: { secret: '0123456789abcdef', shareHost: '9.9.9.9' },
    } as Inbound;
    const payload = JSON.parse(decode(genOpenfluxLink({ inbound, address: '1.1.1.1' })));
    expect(payload.transports[0].dial).toBe('9.9.9.9:18445');
  });

  it('carries the Volga, Board, Telemost and Avito carriers with the core transport types', () => {
    const link = encodeOpenfluxLink({
      secret: '0123456789abcdef',
      host: '203.0.113.5',
      port: 18445,
      yandexUrl: 'https://y.example/1',
      volgaUrl: 'https://v.example/1',
      boardUrl: 'https://b.example/1',
      telemostUrl: 'https://telemost.yandex.ru/j/1',
      avitoUrl: 'https://a.example/1',
    });
    const payload = JSON.parse(decode(link));
    const byType = Object.fromEntries(
      payload.transports.map((t: { type: string; url?: string }) => [t.type, t.url]),
    );
    expect(byType).toMatchObject({
      yandex: 'https://y.example/1',
      vyandex: 'https://v.example/1',
      boards: 'https://b.example/1',
      telemost: 'https://telemost.yandex.ru/j/1',
      avito: 'https://a.example/1',
    });
    expect(payload.transports).toHaveLength(6);
    expect(payload.transports[0].type).toBe('direct');
  });

  it('puts several links of one carrier into one comma-separated value', () => {
    const link = encodeOpenfluxLink({
      secret: '0123456789abcdef',
      host: '203.0.113.5',
      port: 18445,
      mailruUrl: ' https://m.example/a\n https://m.example/b|https://m.example/c ',
    });
    const payload = JSON.parse(decode(link));
    const want = 'https://m.example/a,https://m.example/b,https://m.example/c';
    expect(payload.context).toBe(want);
    expect(payload.transports[1]).toEqual({ type: 'mailru', priority: 40, url: want });
  });

  it('keeps the context of an inbound that has only the older fields', () => {
    const payload = JSON.parse(
      decode(
        encodeOpenfluxLink({
          secret: '0123456789abcdef',
          host: '1.2.3.4',
          port: 1,
          mailruUrl: 'https://m.example/a',
          cupsUrl: 'https://c.example/a',
        }),
      ),
    );
    expect(payload.context).toBe('https://m.example/a');
  });
});
