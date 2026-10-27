// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  Checkbox,
  Modal,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  DeleteOutlined,
  FileTextOutlined,
  PlusOutlined,
  ReloadOutlined,
  SyncOutlined,
  ToolOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { TableColumnsType } from 'antd';

import { keys } from '@/api/queryKeys';
import { pluginsApi, type NodePluginStatus, type PluginEntry } from '@/api/plugins';
import type { NodePlugin } from '@/schemas/plugins';
import NodeInstallModal from '@/pages/plugins/NodeInstallModal';
import NodeSystemModal from '@/pages/plugins/NodeSystemModal';
import NodeUpdateModal from '@/pages/plugins/NodeUpdateModal';
import PluginLogsModal from '@/pages/plugins/PluginLogsModal';
import PluginStatus from '@/pages/plugins/PluginStatus';
import { canManageNode, installCandidates, pluginDrift } from '@/pages/plugins/nodePlugins';
import { shortCommit } from '@/pages/plugins/permissions';

interface Props {
  masterPlugins: PluginEntry[];
  onToast: (text: string) => void;
}

const STATE_COLOR: Record<NodePluginStatus['state'], string> = {
  ok: 'green',
  pending: 'blue',
  offline: 'default',
  unsupported: 'default',
  error: 'red',
  locked: 'orange',
};

interface Target {
  nodeId: number;
  nodeName: string;
  plugin: NodePlugin;
}

