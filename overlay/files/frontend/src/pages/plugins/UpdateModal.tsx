// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Checkbox, Input, Modal, Space, Spin, Tag, Typography } from 'antd';

import { pluginsApi, type PluginEntry } from '@/api/plugins';
import { keys } from '@/api/queryKeys';
import { shortCommit } from '@/pages/plugins/permissions';

interface Props {
  plugin: PluginEntry;
  // Check the commit the signed catalog names instead of a ref the operator types.
  fromCatalog?: boolean;
  onClose: () => void;
  onUpdated: () => void;
}

function Permissions({
  title,
  items,
  color,
  strike,
}: {
  title: string;
  items: string[];
  color?: string;
  strike?: boolean;
}) {
  if (items.length === 0) return null;
  return (
    <div style={{ marginBottom: 8 }}>
      <Typography.Text type="secondary">{title}</Typography.Text>
      <div>
        {items.map((p) => (
          <Tag
            key={p}
            color={color}
            style={strike ? { textDecoration: 'line-through' } : undefined}
          >
            {p}
          </Tag>
        ))}
      </div>
    </div>
  );
}

export default function UpdateModal({ plugin, fromCatalog = false, onClose, onUpdated }: Props) {
  const { t } = useTranslation();
  const [ref, setRef] = useState('');
  const [activeRef, setActiveRef] = useState<string | undefined>(undefined);
  const [acceptedToken, setAcceptedToken] = useState('');
  const [applying, setApplying] = useState(false);

  // A check downloads the update, so it runs once per ref and again only on request.
  const query = useQuery({
    queryKey: keys.plugins.updatePreview(plugin.id, fromCatalog ? 'catalog' : (activeRef ?? '')),
    queryFn: () =>
      fromCatalog
        ? pluginsApi.catalogUpdatePreview(plugin.id)
        : pluginsApi.updatePreview(plugin.id, activeRef),
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const preview = query.data?.success ? (query.data.obj ?? null) : null;
  const error = query.data && !query.data.success ? query.data.msg : '';
  const accepted = preview !== null && acceptedToken === preview.token;

  const check = () => {
    const wanted = ref.trim() || undefined;
    if (wanted === activeRef) void query.refetch();
    else setActiveRef(wanted);
  };

  const apply = async () => {
    if (!preview) return;
    setApplying(true);
    try {
      const res = await pluginsApi.update(plugin.id, preview.token, preview.manifest.permissions);
      if (res.success) onUpdated();
    } finally {
      setApplying(false);
    }
  };

  const canApply =
    preview !== null && !preview.upToDate && (!preview.needsConfirmation || accepted);

  return (
    <Modal
      open
      width={640}
      title={t('pages.plugins.update.title', { name: plugin.name })}
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>{t('cancel')}</Button>
          {preview && !preview.upToDate && (
            <Button
              type="primary"
              loading={applying}
              disabled={!canApply}
              onClick={() => void apply()}
            >
              {t('pages.plugins.update.apply')}
            </Button>
          )}
        </Space>
      }
    >
      {!fromCatalog && (
        <Space.Compact style={{ width: '100%', marginBottom: 12 }}>
          <Input
            value={ref}
            placeholder={t('pages.plugins.update.refPlaceholder')}
            onChange={(e) => setRef(e.target.value)}
            onPressEnter={check}
          />
          <Button loading={query.isFetching} onClick={check}>
            {t('pages.plugins.update.check')}
          </Button>
        </Space.Compact>
      )}
      <Spin spinning={query.isFetching}>
        {error && <Alert type="error" showIcon message={error} />}
        {preview?.upToDate && (
          <Alert
            type="success"
            showIcon
            message={t('pages.plugins.update.upToDate', {
              name: plugin.name,
              version: preview.current.version,
            })}
          />
        )}
        {preview && !preview.upToDate && (
          <>
            <Typography.Paragraph>
              <Typography.Text strong>
                {t('pages.plugins.update.versionLine', {
                  from: preview.current.version,
                  to: preview.manifest.version,
                })}
              </Typography.Text>
              <br />
              <Typography.Text type="secondary" code>
                {shortCommit(preview.current.commit)} → {shortCommit(preview.commit)}
              </Typography.Text>
            </Typography.Paragraph>
            {preview.typeChanged && (
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 8 }}
                message={t('pages.plugins.update.typeChanged', {
                  from: plugin.type,
                  to: preview.manifest.type,
                })}
              />
            )}
            {preview.scopeChanged && (
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 8 }}
                message={t('pages.plugins.update.scopeChanged', {
                  from: plugin.scope,
                  to: preview.manifest.scope,
                })}
              />
            )}
            <Permissions
              title={t('pages.plugins.update.added')}
              items={preview.diff.added}
              color="red"
            />
            <Permissions
              title={t('pages.plugins.update.removed')}
              items={preview.diff.removed}
              strike
            />
            <Permissions title={t('pages.plugins.update.kept')} items={preview.diff.kept} />
            {preview.diff.dangerousAdded.length > 0 && (
              <Alert
                type="warning"
                showIcon
                style={{ margin: '8px 0' }}
                message={t('pages.plugins.dangerousWarning')}
              />
            )}
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 12 }}
              message={t('pages.plugins.update.restartNote')}
            />
            {preview.needsConfirmation && (
              <Checkbox
                checked={accepted}
                onChange={(e) => setAcceptedToken(e.target.checked ? preview.token : '')}
              >
                {t('pages.plugins.update.accept')}
              </Checkbox>
            )}
          </>
        )}
      </Spin>
    </Modal>
  );
}
