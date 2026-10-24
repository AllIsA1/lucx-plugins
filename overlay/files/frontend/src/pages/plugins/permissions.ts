// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import type { PluginSeam } from '@/schemas/plugins';

export type PermissionKind =
  | 'network'
  | 'apiRead'
  | 'apiWrite'
  | 'events'
  | 'pipeline'
  | 'uiMenu'
  | 'uiReplace'
  | 'seam'
  | 'unknown';

export function permissionKind(permission: string): PermissionKind {
  if (permission === 'network') return 'network';
  if (permission === 'ui:menu') return 'uiMenu';
  if (permission.startsWith('ui:replace:')) return 'uiReplace';
  if (permission.startsWith('events:')) return 'events';
  if (permission.startsWith('pipeline:')) return 'pipeline';
  if (permission.startsWith('seam:')) return 'seam';
  if (permission.startsWith('api:')) return permission.endsWith(':write') ? 'apiWrite' : 'apiRead';
  return 'unknown';
}

// Mirrors IsDangerousPermission in internal/lucx/plugins/permissions.go.
export function isDangerousPermission(permission: string): boolean {
  const kind = permissionKind(permission);
  return (
    kind === 'network' ||
    kind === 'apiWrite' ||
    kind === 'pipeline' ||
    kind === 'uiReplace' ||
    kind === 'seam' ||
    permission.startsWith('api:settings:') ||
    permission.startsWith('api:nodes:')
  );
}

// Mirrors Seam.RequiredPermission in internal/plugins/seam.go.
export function seamPermission(seam: PluginSeam): string {
  if (seam.engine === 'sub') return 'seam:sub';
  if (seam.route === '*') return 'seam:api:*';
  return `seam:api:${seam.route.replace(/^\//, '').split('/')[0]}`;
}

// A seam only takes effect when the plugin holds its permission.
export function activeSeams(plugin: {
  permissions?: string[] | null;
  seams?: PluginSeam[] | null;
}): PluginSeam[] {
  const held = plugin.permissions ?? [];
  return (plugin.seams ?? []).filter((seam) => held.includes(seamPermission(seam)));
}

export function shortCommit(commit: string): string {
  return commit.slice(0, 7);
}

export function sourceUrl(source: { owner: string; repo: string; ref?: string }): string {
  const base = `https://github.com/${source.owner}/${source.repo}`;
  return source.ref ? `${base}/tree/${source.ref}` : base;
}
