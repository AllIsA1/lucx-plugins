// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { describe, it, expect } from 'vitest';
import { genOlcrtcLink } from '@/lib/xray/inbound-link';
import { parseLinkParts, linkMetaText } from '@/lib/xray/link-label';
import { OlcrtcInboundSettingsSchema } from '@/schemas/protocols/inbound/olcrtc';
import type { Inbound } from '@/schemas/api/inbound';

const key = 'ab'.repeat(32);

function inbound(settings: Record<string, unknown>, remark = ''): Inbound {
  return {
    protocol: 'olcrtc',
    port: 0,
    listen: '',
    remark,
    settings: { roomId: 'room', cryptoKey: key, ...settings },
    streamSettings: {},
    sniffing: {},
  } as unknown as Inbound;
}

// The same cases as TestOlcrtcClientURIName and TestOlcrtcClientURIVideoParams in internal/lucx/tunnel/olcrtc_test.go:
// the panel builds this link twice (here for the page, in Go for the subscription) and the two must agree.
describe('genOlcrtcLink, fork settings and name', () => {
  const vk = {
    provider: 'vkcalls',
    roomId: 'https://vk.ru/call/join/abc',
    transport: 'vp8channel',
    vp8Fps: 30,
    vp8Batch: 64,
  };
  const plain = `olcrtc://vkcalls?vp8channel<vp8-fps=30&vp8-batch=64>@https://vk.ru/call/join/abc#${key}`;

  it('has no name tail without a remark', () => {
    expect(genOlcrtcLink({ inbound: inbound(vk) })).toBe(plain);
  });

  it('puts the inbound remark after $, on one line, without $', () => {
    expect(genOlcrtcLink({ inbound: inbound(vk, '  Poland $1\n\tfast  ') })).toBe(
      `${plain}$Poland -1 fast`,
    );
  });

  it('leaves the name out when noLinkName is set', () => {
    expect(genOlcrtcLink({ inbound: inbound({ ...vk, noLinkName: true }, 'Poland') })).toBe(plain);
  });

  it('cuts a long name to 100 characters', () => {
    const link = genOlcrtcLink({ inbound: inbound(vk, 'я'.repeat(150)) });
    expect(link.endsWith(`$${'я'.repeat(100)}`)).toBe(true);
  });

  it('adds the QR parameters of the qrcode video codec', () => {
    const link = genOlcrtcLink({
      inbound: inbound({
        transport: 'videochannel',
        videoW: 1080,
        videoH: 1080,
        videoFps: 30,
        videoCodec: 'qrcode',
        videoQrSize: 300,
        videoQrRecovery: 'high',
        videoTileModule: 9,
      }),
    });
    expect(link).toContain(
      'videochannel<video-w=1080&video-h=1080&video-fps=30&video-codec=qrcode&video-qr-size=300&video-qr-recovery=high>@',
    );
  });

  it('adds the tile parameters of the tile video codec', () => {
    const link = genOlcrtcLink({
      inbound: inbound({
        transport: 'videochannel',
        videoW: 1080,
        videoH: 1080,
        videoFps: 30,
        videoCodec: 'tile',
        videoTileModule: 6,
        videoTileRs: 25,
        videoQrSize: 300,
      }),
    });
    expect(link).toContain('video-codec=tile&video-tile-module=6&video-tile-rs=25>@');
    expect(link).not.toContain('qr-');
  });
});

describe('olcrtc link label', () => {
  it('shows the server name of the $ tail, else the room', () => {
    const named = parseLinkParts(
      `olcrtc://jitsi?datachannel@https://meet.jit.si/r#${key}$Poland 1`,
    );
    expect(named?.protocol).toBe('olcRTC');
    expect(named && linkMetaText(named)).toBe('Poland 1');
    const unnamed = parseLinkParts(`olcrtc://jitsi?datachannel@https://meet.jit.si/r#${key}`);
    expect(unnamed && linkMetaText(unnamed)).toBe('https://meet.jit.si/r');
  });
});

describe('OlcrtcInboundSettingsSchema', () => {
  it('takes the fork providers and leaves the new settings at their defaults', () => {
    const s = OlcrtcInboundSettingsSchema.parse({ provider: 'mtslink', transport: 'vp8channel' });
    expect(s.provider).toBe('mtslink');
    expect(s).toMatchObject({
      authToken: '',
      extraKeys: [],
      dtlsProfile: '',
      udpEnabled: false,
      livenessInterval: '',
      noLinkName: false,
    });
  });

  it('still takes the settings an older panel saved', () => {
    const s = OlcrtcInboundSettingsSchema.parse({
      provider: 'jitsi',
      roomId: 'https://meet.jit.si/r',
      transport: 'datachannel',
    });
    expect(s.udpMaxFlows).toBe(0);
    expect(s.videoQrRecovery).toBe('');
  });

  it('refuses an unknown provider', () => {
    expect(() => OlcrtcInboundSettingsSchema.parse({ provider: 'zoom' })).toThrow();
  });
});
