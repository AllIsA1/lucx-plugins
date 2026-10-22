// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { z } from 'zod';

import { HttpUtil, Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import {
  CatalogViewSchema,
  NodePluginStatusSchema,
  PluginEntrySchema,
  PluginPreviewSchema,
  PluginFrameTokenSchema,
  PluginsStateSchema,
  PluginUiSchema,
  UpdatePreviewSchema,
  type CatalogView,
  type NodePluginStatus,
  type PluginEntry,
  type PluginPreview,
  type PluginFrameToken,
  type PluginsState,
  type PluginUi,
  type UpdatePreview,
} from '@/schemas/plugins';

export type { NodePluginStatus, PluginEntry, PluginPreview, PluginsState, PluginUi };
export type { CatalogView, PluginFrameToken, UpdatePreview };

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };
const BASE = '/panel/api/plugins';

export const pluginsApi = {
  state: async (): Promise<Msg<PluginsState>> => {
    const raw = await HttpUtil.get<PluginsState>(`${BASE}/list`, undefined, { silent: true });
    return parseMsg(raw, PluginsStateSchema, 'plugins/list');
  },
  ui: async (): Promise<Msg<PluginUi>> => {
    const raw = await HttpUtil.get<PluginUi>(`${BASE}/menu`, undefined, { silent: true });
    return parseMsg(raw, PluginUiSchema, 'plugins/menu');
  },
  updatePreview: async (id: string, ref?: string): Promise<Msg<UpdatePreview>> => {
    const raw = await HttpUtil.post<UpdatePreview>(
      `${BASE}/update-preview/${encodeURIComponent(id)}`,
      ref ? { ref } : {},
      JSON_HEADERS,
    );
    return parseMsg(raw, UpdatePreviewSchema, 'plugins/update-preview');
  },
  update: async (id: string, token: string, permissions: string[]): Promise<Msg<PluginEntry>> => {
    const raw = await HttpUtil.post<PluginEntry>(
      `${BASE}/update/${encodeURIComponent(id)}`,
      { token, permissions },
      JSON_HEADERS,
    );
    return parseMsg(raw, PluginEntrySchema, 'plugins/update');
  },
  rollback: async (id: string): Promise<Msg<PluginEntry>> => {
    const raw = await HttpUtil.post<PluginEntry>(
      `${BASE}/rollback/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    );
    return parseMsg(raw, PluginEntrySchema, 'plugins/rollback');
  },
  catalog: async (): Promise<Msg<CatalogView>> => {
    const raw = await HttpUtil.get<CatalogView>(`${BASE}/catalog`, undefined, { silent: true });
    return parseMsg(raw, CatalogViewSchema, 'plugins/catalog');
  },
  catalogRefresh: async (): Promise<Msg<CatalogView>> => {
    const raw = await HttpUtil.post<CatalogView>(`${BASE}/catalog/refresh`, {}, JSON_HEADERS);
    return parseMsg(raw, CatalogViewSchema, 'plugins/catalog/refresh');
  },
  catalogPreview: async (id: string): Promise<Msg<PluginPreview>> => {
    const raw = await HttpUtil.post<PluginPreview>(
      `${BASE}/catalog/preview/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    );
    return parseMsg(raw, PluginPreviewSchema, 'plugins/catalog/preview');
  },
  catalogUpdatePreview: async (id: string): Promise<Msg<UpdatePreview>> => {
    const raw = await HttpUtil.post<UpdatePreview>(
      `${BASE}/catalog/update-preview/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    );
    return parseMsg(raw, UpdatePreviewSchema, 'plugins/catalog/update-preview');
  },
  catalogSetBase: (base: string): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/catalog/settings`, { base }, JSON_HEADERS),
  frameToken: async (id: string): Promise<Msg<PluginFrameToken>> => {
    const raw = await HttpUtil.get<PluginFrameToken>(
      `${BASE}/frame-token/${encodeURIComponent(id)}`,
      undefined,
      { silent: true },
    );
    return parseMsg(raw, PluginFrameTokenSchema, 'plugins/frame-token');
  },
  preview: async (url: string): Promise<Msg<PluginPreview>> => {
    const raw = await HttpUtil.post<PluginPreview>(`${BASE}/preview`, { url }, JSON_HEADERS);
    return parseMsg(raw, PluginPreviewSchema, 'plugins/preview');
  },
  install: async (token: string, permissions: string[]): Promise<Msg<PluginEntry>> => {
    const raw = await HttpUtil.post<PluginEntry>(
      `${BASE}/install`,
      { token, permissions },
      JSON_HEADERS,
    );
    return parseMsg(raw, PluginEntrySchema, 'plugins/install');
  },
  setEnabled: (id: string, enabled: boolean): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/${enabled ? 'enable' : 'disable'}/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    ),
  restart: (id: string): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/restart/${encodeURIComponent(id)}`, {}, JSON_HEADERS),
  logs: async (id: string, lines = 200): Promise<Msg<string[]>> => {
    const raw = await HttpUtil.get<string[]>(
      `${BASE}/logs/${encodeURIComponent(id)}?lines=${lines}`,
      undefined,
      { silent: true },
    );
    return parseMsg(raw, z.array(z.string()), 'plugins/logs');
  },
  uninstall: (id: string, removeData: boolean): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/uninstall/${encodeURIComponent(id)}`,
      { removeData },
      JSON_HEADERS,
    ),
  setMasterManagement: (allow: boolean): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/master-management`, { allow }, JSON_HEADERS),
  saveArchiveBase: (archiveBase: string): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/settings`, { archiveBase }, JSON_HEADERS),
  nodes: async (): Promise<Msg<NodePluginStatus[]>> => {
    const raw = await HttpUtil.get<NodePluginStatus[]>(`${BASE}/nodes`, undefined, {
      silent: true,
    });
    return parseMsg(raw, z.array(NodePluginStatusSchema), 'plugins/nodes');
  },
  nodeRefresh: (nodeId: number): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/nodes/${nodeId}/refresh`, {}, JSON_HEADERS),
  nodeInstall: (nodeId: number, id: string, permissions: string[]): Promise<Msg<null>> =>
    HttpUtil.post<null>(`${BASE}/nodes/${nodeId}/install`, { id, permissions }, JSON_HEADERS),
  nodeSetEnabled: (nodeId: number, id: string, enabled: boolean): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/nodes/${nodeId}/${enabled ? 'enable' : 'disable'}/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    ),
  nodeUpdate: (nodeId: number, id: string, permissions: string[]): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/nodes/${nodeId}/update/${encodeURIComponent(id)}`,
      { permissions },
      JSON_HEADERS,
    ),
  nodeRestart: (nodeId: number, id: string): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/nodes/${nodeId}/restart/${encodeURIComponent(id)}`,
      {},
      JSON_HEADERS,
    ),
  nodeUninstall: (nodeId: number, id: string, removeData: boolean): Promise<Msg<null>> =>
    HttpUtil.post<null>(
      `${BASE}/nodes/${nodeId}/uninstall/${encodeURIComponent(id)}`,
      { removeData },
      JSON_HEADERS,
    ),
  nodeLogs: async (nodeId: number, id: string, lines = 200): Promise<Msg<string[]>> => {
    const raw = await HttpUtil.get<string[]>(
      `${BASE}/nodes/${nodeId}/logs/${encodeURIComponent(id)}?lines=${lines}`,
      undefined,
      { silent: true },
    );
    return parseMsg(raw, z.array(z.string()), 'plugins/nodes/logs');
  },
};
