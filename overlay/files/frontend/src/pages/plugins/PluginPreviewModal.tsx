// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Descriptions, Modal, Space, Typography } from 'antd';

import { pluginsApi, type PluginPreview } from '@/api/plugins';
import type { Isolation } from '@/schemas/plugins';
import PermissionList from '@/pages/plugins/PermissionList';
import { shortCommit, sourceUrl } from '@/pages/plugins/permissions';

interface Props {
  preview: PluginPreview;
  isolation?: Isolation | null;
  onClose: () => void;
  onInstalled: () => void;
}

export default function PluginPreviewModal({ preview, isolation, onClose, onInstalled }: Props) {
  const { t } = useTranslation();
  const [accepted, setAccepted] = useState(false);
  const [installing, setInstalling] = useState(false);
  const { manifest } = preview;
  const networkUnenforced =
    manifest.type !== 'ui' &&
    isolation?.network === false &&
    !manifest.permissions.includes('network');

  const install = async () => {
    setInstalling(true);
    try {
      const res = await pluginsApi.install(preview.token, manifest.permissions);
      if (res.success) onInstalled();
    } finally {
      setInstalling(false);
    }
  };

  return (
    <Modal
      open
      width={640}
      title={t('pages.plugins.previewTitle', { name: manifest.name })}
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>{t('cancel')}</Button>
          <Button
            type="primary"
            loading={installing}
            disabled={!accepted || preview.installed}
            onClick={() => void install()}
          >
            {t('pages.plugins.install')}
          </Button>
        </Space>
      }
    >
      {networkUnenforced && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('pages.plugins.isolation.previewNoNetwork')}
        />
      )}
      {preview.installed && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('pages.plugins.alreadyInstalled')}
        />
      )}
      {preview.catalog === 'unlisted' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('pages.plugins.catalog.unlisted')}
        />
      )}
      {preview.catalog === 'listed' && (
        <Alert
          type="success"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('pages.plugins.catalog.listed')}
        />
      )}
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label={t('pages.plugins.version')}>{manifest.version}</Descriptions.Item>
        {manifest.author && (
          <Descriptions.Item label={t('pages.plugins.author')}>{manifest.author}</Descriptions.Item>
        )}
        {manifest.license && (
          <Descriptions.Item label={t('pages.plugins.license')}>
            {manifest.license}
          </Descriptions.Item>
        )}
        <Descriptions.Item label={t('pages.plugins.kind')}>
          {t(`pages.plugins.type.${manifest.type}`)} · {t(`pages.plugins.scope.${manifest.scope}`)}
        </Descriptions.Item>
        <Descriptions.Item label={t('pages.plugins.source')}>
          <a href={sourceUrl(preview.source)} target="_blank" rel="noopener noreferrer">
            {preview.source.owner}/{preview.source.repo}
          </a>{' '}
          <Typography.Text code>{shortCommit(preview.commit)}</Typography.Text>
        </Descriptions.Item>
      </Descriptions>
      {manifest.description && (
        <Typography.Paragraph style={{ marginTop: 12 }}>
          {manifest.description}
        </Typography.Paragraph>
      )}

      <Typography.Title level={5} style={{ marginTop: 16 }}>
        {t('pages.plugins.permissions')}
      </Typography.Title>
      <PermissionList permissions={manifest.permissions} />
      {preview.dangerous.length > 0 && (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 16 }}
          message={t('pages.plugins.dangerousWarning')}
        />
      )}
      <Checkbox
        style={{ marginTop: 16 }}
        checked={accepted}
        onChange={(e) => setAccepted(e.target.checked)}
      >
        {t('pages.plugins.accept')}
      </Checkbox>
    </Modal>
  );
}
