// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useTranslation } from 'react-i18next';
import { Space, Tag, Tooltip, Typography } from 'antd';

import type { PluginEntry } from '@/schemas/plugins';

type StatusSubject = Pick<PluginEntry, 'type' | 'enabled' | 'disabledReason' | 'runtime'>;

function tagColor(state: string, healthy: boolean): string {
  if (state === 'running') return healthy ? 'green' : 'orange';
  if (state === 'failed') return 'red';
  if (state === 'starting' || state === 'stopping') return 'blue';
  return 'default';
}

export default function PluginStatus({ plugin }: { plugin: StatusSubject }) {
  const { t } = useTranslation();
  if (plugin.type === 'ui') {
    return <Typography.Text type="secondary">{t('pages.plugins.uiOnly')}</Typography.Text>;
  }
  const runtime = plugin.runtime;
  const state = runtime?.state ?? 'stopped';
  const healthy = runtime?.healthy ?? false;
  const unhealthy = state === 'running' && !healthy;
  const hint = runtime?.healthError || runtime?.detail;
  const label = `${t(`pages.plugins.state.${state}`)}${unhealthy ? ` · ${t('pages.plugins.unhealthy')}` : ''}`;
  return (
    <Space direction="vertical" size={2}>
      <Tooltip title={hint}>
        <Tag color={tagColor(state, healthy)} style={{ marginInlineEnd: 0 }}>
          {label}
        </Tag>
      </Tooltip>
      {runtime && (runtime.eventsSent || runtime.eventsFailed || runtime.eventsDropped) ? (
        <Typography.Text type="secondary">
          {t('pages.plugins.eventsSummary', {
            sent: runtime.eventsSent ?? 0,
            failed: runtime.eventsFailed ?? 0,
            dropped: runtime.eventsDropped ?? 0,
          })}
        </Typography.Text>
      ) : null}
      {runtime?.lastError && (
        <Typography.Text type="warning">
          {t('pages.plugins.lastError', { error: runtime.lastError })}
        </Typography.Text>
      )}
      {!plugin.enabled && plugin.disabledReason && (
        <Typography.Text type="danger">
          {t('pages.plugins.disabledReason', { reason: plugin.disabledReason })}
        </Typography.Text>
      )}
    </Space>
  );
}
