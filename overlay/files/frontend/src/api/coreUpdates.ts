// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { z } from 'zod';

import { HttpUtil, Msg } from '@/utils';

// The Better Cores plugin keeps the tunnel cores up to date. Its answers reach the Cores page through the
// plugin system's relay, in the panel's usual envelope. Where the plugin is not installed or not turned on, the
// relay answers an error and the page shows nothing of this.

const BASE = '/panel/api/plugins/x/better-cores/api/cores';

const ReleaseSchema = z.object({
  tag: z.string(),
  publishedAt: z.string().optional(),
  url: z.string().optional(),
});

const CoreUpdateSchema = z.object({
  id: z.string(),
  name: z.string(),
  latest: ReleaseSchema.optional(),
  installed: ReleaseSchema.extend({ at: z.string().optional() }).optional(),
  updateAvailable: z.boolean(),
  installing: z.boolean(),
  error: z.string().optional(),
});

export const CoreUpdatesSchema = z.object({
  checkedAt: z.string(),
  nextCheckAt: z.string(),
  checking: z.boolean(),
  cores: z.array(CoreUpdateSchema),
});

export type CoreUpdate = z.infer<typeof CoreUpdateSchema>;
export type CoreUpdates = z.infer<typeof CoreUpdatesSchema>;

// A look the plugin never made carries the zero time.
export function isNever(iso: string | undefined): boolean {
  return !iso || iso.startsWith('0001-');
}

function parse(msg: Msg<unknown>): Msg<CoreUpdates> {
  if (!msg.success) return msg as Msg<CoreUpdates>;
  const result = CoreUpdatesSchema.safeParse(msg.obj);
  // Anything else answering on this address is not the plugin.
  if (!result.success) return new Msg<CoreUpdates>(false, 'not the Better Cores plugin');
  return new Msg<CoreUpdates>(true, msg.msg, result.data);
}

export const coreUpdatesApi = {
  state: async (): Promise<Msg<CoreUpdates>> =>
    parse(await HttpUtil.get<unknown>(BASE, undefined, { silent: true })),
  check: async (): Promise<Msg<CoreUpdates>> =>
    parse(await HttpUtil.post<unknown>(`${BASE}/check`, undefined, { silent: true })),
  install: async (id: string): Promise<Msg<CoreUpdates>> =>
    parse(
      await HttpUtil.post<unknown>(`${BASE}/${encodeURIComponent(id)}/install`, undefined, {
        silent: true,
      }),
    ),
};
