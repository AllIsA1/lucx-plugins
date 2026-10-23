// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { z } from 'zod';

// Go encodes a nil slice as null; normalise to [] at the boundary.
const stringList = z
  .array(z.string())
  .nullish()
  .transform((v) => v ?? []);

export const PluginSourceSchema = z.object({
  owner: z.string(),
  repo: z.string(),
  ref: z.string().optional(),
});

export const PluginRuntimeSchema = z.object({
  state: z.enum(['stopped', 'starting', 'running', 'stopping', 'failed']),
  healthy: z.boolean(),
  healthError: z.string().optional(),
  pid: z.number(),
  restarts: z.number(),
  detail: z.string().optional(),
  eventsSent: z.number().optional(),
  eventsFailed: z.number().optional(),
  eventsDropped: z.number().optional(),
  pipelineErrors: z.number().optional(),
  lastError: z.string().optional(),
});

export const PluginPreviousSchema = z.object({
  version: z.string(),
  commit: z.string(),
});

export const PluginEntrySchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  type: z.string(),
  scope: z.string(),
  source: PluginSourceSchema,
  commit: z.string(),
  enabled: z.boolean(),
  disabledReason: z.string().optional(),
  managedByMaster: z.boolean().optional(),
  installedAt: z.string(),
  permissions: stringList,
  runtime: PluginRuntimeSchema.nullish(),
  previous: PluginPreviousSchema.nullish(),
});

export const IsolationSchema = z.object({
  runner: z.string(),
  level: z.enum(['strong', 'partial', 'none']),
  network: z.boolean(),
  filesystem: z.boolean(),
  user: z.boolean(),
  notes: z.array(z.string()).nullish(),
});

export const PluginsStateSchema = z.object({
  available: z.boolean(),
  allowMasterManagement: z.boolean(),
  processesAvailable: z.boolean(),
  processesError: z.string(),
  processesIsolation: IsolationSchema.nullish(),
  safeMode: z.boolean(),
  error: z.string(),
  archiveBase: z.string(),
  customArchiveBase: z.string(),
  defaultArchiveBase: z.string(),
  plugins: z.array(PluginEntrySchema),
});

export const PluginManifestSchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  apiVersion: z.string(),
  minPanelVersion: z.string().optional(),
  description: z.string().optional(),
  author: z.string().optional(),
  license: z.string().optional(),
  type: z.string(),
  scope: z.string(),
  permissions: stringList,
});

export const UpdatePreviewSchema = z.object({
  token: z.string(),
  commit: z.string(),
  manifest: PluginManifestSchema,
  current: z.object({ version: z.string(), commit: z.string() }),
  upToDate: z.boolean(),
  diff: z.object({
    added: stringList,
    removed: stringList,
    kept: stringList,
    dangerousAdded: stringList,
  }),
  typeChanged: z.boolean(),
  scopeChanged: z.boolean(),
  needsConfirmation: z.boolean(),
});

export const CatalogEntrySchema = z.object({
  id: z.string(),
  name: z.string(),
  summary: z.string(),
  repo: z.string(),
  commit: z.string(),
  version: z.string(),
  manifestSha256: z.string(),
  type: z.string(),
  scope: z.string(),
  permissions: stringList,
  icon: z.string().optional(),
  license: z.string().optional(),
  author: z.string().optional(),
  verified: z.object({ by: z.string(), at: z.string(), notes: z.string().optional() }).nullish(),
});

export const CatalogPluginSchema = CatalogEntrySchema.extend({
  status: z.enum(['available', 'installed', 'update-available', 'id-conflict']),
  installed: z.object({ version: z.string(), commit: z.string() }).nullish(),
});

export const CatalogViewSchema = z.object({
  state: z.enum(['ok', 'stale', 'error', 'unconfigured', 'not-published']),
  error: z.string().optional(),
  base: z.string(),
  customBase: z.string(),
  fetchedAt: z.string(),
  generatedAt: z.string(),
  sequence: z.number(),
  plugins: z.array(CatalogPluginSchema),
});

export const PluginPreviewSchema = z.object({
  token: z.string(),
  source: PluginSourceSchema,
  commit: z.string(),
  manifest: PluginManifestSchema,
  dangerous: stringList,
  installed: z.boolean(),
  expiresAt: z.string(),
  catalog: z.enum(['listed', 'unlisted', 'unavailable']).optional(),
});

export const NodePluginSchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  type: z.string(),
  scope: z.string(),
  commit: z.string(),
  enabled: z.boolean(),
  disabledReason: z.string().optional(),
  managedByMaster: z.boolean().optional(),
  permissions: stringList,
  runtime: PluginRuntimeSchema.nullish(),
});

export const NodePluginsReportSchema = z.object({
  available: z.boolean(),
  allowMasterManagement: z.boolean(),
  processesAvailable: z.boolean(),
  processesError: z.string(),
  safeMode: z.boolean(),
  error: z.string(),
  plugins: z
    .array(NodePluginSchema)
    .nullish()
    .transform((v) => v ?? []),
});

export const NodePluginStatusSchema = z.object({
  nodeId: z.number(),
  nodeName: z.string(),
  state: z.enum(['ok', 'pending', 'offline', 'unsupported', 'error', 'locked']),
  error: z.string().optional(),
  fetchedAt: z.number(),
  plugins: NodePluginsReportSchema.nullish(),
});

export const PluginUiMenuItemSchema = z.object({
  pluginId: z.string(),
  pluginName: z.string(),
  title: z.string(),
  icon: z.string(),
  path: z.string(),
});

export const PluginUiReplaceSchema = z.object({
  pluginId: z.string(),
  pluginName: z.string(),
  path: z.string(),
});

export const PluginUiSchema = z.object({
  menu: z.array(PluginUiMenuItemSchema),
  replaces: z.record(z.string(), PluginUiReplaceSchema),
});

export const PluginFrameTokenSchema = z.object({ token: z.string(), expiresAt: z.string() });

export type PluginRuntime = z.infer<typeof PluginRuntimeSchema>;
export type PluginEntry = z.infer<typeof PluginEntrySchema>;
export type PluginsState = z.infer<typeof PluginsStateSchema>;
export type Isolation = z.infer<typeof IsolationSchema>;
export type PluginPreview = z.infer<typeof PluginPreviewSchema>;
export type NodePlugin = z.infer<typeof NodePluginSchema>;
export type NodePluginStatus = z.infer<typeof NodePluginStatusSchema>;
export type PluginUiMenuItem = z.infer<typeof PluginUiMenuItemSchema>;
export type PluginUiReplace = z.infer<typeof PluginUiReplaceSchema>;
export type PluginUi = z.infer<typeof PluginUiSchema>;
export type PluginFrameToken = z.infer<typeof PluginFrameTokenSchema>;
export type UpdatePreview = z.infer<typeof UpdatePreviewSchema>;
export type CatalogPlugin = z.infer<typeof CatalogPluginSchema>;
export type CatalogView = z.infer<typeof CatalogViewSchema>;

// The plugin system service itself (not a plugin): what it runs and whether a newer release is ready.
export const PluginSystemSchema = z.object({
  managed: z.boolean(),
  version: z.string(),
  available: z.string(),
  checkedAt: z.string(),
  busy: z.boolean(),
  pending: z.string(),
  rolledBack: z.string(),
  error: z.string(),
});
export type PluginSystem = z.infer<typeof PluginSystemSchema>;
