// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import type { NodePlugin, NodePluginStatus, PluginEntry } from '@/schemas/plugins';

export type Drift = 'same' | 'differs' | 'unknown';

// A node plugin the master has no record of (installed by hand on the node) has nothing to compare with.
export function pluginDrift(master: PluginEntry[], onNode: NodePlugin): Drift {
  const own = master.find((p) => p.id === onNode.id);
  if (!own) return 'unknown';
  return own.commit === onNode.commit ? 'same' : 'differs';
}

export function canManageNode(status: NodePluginStatus): boolean {
  return (
    status.state === 'ok' &&
    status.plugins?.available === true &&
    status.plugins.allowMasterManagement
  );
}

// A master-only plugin can never run on a node; one already there is updated by reinstalling, not by installing again.
export function installCandidates(master: PluginEntry[], status: NodePluginStatus): PluginEntry[] {
  const present = new Set((status.plugins?.plugins ?? []).map((p) => p.id));
  return master.filter((p) => p.scope !== 'master' && !present.has(p.id));
}
