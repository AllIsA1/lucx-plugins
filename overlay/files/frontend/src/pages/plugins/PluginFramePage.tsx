// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router';
import { Button, ConfigProvider, Layout, Result, Spin } from 'antd';

import { useTheme } from '@/hooks/useTheme';
import AppSidebar from '@/layouts/AppSidebar';
import PluginFrame from '@/pages/plugins/PluginFrame';
import { usePluginUi } from '@/pages/plugins/usePluginUi';
import './PluginFrame.css';

// The panel chrome (sidebar, theme) around whatever the plugin page or banner is.
export function PluginPageShell({ children }: { children: ReactNode }) {
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const pageClass = useMemo(() => {
    const classes = ['plugin-frame-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);
  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <div className="plugin-frame-body">{children}</div>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

// Only the pages a plugin declared (menu entries or the page it replaces) can be opened.
export default function PluginFramePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '', '*': rest = '' } = useParams();
  const { menu, replaces, isLoading } = usePluginUi();
  const path = `/${rest}`;
  const declared =
    menu.some((item) => item.pluginId === id && item.path === path) ||
    Object.values(replaces).some((r) => r.pluginId === id && r.path === path);
  const name = menu.find((item) => item.pluginId === id && item.path === path)?.title;

  return (
    <PluginPageShell>
      {isLoading ? (
        <Spin size="large" style={{ margin: 'auto' }} />
      ) : declared ? (
        <PluginFrame pluginId={id} path={path} title={name} />
      ) : (
        <Result
          status="404"
          title={t('pages.plugins.frame.unavailable')}
          subTitle={t('pages.plugins.frame.unavailableHint')}
          extra={
            <Button type="primary" onClick={() => navigate('/plugins')}>
              {t('pages.plugins.frame.back')}
            </Button>
          }
        />
      )}
    </PluginPageShell>
  );
}
