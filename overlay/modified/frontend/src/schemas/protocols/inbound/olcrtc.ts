// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { z } from 'zod';

export const OlcrtcInboundSettingsSchema = z.object({
  provider: z
    .enum(['jitsi', 'telemost', 'wbstream', 'salutejazz', 'vkcalls', 'mtslink'])
    .default('jitsi'),
  roomId: z.string().default(''),
  cryptoKey: z.string().default(''),
  transport: z
    .enum(['datachannel', 'vp8channel', 'seichannel', 'videochannel'])
    .default('datachannel'),
  dns: z.string().default('8.8.8.8:53'),
  vp8Fps: z.number().int().min(1).max(120).default(60),
  vp8Batch: z.number().int().min(1).max(64).default(64),
  seiFps: z.number().int().min(1).max(120).default(30),
  seiBatch: z.number().int().min(1).max(64).default(64),
  seiFrag: z.number().int().min(1).default(900),
  seiAck: z.number().int().min(1).default(2000),
  videoW: z.number().int().min(1).default(1080),
  videoH: z.number().int().min(1).default(1080),
  videoFps: z.number().int().min(1).max(120).default(30),
  videoCodec: z.enum(['qrcode', 'tile']).default('qrcode'),
  // Settings of the ghostlane-project fork (the core of the Better olcRTC plugin). A setting left empty or 0 is not
  // sent to the core, so the original core keeps running what it ran before.
  videoQrSize: z.number().int().min(0).default(0),
  videoQrRecovery: z.enum(['', 'low', 'medium', 'high', 'highest']).default(''),
  videoTileModule: z.number().int().min(0).max(270).default(0),
  videoTileRs: z.number().int().min(0).max(200).default(0),
  authToken: z.string().default(''),
  extraKeys: z.array(z.string()).default([]),
  dtlsProfile: z.enum(['', 'off', 'chrome-linux-138-compat-v1']).default(''),
  udpEnabled: z.boolean().default(false),
  udpMaxFlows: z.number().int().min(0).default(0),
  livenessInterval: z.string().default(''),
  livenessTimeout: z.string().default(''),
  livenessFailures: z.number().int().min(0).default(0),
  maxSessionDuration: z.string().default(''),
  trafficMaxPayload: z.number().int().min(0).default(0),
  trafficMinDelay: z.string().default(''),
  trafficMaxDelay: z.string().default(''),
  noLinkName: z.boolean().default(false),
  debug: z.boolean().default(false),
  routeThroughXray: z.boolean().default(false),
  outboundTag: z.string().default(''),
  routeXrayPort: z.number().int().min(0).max(65535).default(0),
});
export type OlcrtcInboundSettings = z.infer<typeof OlcrtcInboundSettingsSchema>;
