// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Space, Tag, Typography } from 'antd';
import { CloudSyncOutlined, ReloadOutlined } from '@ant-design/icons';

import { coreUpdatesApi, isNever, type CoreUpdate, type CoreUpdates } from '@/api/coreUpdates';
import { getMessage } from '@/utils/messageBus';

const KEY = ['plugins', 'better-cores', 'cores'] as const;

// The plugin's view of the cores it manages, or undefined where the plugin is not there.
export function useCoreUpdates() {
  return useQuery({
    queryKey: KEY,
    queryFn: coreUpdatesApi.state,
    select: (res): CoreUpdates | undefined => (res.success ? (res.obj ?? undefined) : undefined),
    // quick while something is under way, slow otherwise; no retries for a plugin that is simply not installed
    refetchInterval: (query) => {
      const data = query.state.data;
      const working =
        data?.success && (data.obj?.checking || data.obj?.cores.some((c) => c.installing));
      return working ? 2000 : 60000;
    },
    retry: false,
  });
}

function when(iso: string | undefined, lang: string): string {
  if (isNever(iso)) return '';
  const d = new Date(iso as string);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(lang);
}

function day(iso: string | undefined, lang: string): string {
  if (isNever(iso)) return '';
  const d = new Date(iso as string);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(lang);
}

// The card at the top of the Cores page: when the cores were last looked at and the button to look now.
export function CoreUpdatesCard() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const { data } = useCoreUpdates();
  const [busy, setBusy] = useState(false);

  const check = useCallback(async () => {
    setBusy(true);
    try {
      const res = await coreUpdatesApi.check();
      if (res.success) {
        qc.setQueryData(KEY, res);
      } else {
        getMessage().error(res.msg || t('pages.settings.cores.updates.checkFailed'));
      }
    } finally {
      setBusy(false);
    }
  }, [qc, t]);

  if (!data) return null;
  const never = isNever(data.checkedAt);
  const available = data.cores.filter((c) => c.updateAvailable).length;

  return (
    <Card
      size="small"
      style={{ marginBottom: 12 }}
      title={
        <>
          <CloudSyncOutlined style={{ marginRight: 6 }} />
          {t('pages.settings.cores.updates.title')}
        </>
      }
      extra={
        <Space>
          {available > 0 && (
            <Tag color="orange">
              {t('pages.settings.cores.updates.available', { n: available })}
            </Tag>
          )}
          <Button
            icon={<ReloadOutlined />}
            loading={busy || data.checking}
            onClick={() => void check()}
          >
            {t('pages.settings.cores.updates.checkNow')}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary" style={{ marginBottom: 4 }}>
        {t('pages.settings.cores.updates.desc')}
      </Typography.Paragraph>
      <Typography.Text type="secondary">
        {never
          ? t('pages.settings.cores.updates.neverChecked')
          : t('pages.settings.cores.updates.lastChecked', {
              at: when(data.checkedAt, i18n.language),
            })}
        {!isNever(data.nextCheckAt) &&
          ` · ${t('pages.settings.cores.updates.nextCheck', { at: when(data.nextCheckAt, i18n.language) })}`}
      </Typography.Text>
    </Card>
  );
}

// The line inside the card of one core: its latest release, what the plugin installed, and the button.
export function CoreUpdateRow({ kind }: { kind: string }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const { data } = useCoreUpdates();
  const [busy, setBusy] = useState(false);
  const core: CoreUpdate | undefined = data?.cores.find((c) => c.id === kind);

  const install = useCallback(async () => {
    setBusy(true);
    try {
      const res = await coreUpdatesApi.install(kind);
      if (res.success) {
        qc.setQueryData(KEY, res);
      } else {
        getMessage().error(res.msg || t('pages.settings.cores.updates.installFailed'));
      }
    } finally {
      setBusy(false);
    }
  }, [kind, qc, t]);

  if (!core) return null;
  const working = busy || core.installing;
  const label = core.installed
    ? core.updateAvailable
      ? t('pages.settings.cores.updates.updateTo', { tag: core.latest?.tag })
      : t('pages.settings.cores.updates.reinstall')
    : t('pages.settings.cores.updates.installRelease', { tag: core.latest?.tag });

  return (
    <div style={{ marginBottom: 8 }}>
      <Space wrap size={[8, 4]}>
        <Typography.Text>
          {core.installed
            ? t('pages.settings.cores.updates.installedByPlugin', {
                tag: core.installed.tag,
                at: day(core.installed.at, i18n.language),
              })
            : t('pages.settings.cores.updates.panelCopy')}
        </Typography.Text>
        {core.latest && (
          <Typography.Text type="secondary">
            {t('pages.settings.cores.updates.latest', {
              tag: core.latest.tag,
              at: day(core.latest.publishedAt, i18n.language),
            })}
            {core.latest.url && (
              <>
                {' '}
                <a href={core.latest.url} target="_blank" rel="noreferrer">
                  {t('pages.settings.cores.updates.releaseNotes')}
                </a>
              </>
            )}
          </Typography.Text>
        )}
        {core.updateAvailable && (
          <Tag color="orange">{t('pages.settings.cores.updates.newer')}</Tag>
        )}
        {core.latest && (
          <Button
            size="small"
            type={core.updateAvailable || !core.installed ? 'primary' : 'default'}
            loading={working}
            disabled={working}
            onClick={() => void install()}
          >
            {label}
          </Button>
        )}
      </Space>
      {core.error && (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 6 }}
          message={core.error}
          closable={false}
        />
      )}
    </div>
  );
}
