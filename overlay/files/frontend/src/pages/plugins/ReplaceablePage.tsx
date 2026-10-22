// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { lazy, Suspense, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import { Alert, Button, Spin } from 'antd';

import PluginFrame from '@/pages/plugins/PluginFrame';
import { usePluginUi } from '@/pages/plugins/usePluginUi';

const PluginPageShell = lazy(() =>
  import('@/pages/plugins/PluginFramePage').then((m) => ({ default: m.PluginPageShell })),
);

interface Props {
  route: string;
  children: ReactNode;
}

// Shows the plugin page that replaces a built-in route; ?original=1 always opens the built-in one.
export default function ReplaceablePage({ route, children }: Props) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const { replaces, isLoading } = usePluginUi();
  if (params.get('original') === '1') return <>{children}</>;
  if (isLoading) return <Spin size="large" style={{ display: 'block', margin: '30vh auto' }} />;
  const replacement = replaces[route];
  if (!replacement) return <>{children}</>;

  return (
    <Suspense fallback={<Spin size="large" style={{ display: 'block', margin: '30vh auto' }} />}>
      <PluginPageShell>
        <Alert
          type="info"
          showIcon
          message={t('pages.plugins.frame.replacedBanner', { name: replacement.pluginName })}
          action={
            <Button size="small" onClick={() => setParams({ original: '1' })}>
              {t('pages.plugins.frame.showOriginal')}
            </Button>
          }
        />
        <PluginFrame
          pluginId={replacement.pluginId}
          path={replacement.path}
          title={replacement.pluginName}
        />
      </PluginPageShell>
    </Suspense>
  );
}
