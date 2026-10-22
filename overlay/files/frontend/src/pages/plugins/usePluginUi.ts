// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

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
