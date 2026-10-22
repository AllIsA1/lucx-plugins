import { lazy, Suspense } from 'react';
import { createBrowserRouter, useRouteError, type RouteObject } from 'react-router';
import { Spin } from 'antd';

import PanelLayout from '@/layouts/PanelLayout';
import { reloadOnceOnStaleChunk } from '@/lib/stale-chunk';
// LUCX-HOOK: plugin pages replacing built-in ones
import ReplaceablePage from '@/pages/plugins/ReplaceablePage';
// END LUCX-HOOK

const IndexPage = lazy(() => import('@/pages/index/IndexPage'));
const InboundsPage = lazy(() => import('@/pages/inbounds/InboundsPage'));
const ClientsPage = lazy(() => import('@/pages/clients/ClientsPage'));
const GroupsPage = lazy(() => import('@/pages/groups/GroupsPage'));
const NodesPage = lazy(() => import('@/pages/nodes/NodesPage'));
const HostsPage = lazy(() => import('@/pages/hosts/HostsPage'));
const SettingsPage = lazy(() => import('@/pages/settings/SettingsPage'));
const XrayPage = lazy(() => import('@/pages/xray/XrayPage'));
const ApiDocsPage = lazy(() => import('@/pages/api-docs/ApiDocsPage'));
const SponsorsPage = lazy(() => import('@/pages/sponsors/SponsorsPage'));
// LUCX-HOOK: tunnel sidecars (NaiveProxy) page
const TunnelsPage = lazy(() => import('@/pages/tunnels/TunnelsPage'));
const MaskingPage = lazy(() => import('@/pages/masking/MaskingPage'));
// END LUCX-HOOK
// LUCX-HOOK: plugin system page
const PluginsPage = lazy(() => import('@/pages/plugins/PluginsPage'));
const PluginFramePage = lazy(() => import('@/pages/plugins/PluginFramePage'));
// END LUCX-HOOK
// LUCX-HOOK: built-in pages a plugin may replace (the catalog lives in internal/lucx/plugins/ui_catalog.go)
function replaceable(route: string, page: React.ReactNode) {
  return withSuspense(<ReplaceablePage route={route}>{page}</ReplaceablePage>);
}
// END LUCX-HOOK

function withSuspense(node: React.ReactNode) {
  return (
    <Suspense
      fallback={
        <div
          style={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            minHeight: '60vh',
          }}
        >
          <Spin size="large" />
        </div>
      }
    >
      {node}
    </Suspense>
  );
}

function StaleAssetReload() {
  const error = useRouteError();
  if (reloadOnceOnStaleChunk(error)) return null;
  const msg = error instanceof Error ? error.message : String(error ?? '');
  return (
    <div style={{ padding: 48, textAlign: 'center' }}>{msg || 'Unexpected Application Error'}</div>
  );
}

const routes: RouteObject[] = [
  {
    path: '/',
    element: <PanelLayout />,
    errorElement: <StaleAssetReload />,
    children: [
      { index: true, element: replaceable('/', <IndexPage />) }, // LUCX-HOOK: replaceable
      { path: 'inbounds', element: replaceable('/inbounds', <InboundsPage />) }, // LUCX-HOOK: replaceable
      { path: 'clients', element: replaceable('/clients', <ClientsPage />) }, // LUCX-HOOK: replaceable
      { path: 'groups', element: replaceable('/groups', <GroupsPage />) }, // LUCX-HOOK: replaceable
      { path: 'nodes', element: replaceable('/nodes', <NodesPage />) }, // LUCX-HOOK: replaceable
      { path: 'hosts', element: replaceable('/hosts', <HostsPage />) }, // LUCX-HOOK: replaceable
      /* LUCX-HOOK: tunnel sidecars (NaiveProxy) */
      { path: 'tunnels', element: withSuspense(<TunnelsPage />) },
      { path: 'masking', element: withSuspense(<MaskingPage />) },
      /* END LUCX-HOOK */
      /* LUCX-HOOK: plugin system */
      { path: 'plugins', element: withSuspense(<PluginsPage />) },
      { path: 'plugins/ui/:id/*', element: withSuspense(<PluginFramePage />) },
      /* END LUCX-HOOK */
      { path: 'settings', element: withSuspense(<SettingsPage />) },
      { path: 'xray', element: withSuspense(<XrayPage />) },
      { path: 'outbound', element: withSuspense(<XrayPage />) },
      { path: 'routing', element: withSuspense(<XrayPage />) },
      { path: 'api-docs', element: withSuspense(<ApiDocsPage />) },
      { path: 'sponsors', element: withSuspense(<SponsorsPage />) },
    ],
  },
];

function computeBasename() {
  const raw = (typeof window !== 'undefined' && window.X_UI_BASE_PATH) || '/';
  const trimmed = raw.replace(/\/+$/, '');
  return `${trimmed}/panel`;
}

export const router = createBrowserRouter(routes, {
  basename: computeBasename(),
});