export default function NodePluginsCard({ masterPlugins, onToast }: Props) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const [installFor, setInstallFor] = useState<NodePluginStatus | null>(null);
  const [logs, setLogs] = useState<Target | null>(null);
  const [removal, setRemoval] = useState<Target | null>(null);
  const [updating, setUpdating] = useState<Target | null>(null);
  const [systemFor, setSystemFor] = useState<NodePluginStatus | null>(null);
  const [removeData, setRemoveData] = useState(true);

  const nodesQuery = useQuery({
    queryKey: keys.plugins.nodes(),
    queryFn: () => pluginsApi.nodes(),
    refetchInterval: 10000,
  });
  const nodes = nodesQuery.data?.success ? (nodesQuery.data.obj ?? []) : [];

  const reload = () => void queryClient.invalidateQueries({ queryKey: keys.plugins.nodes() });

  const run = async (key: string, call: () => Promise<{ success: boolean }>, toast?: string) => {
    setBusy(key);
    try {
      const res = await call();
      if (res.success && toast) onToast(toast);
    } finally {
      setBusy(null);
      reload();
    }
  };

  const pluginColumns = (node: NodePluginStatus): TableColumnsType<NodePlugin> => {
    const manageable = canManageNode(node);
    return [
      {
        title: t('pages.plugins.plugin'),
        render: (_: unknown, p) => (
          <div>
            <Typography.Text strong>{p.name}</Typography.Text>
            <br />
            <Typography.Text type="secondary" code>
              {p.id}
            </Typography.Text>
          </div>
        ),
      },
      {
        title: t('pages.plugins.version'),
        render: (_: unknown, p) => {
          const drift = pluginDrift(masterPlugins, p);
          return (
            <Space direction="vertical" size={2}>
              <span>
                {p.version}{' '}
                <Typography.Text type="secondary" code>
                  {shortCommit(p.commit)}
                </Typography.Text>
              </span>
              {drift === 'differs' && (
                <Tooltip title={t('pages.plugins.nodes.driftHint')}>
                  <Tag color="orange">{t('pages.plugins.nodes.drift')}</Tag>
                </Tooltip>
              )}
              {drift === 'differs' && manageable && p.managedByMaster === true && (
                <Button
                  size="small"
                  onClick={() =>
                    setUpdating({
                      nodeId: node.nodeId,
                      nodeName: node.nodeName,
                      plugin: p,
                    })
                  }
                >
                  {t('pages.plugins.nodes.update.button')}
                </Button>
              )}
            </Space>
          );
        },
      },
      {
        title: t('pages.plugins.status'),
        render: (_: unknown, p) => <PluginStatus plugin={p} />,
      },
      {
        title: t('pages.plugins.nodes.origin'),
        render: (_: unknown, p) => (
          <Tag color={p.managedByMaster ? 'blue' : 'default'}>
            {t(p.managedByMaster ? 'pages.plugins.nodes.byMaster' : 'pages.plugins.nodes.local')}
          </Tag>
        ),
      },
      {
        title: t('pages.plugins.enabled'),
        width: 100,
        render: (_: unknown, p) => (
          <Switch
            checked={p.enabled}
            disabled={!manageable || !p.managedByMaster}
            loading={busy === `${node.nodeId}:${p.id}`}
            onChange={(on) =>
              void run(
                `${node.nodeId}:${p.id}`,
                () => pluginsApi.nodeSetEnabled(node.nodeId, p.id, on),
                t(on ? 'pages.plugins.toasts.enabled' : 'pages.plugins.toasts.disabled'),
              )
            }
          />
        ),
      },
      {
        title: '',
        width: 130,
        render: (_: unknown, p) => {
          const target = {
            nodeId: node.nodeId,
            nodeName: node.nodeName,
            plugin: p,
          };
          const own = manageable && p.managedByMaster === true;
          return (
            <Space size={0}>
              <Button
                type="text"
                aria-label={t('pages.plugins.logs')}
                icon={<FileTextOutlined />}
                disabled={!own}
                onClick={() => setLogs(target)}
              />
              <Button
                type="text"
                aria-label={t('pages.plugins.restart')}
                icon={<ReloadOutlined />}
                disabled={!own || !p.enabled}
                onClick={() =>
                  void run(
                    `${node.nodeId}:${p.id}`,
                    () => pluginsApi.nodeRestart(node.nodeId, p.id),
                    t('pages.plugins.toasts.restarted'),
                  )
                }
              />
              <Button
                danger
                type="text"
                aria-label={t('pages.plugins.uninstall')}
                icon={<DeleteOutlined />}
                disabled={!own}
                onClick={() => {
                  setRemoveData(true);
                  setRemoval(target);
                }}
              />
            </Space>
          );
        },
      },
    ];
  };

  const columns: TableColumnsType<NodePluginStatus> = [
    { title: t('pages.plugins.nodes.node'), dataIndex: 'nodeName' },
    {
      title: t('pages.plugins.status'),
      render: (_: unknown, n) => (
        <Space direction="vertical" size={2}>
          <Tooltip title={n.error || undefined}>
            <Tag color={STATE_COLOR[n.state]} style={{ marginInlineEnd: 0 }}>
              {t(`pages.plugins.nodes.state.${n.state}`)}
            </Tag>
          </Tooltip>
          {n.state === 'locked' && (
            <Typography.Text type="warning">
              {t('pages.plugins.nodes.managementOff')}
            </Typography.Text>
          )}
          {n.state === 'error' && <Typography.Text type="danger">{n.error}</Typography.Text>}
        </Space>
      ),
    },
    {
      title: t('pages.plugins.nodes.count'),
      render: (_: unknown, n) => n.plugins?.plugins.length ?? 0,
    },
    {
      title: '',
      width: 160,
      render: (_: unknown, n) => {
        const manageable = canManageNode(n);
        return (
          <Space size={0}>
            <Tooltip title={t('pages.plugins.nodes.refresh')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.nodes.refresh')}
                icon={<SyncOutlined />}
                disabled={n.state === 'offline' || n.state === 'unsupported'}
                loading={busy === `${n.nodeId}:refresh`}
                onClick={() =>
                  void run(`${n.nodeId}:refresh`, () => pluginsApi.nodeRefresh(n.nodeId))
                }
              />
            </Tooltip>
            <Tooltip title={t('pages.plugins.nodes.system.button')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.nodes.system.button')}
                icon={<ToolOutlined />}
                disabled={!manageable}
                onClick={() => setSystemFor(n)}
              />
            </Tooltip>
            <Tooltip title={t('pages.plugins.nodes.install')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.nodes.install')}
                icon={<PlusOutlined />}
                disabled={!manageable}
                onClick={() => setInstallFor(n)}
              />
            </Tooltip>
          </Space>
        );
      },
    },
  ];

  const confirmRemoval = async () => {
    if (!removal) return;
    const { nodeId, plugin } = removal;
    await run(
      `${nodeId}:${plugin.id}`,
      () => pluginsApi.nodeUninstall(nodeId, plugin.id, removeData),
      t('pages.plugins.toasts.uninstalled'),
    );
    setRemoval(null);
  };

  return (
    <Card title={t('pages.plugins.nodes.title')} style={{ marginTop: 16 }}>
      <Typography.Paragraph type="secondary">{t('pages.plugins.nodes.desc')}</Typography.Paragraph>
      <Table<NodePluginStatus>
        rowKey="nodeId"
        size="middle"
        columns={columns}
        dataSource={nodes}
        pagination={false}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('pages.plugins.nodes.empty') }}
        expandable={{
          rowExpandable: (n) => (n.plugins?.plugins.length ?? 0) > 0,
          expandedRowRender: (n) => (
            <Table<NodePlugin>
              rowKey="id"
              size="small"
              columns={pluginColumns(n)}
              dataSource={n.plugins?.plugins ?? []}
              pagination={false}
            />
          ),
        }}
      />

      {installFor && (
        <NodeInstallModal
          key={installFor.nodeId}
          nodeId={installFor.nodeId}
          nodeName={installFor.nodeName}
          candidates={installCandidates(masterPlugins, installFor)}
          onClose={() => setInstallFor(null)}
          onInstalled={() => {
            setInstallFor(null);
            onToast(t('pages.plugins.nodes.installedToast'));
            reload();
          }}
        />
      )}
      {systemFor && (
        <NodeSystemModal
          key={systemFor.nodeId}
          nodeId={systemFor.nodeId}
          nodeName={systemFor.nodeName}
          onClose={() => setSystemFor(null)}
          onDone={() => {
            onToast(t('pages.plugins.nodes.system.done'));
            reload();
          }}
        />
      )}
      {updating && masterPlugins.some((m) => m.id === updating.plugin.id) && (
        <NodeUpdateModal
          key={`${updating.nodeId}:${updating.plugin.id}`}
          nodeId={updating.nodeId}
          nodeName={updating.nodeName}
          onNode={updating.plugin}
          master={masterPlugins.find((m) => m.id === updating.plugin.id)!}
          onClose={() => setUpdating(null)}
          onDone={() => {
            setUpdating(null);
            onToast(t('pages.plugins.nodes.update.done'));
            reload();
          }}
        />
      )}
      {logs && (
        <PluginLogsModal
          key={`${logs.nodeId}:${logs.plugin.id}`}
          id={logs.plugin.id}
          name={`${logs.plugin.name} · ${logs.nodeName}`}
          nodeId={logs.nodeId}
          onClose={() => setLogs(null)}
        />
      )}
      <Modal
        open={removal !== null}
        title={t('pages.plugins.nodes.uninstallTitle', {
          name: removal?.plugin.name ?? '',
          node: removal?.nodeName ?? '',
        })}
        okText={t('pages.plugins.uninstall')}
        okButtonProps={{ danger: true, loading: busy !== null }}
        cancelText={t('cancel')}
        onOk={() => void confirmRemoval()}
        onCancel={() => setRemoval(null)}
      >
        <Checkbox checked={removeData} onChange={(e) => setRemoveData(e.target.checked)}>
          {t('pages.plugins.removeData')}
        </Checkbox>
      </Modal>
    </Card>
  );
}
