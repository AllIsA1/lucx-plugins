// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useQuery } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi } from '@/api/plugins';
import type { PluginUiMenuItem, PluginUiReplace } from '@/schemas/plugins';

const NONE_MENU: PluginUiMenuItem[] = [];
const NONE_REPLACES: Record<string, PluginUiReplace> = {};

// What enabled plugins add to the interface. A failed request means "nothing", never a broken panel.
export function usePluginUi() {
  const query = useQuery({
    queryKey: keys.plugins.ui(),
    queryFn: () => pluginsApi.ui(),
    staleTime: 30_000,
  });
  const ui = query.data?.success ? query.data.obj : null;
  return {
    menu: ui?.menu ?? NONE_MENU,
    replaces: ui?.replaces ?? NONE_REPLACES,
    isLoading: query.isLoading,
  };
}

// The sidebar and routes key plugin pages by this path.
export function pluginPagePath(pluginId: string, path: string): string {
  return `/plugins/ui/${pluginId}${path}`;
}
