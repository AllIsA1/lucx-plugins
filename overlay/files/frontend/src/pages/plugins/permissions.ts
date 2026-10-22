// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

export type PermissionKind =
  | 'network'
  | 'apiRead'
  | 'apiWrite'
  | 'events'
  | 'pipeline'
  | 'uiMenu'
  | 'uiReplace'
  | 'unknown';

export function permissionKind(permission: string): PermissionKind {
  if (permission === 'network') return 'network';
  if (permission === 'ui:menu') return 'uiMenu';
  if (permission.startsWith('ui:replace:')) return 'uiReplace';
  if (permission.startsWith('events:')) return 'events';
  if (permission.startsWith('pipeline:')) return 'pipeline';
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
    permission.startsWith('api:settings:') ||
    permission.startsWith('api:nodes:')
  );
}

export function shortCommit(commit: string): string {
  return commit.slice(0, 7);
}

export function sourceUrl(source: { owner: string; repo: string; ref?: string }): string {
  const base = `https://github.com/${source.owner}/${source.repo}`;
  return source.ref ? `${base}/tree/${source.ref}` : base;
}
