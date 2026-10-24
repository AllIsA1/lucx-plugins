// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, Space, Tooltip, Typography } from 'antd';
import { CopyOutlined, ToolOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi } from '@/api/plugins';
import type { NodePluginStatus } from '@/schemas/plugins';

// The command that puts the plugin system on a server that runs LucX-UI (and updates it when it is there).
export const PLUGIN_SYSTEM_INSTALL_COMMAND =
  'curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/install.sh | sudo bash';

type Step = 'install' | 'allow' | 'ready' | 'wait' | 'failed';

// What the node needs next, judged from what the master reads of it.
export function pluginSystemStep(status: NodePluginStatus | undefined): Step {
  if (!status) return 'wait';
  switch (status.state) {
    case 'unsupported':
      return 'install';
    case 'locked':
      return 'allow';
    case 'ok':
      return status.plugins?.available === true && status.plugins.allowMasterManagement
        ? 'ready'
        : 'allow';
    case 'pending':
      return 'wait';
    default:
      return 'failed';
  }
}

interface Props {
  nodeId: number;
  nodeName: string;
}

// A button for a node's row: what the node's plugin system needs, and the command that installs it.
export default function NodePluginSystemButton({ nodeId, nodeName }: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const query = useQuery({
    queryKey: keys.plugins.nodes(),
    queryFn: () => pluginsApi.nodes(),
    enabled: open,
    refetchInterval: open ? 10000 : false,
  });
  const statuses = query.data?.success ? (query.data.obj ?? []) : [];
  const status = statuses.find((s) => s.nodeId === nodeId);
  const step = pluginSystemStep(status);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(PLUGIN_SYSTEM_INSTALL_COMMAND);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  };

  const alertType = step === 'ready' ? 'success' : step === 'install' || step === 'failed' ? 'warning' : 'info';

  return (
    <>
      <Tooltip title={t('pages.plugins.nodeSystem.tooltip')}>
        <Button
          type="text"
          size="small"
          style={{ fontSize: 16 }}
          icon={<ToolOutlined />}
          aria-label={t('pages.plugins.nodeSystem.tooltip')}
          onClick={() => setOpen(true)}
        />
      </Tooltip>
      <Modal
        open={open}
        title={t('pages.plugins.nodeSystem.title', { name: nodeName })}
        onCancel={() => setOpen(false)}
        footer={<Button onClick={() => setOpen(false)}>{t('close')}</Button>}
        width={640}
      >
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Alert
            type={alertType}
            showIcon
            message={t(`pages.plugins.nodeSystem.state.${step}`)}
            description={
              step === 'failed' && status?.error ? status.error : undefined
            }
          />
          {step === 'wait' ? null : step === 'ready' ? (
            <Typography.Paragraph>{t('pages.plugins.nodeSystem.readyHelp')}</Typography.Paragraph>
          ) : step === 'allow' ? (
            <Typography.Paragraph>{t('pages.plugins.nodeSystem.allowHelp')}</Typography.Paragraph>
          ) : (
            <>
              <Typography.Paragraph>{t('pages.plugins.nodeSystem.installHelp')}</Typography.Paragraph>
              <Typography.Paragraph>
                <Typography.Text code copyable={false} style={{ wordBreak: 'break-all' }}>
                  {PLUGIN_SYSTEM_INSTALL_COMMAND}
                </Typography.Text>
              </Typography.Paragraph>
              <Button icon={<CopyOutlined />} onClick={() => void copy()}>
                {copied ? t('pages.plugins.nodeSystem.copied') : t('pages.plugins.nodeSystem.copy')}
              </Button>
              <Typography.Paragraph type="secondary">
                {t('pages.plugins.nodeSystem.afterInstall')}
              </Typography.Paragraph>
            </>
          )}
        </Space>
      </Modal>
    </>
  );
}
