// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { createDefaultWarpInboundSettings } from '@/lib/xray/inbound-defaults';
import { Protocols } from '@/schemas/primitives';
import { WARP_PRESETS, WarpInboundSettingsSchema } from '@/schemas/protocols/inbound/warp';
import { InboundSettingsSchema } from '@/schemas/protocols/inbound';

describe('WARP inbound', () => {
  it('has defaults that are known to work with Cloudflare', () => {
    const s = createDefaultWarpInboundSettings();
    expect(s.mtu).toBe(1280);
    expect(s.awg).toMatchObject({ jc: 4, jmin: 40, jmax: 70, s1: 0, s2: 0 });
    expect(s.endpoints).toEqual([]);
    expect(WarpInboundSettingsSchema.parse({})).toMatchObject({
      mtu: 1280,
      ipv6: true,
      keepalive: 25,
    });
  });

  it('is a protocol of the inbound union', () => {
    expect(Protocols.WARP).toBe('warp');
    const parsed = InboundSettingsSchema.parse({ protocol: 'warp', settings: {} });
    expect(parsed.protocol).toBe('warp');
  });

  it('keeps at most three endpoints and a sane MTU', () => {
    expect(() =>
      WarpInboundSettingsSchema.parse({ endpoints: ['a:1', 'a:2', 'a:3', 'a:4'] }),
    ).toThrow();
    expect(() => WarpInboundSettingsSchema.parse({ mtu: 100 })).toThrow();
  });

  it('presets are the ones the backend knows', () => {
    expect(WARP_PRESETS.strong).toEqual({ jc: 8, jmin: 50, jmax: 150 });
    expect(Object.keys(WARP_PRESETS).sort()).toEqual(['light', 'standard', 'strong']);
  });
});
