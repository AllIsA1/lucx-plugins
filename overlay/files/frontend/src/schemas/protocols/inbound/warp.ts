// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { z } from 'zod';

// Cloudflare WARP over AmneziaWG. The row runs nothing: it holds the settings of the WARP nodes the subscription gives
// its clients, and the Better WARP plugin registers the devices and writes the lines. Keep in step with
// normalizeWarpSettings in internal/web/service/inbound_warp.go.
export const WarpAwgSchema = z.object({
  jc: z.number().int().min(0).max(16).default(4),
  jmin: z.number().int().min(0).max(1280).default(40),
  jmax: z.number().int().min(0).max(1280).default(70),
  s1: z.number().int().min(0).max(1132).default(0),
  s2: z.number().int().min(0).max(1188).default(0),
  h1: z.string().default(''),
  h2: z.string().default(''),
  h3: z.string().default(''),
  h4: z.string().default(''),
  i1: z.string().default(''),
  i2: z.string().default(''),
  i3: z.string().default(''),
  i4: z.string().default(''),
  i5: z.string().default(''),
});

export const WarpInboundSettingsSchema = z.object({
  // host:port, up to three, one node each; empty means the endpoints Better WARP keeps (its scan).
  endpoints: z.array(z.string()).max(3).default([]),
  mtu: z.number().int().min(576).max(1420).default(1280),
  dns: z.string().default('1.1.1.1, 1.0.0.1, 2606:4700:4700::1111, 2606:4700:4700::1001'),
  ipv6: z.boolean().default(true),
  keepalive: z.number().int().min(0).max(600).default(25),
  preset: z.enum(['standard', 'light', 'strong', 'custom']).default('standard'),
  awg: WarpAwgSchema.default(WarpAwgSchema.parse({})),
  clients: z.array(z.unknown()).default([]),
});
export type WarpInboundSettings = z.infer<typeof WarpInboundSettingsSchema>;

// What each preset sets, as in the backend.
export const WARP_PRESETS: Record<string, { jc: number; jmin: number; jmax: number }> = {
  standard: { jc: 4, jmin: 40, jmax: 70 },
  light: { jc: 2, jmin: 20, jmax: 40 },
  strong: { jc: 8, jmin: 50, jmax: 150 },
};
