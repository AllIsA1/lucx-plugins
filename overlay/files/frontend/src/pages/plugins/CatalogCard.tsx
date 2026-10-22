// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Input, Space, Spin, Tag, Tooltip, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi } from '@/api/plugins';
import type { CatalogPlugin, PluginEntry, PluginPreview } from '@/schemas/plugins';
import { pluginIcon } from '@/pages/plugins/pluginIcons';
import { isDangerousPermission, shortCommit } from '@/pages/plugins/permissions';

interface Props {
  available: boolean;
  installed: PluginEntry[];
  onInstall: (preview: PluginPreview) => void;
  onUpdate: (plugin: PluginEntry) => void;
}

export default function CatalogCard({ available, installed, onInstall, onUpdate }: Props) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [filter, setFilter] = useState('');
  const [busy, setBusy] = useState('');
  const [base, setBase] = useState<string | null>(null);

  const query = useQuery({
    queryKey: keys.plugins.catalog(),
    queryFn: () => pluginsApi.catalog(),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });
  const view = query.data?.success ? query.data.obj : null;
  const plugins = view?.plugins ?? [];
  const shown = plugins.filter((p) =>
    `${p.name} ${p.id} ${p.summary}`.toLowerCase().includes(filter.trim().toLowerCase()),
  );
  const updates = plugins.filter((p) => p.status === 'update-available').length;

  const refresh = async () => {
    setBusy('refresh');
    try {
      const res = await pluginsApi.catalogRefresh();
      if (res.success) queryClient.setQueryData(keys.plugins.catalog(), res);
    } finally {
      setBusy('');
    }
  };

  const install = async (p: CatalogPlugin) => {
    setBusy(p.id);
    try {
      const res = await pluginsApi.catalogPreview(p.id);
      if (res.success && res.obj) onInstall(res.obj);
    } finally {
      setBusy('');
    }
  };

  const saveBase = async (next: string) => {
    setBusy('base');
    try {
      const res = await pluginsApi.catalogSetBase(next);
      if (res.success) {
        setBase(null);
        await queryClient.invalidateQueries({ queryKey: keys.plugins.catalog() });
      }
    } finally {
      setBusy('');
    }
  };

  const action = (p: CatalogPlugin) => {
    switch (p.status) {
      case 'installed':
        return <Tag color="green">{t('pages.plugins.catalog.installed')}</Tag>;
      case 'id-conflict':
        return (
          <Typography.Text type="warning">{t('pages.plugins.catalog.idConflict')}</Typography.Text>
        );
      case 'update-available': {
        const have = installed.find((e) => e.id === p.id);
        return (
          <Button
            type="primary"
            disabled={!available || !have}
            onClick={() => have && onUpdate(have)}
          >
            {t('pages.plugins.catalog.update')}
          </Button>
        );
      }
      default:
        return (
          <Button
            type="primary"
            loading={busy === p.id}
            disabled={!available}
            onClick={() => void install(p)}
          >
            {t('pages.plugins.catalog.install')}
          </Button>
        );
    }
  };

  const customBase = base ?? view?.customBase ?? '';
  return (
    <Card
      title={
        <Space>
          {t('pages.plugins.catalog.title')}
          {updates > 0 && (
            <Tag color="blue">{t('pages.plugins.catalog.updates', { count: updates })}</Tag>
          )}
        </Space>
      }
      extra={
        <Button
          icon={<ReloadOutlined />}
          loading={busy === 'refresh'}
          onClick={() => void refresh()}
        >
          {t('pages.plugins.catalog.refresh')}
        </Button>
      }
      style={{ marginTop: 16 }}
    >
      <Typography.Paragraph type="secondary">
        {t('pages.plugins.catalog.desc')}
      </Typography.Paragraph>
      {view?.state === 'unconfigured' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.catalog.unconfigured')}
        />
      )}
      {view?.state === 'not-published' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.catalog.notPublished')}
        />
      )}
      {view?.state === 'error' && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.catalog.error', { error: view.error ?? '' })}
        />
      )}
      {view?.state === 'stale' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.catalog.stale', { error: view.error ?? '' })}
        />
      )}
      {query.data && !query.data.success && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.catalog.error', { error: query.data.msg })}
        />
      )}
      <Spin spinning={query.isLoading}>
        {plugins.length > 0 && (
          <Input
            allowClear
            style={{ marginBottom: 12, maxWidth: 360 }}
            value={filter}
            placeholder={t('pages.plugins.catalog.search')}
            onChange={(e) => setFilter(e.target.value)}
          />
        )}
        {view && view.state === 'ok' && plugins.length === 0 && (
          <Typography.Text type="secondary">{t('pages.plugins.catalog.empty')}</Typography.Text>
        )}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
            gap: 12,
          }}
        >
          {shown.map((p) => {
            const Icon = pluginIcon(p.icon ?? '');
            return (
              <Card
                key={p.id}
                size="small"
                title={
                  <Space>
                    <Icon />
                    {p.name}
                  </Space>
                }
                extra={<Typography.Text type="secondary">{p.version}</Typography.Text>}
              >
                <Space direction="vertical" size={6} style={{ width: '100%' }}>
                  <Space size={4} wrap>
                    {p.verified && (
                      <Tooltip
                        title={t('pages.plugins.catalog.verifiedBy', {
                          by: p.verified.by,
                          at: p.verified.at,
                        })}
                      >
                        <Tag color="green">{t('pages.plugins.catalog.verified')}</Tag>
                      </Tooltip>
                    )}
                    <Tag>{t(`pages.plugins.type.${p.type}`)}</Tag>
                    <Tag>{t(`pages.plugins.scope.${p.scope}`)}</Tag>
                  </Space>
                  <Typography.Text>{p.summary}</Typography.Text>
                  <Typography.Text type="secondary">
                    {[p.author, p.license].filter(Boolean).join(' · ')}{' '}
                    <Typography.Text code type="secondary">
                      {shortCommit(p.commit)}
                    </Typography.Text>
                  </Typography.Text>
                  <div>
                    {p.permissions.map((perm) => (
                      <Tag key={perm} color={isDangerousPermission(perm) ? 'red' : 'default'}>
                        {perm}
                      </Tag>
                    ))}
                  </div>
                  <div>{action(p)}</div>
                </Space>
              </Card>
            );
          })}
        </div>
      </Spin>
      <Typography.Title level={5} style={{ marginTop: 16 }}>
        {t('pages.plugins.catalog.sourceTitle')}
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.plugins.catalog.sourceDesc')}
      </Typography.Paragraph>
      <Space.Compact style={{ width: '100%' }}>
        <Input
          value={customBase}
          placeholder={view?.base ?? ''}
          onChange={(e) => setBase(e.target.value)}
        />
        <Button type="primary" loading={busy === 'base'} onClick={() => void saveBase(customBase)}>
          {t('pages.plugins.catalog.sourceSave')}
        </Button>
      </Space.Compact>
    </Card>
  );
}
