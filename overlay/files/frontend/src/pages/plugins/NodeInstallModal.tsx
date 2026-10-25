// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Checkbox,
  Descriptions,
  Input,
  Modal,
  Segmented,
  Select,
  Space,
} from 'antd';

import { pluginsApi, type PluginEntry, type PluginPreview } from '@/api/plugins';
import PermissionList from '@/pages/plugins/PermissionList';
import { isDangerousPermission, shortCommit } from '@/pages/plugins/permissions';

type Mode = 'installed' | 'link';

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
  const [mode, setMode] = useState<Mode>(candidates.length > 0 ? 'installed' : 'link');
  const [pickedId, setPickedId] = useState(candidates[0]?.id ?? '');
  const [url, setUrl] = useState('');
  const [preview, setPreview] = useState<PluginPreview | null>(null);
  const [checking, setChecking] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const [installing, setInstalling] = useState(false);
  const picked = candidates.find((p) => p.id === pickedId);
  // A plugin made for the master panel alone cannot run on a node: the node would refuse it.
  const linkScopeOk = preview !== null && preview.manifest.scope !== 'master';
  const ready = mode === 'installed' ? !!picked : linkScopeOk;

  const switchMode = (next: Mode) => {
    setMode(next);
    setAccepted(false);
  };

  const check = async () => {
    const wanted = url.trim();
    if (!wanted) return;
    setChecking(true);
    setAccepted(false);
    setPreview(null);
    try {
      const res = await pluginsApi.preview(wanted);
      if (res.success && res.obj) setPreview(res.obj);
    } finally {
      setChecking(false);
    }
  };

  const install = async () => {
    setInstalling(true);
    try {
      let res;
      if (mode === 'installed') {
        if (!picked) return;
        res = await pluginsApi.nodeInstall(nodeId, picked.id, picked.permissions);
      } else {
        if (!preview) return;
        res = await pluginsApi.nodeInstallLink(nodeId, preview.token, preview.manifest.permissions);
      }
      if (res.success) onInstalled();
    } finally {
      setInstalling(false);
    }
  };

  const permissions =
    mode === 'installed' ? (picked?.permissions ?? []) : (preview?.manifest.permissions ?? []);

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
            disabled={!ready || !accepted}
            onClick={() => void install()}
          >
            {t('pages.plugins.install')}
          </Button>
        </Space>
      }
    >
      <Segmented<Mode>
        block
        style={{ marginBottom: 12 }}
        value={mode}
        onChange={switchMode}
        options={[
          {
            value: 'installed',
            label: t('pages.plugins.nodes.fromPanel'),
            disabled: candidates.length === 0,
          },
          { value: 'link', label: t('pages.plugins.nodes.fromLink') },
        ]}
      />
      {mode === 'installed' ? (
        <Select
          style={{ width: '100%', marginBottom: 12 }}
          value={pickedId}
          aria-label={t('pages.plugins.nodes.pick')}
          options={candidates.map((p) => ({
            value: p.id,
            label: `${p.name} (${p.id})`,
          }))}
          onChange={(id) => {
            setPickedId(id);
            setAccepted(false);
          }}
        />
      ) : (
        <Space.Compact style={{ width: '100%', marginBottom: 12 }}>
          <Input
            value={url}
            placeholder="https://github.com/owner/repo"
            aria-label={t('pages.plugins.nodes.linkLabel')}
            onChange={(e) => {
              setUrl(e.target.value);
              setPreview(null);
              setAccepted(false);
            }}
            onPressEnter={() => void check()}
          />
          <Button loading={checking} disabled={url.trim() === ''} onClick={() => void check()}>
            {t('pages.plugins.check')}
          </Button>
        </Space.Compact>
      )}
      {mode === 'link' && preview && !linkScopeOk && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.nodes.masterOnly')}
        />
      )}
      {ready && (
        <>
          <Descriptions size="small" column={1} style={{ marginBottom: 12 }}>
            <Descriptions.Item label={t('pages.plugins.version')}>
              {mode === 'installed'
                ? picked?.version
                : `${preview?.manifest.name} ${preview?.manifest.version}`}
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.plugins.source')}>
              {mode === 'installed' && picked
                ? `${picked.source.owner}/${picked.source.repo} @ ${shortCommit(picked.commit)}`
                : preview
                  ? `${preview.source.owner}/${preview.source.repo} @ ${shortCommit(preview.commit)}`
                  : ''}
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.plugins.permissions')}>
              <PermissionList permissions={permissions} />
            </Descriptions.Item>
          </Descriptions>
          {permissions.some(isDangerousPermission) && (
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
            message={
              mode === 'installed'
                ? t('pages.plugins.nodes.installNote')
                : t('pages.plugins.nodes.linkNote')
            }
          />
          <Checkbox checked={accepted} onChange={(e) => setAccepted(e.target.checked)}>
            {t('pages.plugins.nodes.accept', { node: nodeName })}
          </Checkbox>
        </>
      )}
    </Modal>
  );
}
