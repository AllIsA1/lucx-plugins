// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

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
