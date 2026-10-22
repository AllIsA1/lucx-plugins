// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Modal, Space, Tag, Typography } from 'antd';

import { pluginsApi, type PluginEntry } from '@/api/plugins';
import type { NodePlugin } from '@/schemas/plugins';
import { isDangerousPermission, shortCommit } from '@/pages/plugins/permissions';

interface Props {
  nodeId: number;
  nodeName: string;
  onNode: NodePlugin;
  master: PluginEntry;
  onClose: () => void;
  onDone: () => void;
}

function Tags({
  title,
  items,
  color,
  strike,
}: {
  title: string;
  items: string[];
  color?: (p: string) => string | undefined;
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
            color={color?.(p)}
            style={strike ? { textDecoration: 'line-through' } : undefined}
          >
            {p}
          </Tag>
        ))}
      </div>
    </div>
  );
}

export default function NodeUpdateModal({
  nodeId,
  nodeName,
  onNode,
  master,
  onClose,
  onDone,
}: Props) {
  const { t } = useTranslation();
  const [accepted, setAccepted] = useState(false);
  const [busy, setBusy] = useState(false);

  const added = master.permissions.filter((p) => !onNode.permissions.includes(p));
  const removed = onNode.permissions.filter((p) => !master.permissions.includes(p));
  const kept = master.permissions.filter((p) => onNode.permissions.includes(p));

  const apply = async () => {
    setBusy(true);
    try {
      const res = await pluginsApi.nodeUpdate(nodeId, master.id, master.permissions);
      if (res.success) onDone();
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      width={600}
      title={t('pages.plugins.nodes.update.title', { name: master.name, node: nodeName })}
      onCancel={onClose}
      footer={
        <Space>
          <Button onClick={onClose}>{t('cancel')}</Button>
          <Button
            type="primary"
            loading={busy}
            disabled={added.length > 0 && !accepted}
            onClick={() => void apply()}
          >
            {t('pages.plugins.nodes.update.apply')}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph>
        <Typography.Text strong>
          {t('pages.plugins.nodes.update.versions', { from: onNode.version, to: master.version })}
        </Typography.Text>
        <br />
        <Typography.Text type="secondary" code>
          {shortCommit(onNode.commit)} → {shortCommit(master.commit)}
        </Typography.Text>
      </Typography.Paragraph>
      <Tags
        title={t('pages.plugins.update.added')}
        items={added}
        color={(p) => (isDangerousPermission(p) ? 'red' : 'orange')}
      />
      <Tags title={t('pages.plugins.update.removed')} items={removed} strike />
      <Tags title={t('pages.plugins.update.kept')} items={kept} />
      <Alert
        type="warning"
        showIcon
        style={{ margin: '12px 0' }}
        message={t('pages.plugins.nodes.update.note')}
      />
      {added.length > 0 && (
        <Checkbox checked={accepted} onChange={(e) => setAccepted(e.target.checked)}>
          {t('pages.plugins.nodes.update.accept', { node: nodeName })}
        </Checkbox>
      )}
    </Modal>
  );
}
