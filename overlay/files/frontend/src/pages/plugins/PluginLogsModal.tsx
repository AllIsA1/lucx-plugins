// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useTranslation } from 'react-i18next';
import { Alert, Modal, Spin, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi } from '@/api/plugins';

interface Props {
  id: string;
  name: string;
  nodeId?: number;
  onClose: () => void;
}

export default function PluginLogsModal({ id, name, nodeId, onClose }: Props) {
  const { t } = useTranslation();
  const logs = useQuery({
    queryKey: nodeId === undefined ? keys.plugins.logs(id) : keys.plugins.nodeLogs(nodeId, id),
    queryFn: () => (nodeId === undefined ? pluginsApi.logs(id) : pluginsApi.nodeLogs(nodeId, id)),
    refetchInterval: 3000,
  });
  const failure = logs.data && !logs.data.success ? logs.data.msg : '';
  const lines = logs.data?.success ? (logs.data.obj ?? []) : [];

  return (
    <Modal
      open
      width={760}
      title={t('pages.plugins.logsTitle', { name })}
      footer={null}
      onCancel={onClose}
    >
      <Spin spinning={logs.isLoading}>
        {failure && <Alert type="error" showIcon message={failure} />}
        {!failure && lines.length === 0 && !logs.isLoading && (
          <Typography.Text type="secondary">{t('pages.plugins.noLogs')}</Typography.Text>
        )}
        {lines.length > 0 && (
          <pre style={{ maxHeight: '60vh', overflow: 'auto', margin: 0, fontSize: 12 }}>
            {lines.join('\n')}
          </pre>
        )}
      </Spin>
    </Modal>
  );
}
