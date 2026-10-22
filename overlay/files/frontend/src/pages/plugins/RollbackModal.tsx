// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from 'antd';

import { pluginsApi, type PluginEntry } from '@/api/plugins';
import { shortCommit } from '@/pages/plugins/permissions';

interface Props {
  plugin: PluginEntry;
  onClose: () => void;
  onDone: () => void;
}

export default function RollbackModal({ plugin, onClose, onDone }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const previous = plugin.previous;

  const rollback = async () => {
    setBusy(true);
    try {
      const res = await pluginsApi.rollback(plugin.id);
      if (res.success) onDone();
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      title={t('pages.plugins.rollback.title', { name: plugin.name })}
      okText={t('pages.plugins.rollback.confirm')}
      okButtonProps={{ danger: true, loading: busy }}
      cancelText={t('cancel')}
      onOk={() => void rollback()}
      onCancel={onClose}
    >
      {previous && (
        <p>
          {t('pages.plugins.rollback.body', {
            version: previous.version,
            commit: shortCommit(previous.commit),
          })}
        </p>
      )}
    </Modal>
  );
}
