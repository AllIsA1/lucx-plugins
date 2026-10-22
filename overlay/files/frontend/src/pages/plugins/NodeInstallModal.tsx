// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Descriptions, Modal, Select, Space } from 'antd';

import { pluginsApi, type PluginEntry } from '@/api/plugins';
import PermissionList from '@/pages/plugins/PermissionList';
import { isDangerousPermission, shortCommit } from '@/pages/plugins/permissions';

interface Props {
  nodeId: number;
  nodeName: string;
  candidates: PluginEntry[];
  onClose: () => void;
  onInstalled: () => void;
}

export default function NodeInstallModal({
  nodeId,
  nodeName,
  candidates,
  onClose,
  onInstalled,
}: Props) {
  const { t } = useTranslation();
  const [pickedId, setPickedId] = useState(candidates[0]?.id ?? '');
  const [accepted, setAccepted] = useState(false);
  const [installing, setInstalling] = useState(false);
  const picked = candidates.find((p) => p.id === pickedId);

  const install = async () => {
    if (!picked) return;
    setInstalling(true);
    try {
      const res = await pluginsApi.nodeInstall(nodeId, picked.id, picked.permissions);
      if (res.success) onInstalled();
    } finally {
      setInstalling(false);
    }
  };

  return (
    <Modal
      open
      width={640}
      title={t('pages.plugins.nodes.installTitle', { node: nodeName })}
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>{t('cancel')}</Button>
          <Button
            type="primary"
            loading={installing}
            disabled={!picked || !accepted}
            onClick={() => void install()}
          >
            {t('pages.plugins.install')}
          </Button>
        </Space>
      }
    >
      <Select
        style={{ width: '100%', marginBottom: 12 }}
        value={pickedId}
        aria-label={t('pages.plugins.nodes.pick')}
        options={candidates.map((p) => ({ value: p.id, label: `${p.name} (${p.id})` }))}
        onChange={(id) => {
          setPickedId(id);
          setAccepted(false);
        }}
      />
      {picked && (
        <>
          <Descriptions size="small" column={1} style={{ marginBottom: 12 }}>
            <Descriptions.Item label={t('pages.plugins.version')}>
              {picked.version}
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.plugins.source')}>
              {picked.source.owner}/{picked.source.repo} @ {shortCommit(picked.commit)}
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.plugins.permissions')}>
              <PermissionList permissions={picked.permissions} />
            </Descriptions.Item>
          </Descriptions>
          {picked.permissions.some(isDangerousPermission) && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 12 }}
              message={t('pages.plugins.dangerousWarning')}
            />
          )}
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 12 }}
            message={t('pages.plugins.nodes.installNote')}
          />
          <Checkbox checked={accepted} onChange={(e) => setAccepted(e.target.checked)}>
            {t('pages.plugins.nodes.accept', { node: nodeName })}
          </Checkbox>
        </>
      )}
    </Modal>
  );
}
