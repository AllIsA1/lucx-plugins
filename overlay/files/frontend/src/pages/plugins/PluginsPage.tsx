// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  ConfigProvider,
  Input,
  Layout,
  Modal,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
  type TableColumnsType,
} from 'antd';
import {
  AppstoreAddOutlined,
  CloudSyncOutlined,
  DeleteOutlined,
  FileTextOutlined,
  ReloadOutlined,
  RollbackOutlined,
} from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi, type PluginEntry, type PluginPreview } from '@/api/plugins';
import { useTheme } from '@/hooks/useTheme';
import AppSidebar from '@/layouts/AppSidebar';
import ArchiveMirrorCard from '@/pages/plugins/ArchiveMirrorCard';
import CatalogCard from '@/pages/plugins/CatalogCard';
import MasterManagementCard from '@/pages/plugins/MasterManagementCard';
import NodePluginsCard from '@/pages/plugins/NodePluginsCard';
import PermissionList from '@/pages/plugins/PermissionList';
import PluginLogsModal from '@/pages/plugins/PluginLogsModal';
import PluginPreviewModal from '@/pages/plugins/PluginPreviewModal';
import ProcessesNotice from '@/pages/plugins/ProcessesNotice';
import PluginStatus from '@/pages/plugins/PluginStatus';
import RollbackModal from '@/pages/plugins/RollbackModal';
import UpdateModal from '@/pages/plugins/UpdateModal';
import { isDangerousPermission, shortCommit, sourceUrl } from '@/pages/plugins/permissions';
import './PluginsPage.css';

export default function PluginsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();

  const [url, setUrl] = useState('');
  const [checking, setChecking] = useState(false);
  const [preview, setPreview] = useState<PluginPreview | null>(null);
  const [uninstallTarget, setUninstallTarget] = useState<PluginEntry | null>(null);
  const [removeData, setRemoveData] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [logsTarget, setLogsTarget] = useState<PluginEntry | null>(null);
  const [updateTarget, setUpdateTarget] = useState<PluginEntry | null>(null);
  const [updateFromCatalog, setUpdateFromCatalog] = useState(false);
  const [rollbackTarget, setRollbackTarget] = useState<PluginEntry | null>(null);

  const stateQuery = useQuery({
    queryKey: keys.plugins.state(),
    queryFn: () => pluginsApi.state(),
    refetchInterval: 5000,
  });
  const state = stateQuery.data?.success ? (stateQuery.data.obj ?? undefined) : undefined;
  const ready = stateQuery.isFetched;

  const pageClass = useMemo(() => {
    const classes = ['plugins-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: keys.plugins.root() });
  };

  const check = async () => {
    const trimmed = url.trim();
    if (!trimmed) return;
    setChecking(true);
    try {
      const res = await pluginsApi.preview(trimmed);
      if (res.success && res.obj) setPreview(res.obj);
    } finally {
      setChecking(false);
    }
  };

  const toggle = async (entry: PluginEntry, enabled: boolean) => {
    setBusyId(entry.id);
    try {
      const res = await pluginsApi.setEnabled(entry.id, enabled);
      if (res.success) {
        messageApi.success(
          t(enabled ? 'pages.plugins.toasts.enabled' : 'pages.plugins.toasts.disabled'),
        );
      }
      refresh();
    } finally {
      setBusyId(null);
    }
  };

  const restart = async (entry: PluginEntry) => {
    setBusyId(entry.id);
    try {
      const res = await pluginsApi.restart(entry.id);
      if (res.success) messageApi.success(t('pages.plugins.toasts.restarted'));
      refresh();
    } finally {
      setBusyId(null);
    }
  };

  const confirmUninstall = async () => {
    if (!uninstallTarget) return;
    setBusyId(uninstallTarget.id);
    try {
      const res = await pluginsApi.uninstall(uninstallTarget.id, removeData);
      if (res.success) messageApi.success(t('pages.plugins.toasts.uninstalled'));
      setUninstallTarget(null);
      refresh();
    } finally {
      setBusyId(null);
    }
  };

  const columns: TableColumnsType<PluginEntry> = [
    {
      title: t('pages.plugins.plugin'),
      dataIndex: 'name',
      render: (_: string, p) => (
        <div>
          <Typography.Text strong>{p.name}</Typography.Text>
          <br />
          <Typography.Text type="secondary" code>
            {p.id}
          </Typography.Text>
        </div>
      ),
    },
    { title: t('pages.plugins.version'), dataIndex: 'version', width: 100 },
    {
      title: t('pages.plugins.kind'),
      render: (_: unknown, p) => (
        <Space size={4} wrap>
          <Tag>{t(`pages.plugins.type.${p.type}`)}</Tag>
          <Tag>{t(`pages.plugins.scope.${p.scope}`)}</Tag>
        </Space>
      ),
    },
    {
      title: t('pages.plugins.source'),
      render: (_: unknown, p) => (
        <span>
          <a href={sourceUrl(p.source)} target="_blank" rel="noopener noreferrer">
            {p.source.owner}/{p.source.repo}
          </a>
          <br />
          <Tooltip title={p.commit}>
            <Typography.Text type="secondary" code>
              {shortCommit(p.commit)}
            </Typography.Text>
          </Tooltip>
        </span>
      ),
    },
    {
      title: t('pages.plugins.status'),
      render: (_: unknown, p) => <PluginStatus plugin={p} />,
    },
    {
      title: t('pages.plugins.permissions'),
      render: (_: unknown, p) => (
        <Tooltip title={<PermissionList permissions={p.permissions} />}>
          <Space size={4} wrap>
            {p.permissions.length === 0 && (
              <Typography.Text type="secondary">{t('pages.plugins.noPermissions')}</Typography.Text>
            )}
            {p.permissions.map((perm) => (
              <Tag key={perm} color={isDangerousPermission(perm) ? 'red' : 'default'}>
                {perm}
              </Tag>
            ))}
          </Space>
        </Tooltip>
      ),
    },
    {
      title: t('pages.plugins.enabled'),
      width: 100,
      render: (_: unknown, p) => (
        <Switch
          checked={p.enabled}
          loading={busyId === p.id}
          onChange={(on) => void toggle(p, on)}
        />
      ),
    },
    {
      title: '',
      width: 200,
      fixed: 'right',
      render: (_: unknown, p) => (
        <Space size={0}>
          {p.type !== 'ui' && (
            <Tooltip title={t('pages.plugins.logs')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.logs')}
                icon={<FileTextOutlined />}
                onClick={() => setLogsTarget(p)}
              />
            </Tooltip>
          )}
          {p.type !== 'ui' && p.enabled && (
            <Tooltip title={t('pages.plugins.restart')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.restart')}
                icon={<ReloadOutlined />}
                loading={busyId === p.id}
                onClick={() => void restart(p)}
              />
            </Tooltip>
          )}
          <Tooltip title={t('pages.plugins.update.button')}>
            <Button
              type="text"
              aria-label={t('pages.plugins.update.button')}
              icon={<CloudSyncOutlined />}
              onClick={() => {
                setUpdateFromCatalog(false);
                setUpdateTarget(p);
              }}
            />
          </Tooltip>
          {p.previous && (
            <Tooltip title={t('pages.plugins.rollback.button')}>
              <Button
                type="text"
                aria-label={t('pages.plugins.rollback.button')}
                icon={<RollbackOutlined />}
                onClick={() => setRollbackTarget(p)}
              />
            </Tooltip>
          )}
          <Button
            danger
            type="text"
            aria-label={t('pages.plugins.uninstall')}
            icon={<DeleteOutlined />}
            onClick={() => {
              setRemoveData(true);
              setUninstallTarget(p);
            }}
          />
        </Space>
      ),
    },
  ];

  const body = (
    <>
      <Typography.Title level={3}>
        <AppstoreAddOutlined /> {t('pages.plugins.title')}
      </Typography.Title>
      <Typography.Paragraph type="secondary">{t('pages.plugins.subtitle')}</Typography.Paragraph>

      {state && !state.available && (
        <Alert
          type={state.safeMode ? 'warning' : 'error'}
          showIcon
          style={{ marginBottom: 16 }}
          message={
            state.safeMode
              ? t('pages.plugins.disabledBySwitch')
              : t('pages.plugins.unavailable', { error: state.error })
          }
        />
      )}
      {!stateQuery.data?.success && ready && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
          message={stateQuery.data?.msg || t('pages.plugins.loadFailed')}
        />
      )}

      <Card title={t('pages.plugins.installTitle')}>
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.trustWarning')}
        />
        <Space.Compact style={{ width: '100%' }}>
          <Input
            value={url}
            disabled={!state?.available}
            placeholder="https://github.com/owner/repo"
            onChange={(e) => setUrl(e.target.value)}
            onPressEnter={() => void check()}
          />
          <Button
            type="primary"
            loading={checking}
            disabled={!state?.available || url.trim() === ''}
            onClick={() => void check()}
          >
            {t('pages.plugins.check')}
          </Button>
        </Space.Compact>
      </Card>

      <CatalogCard
        available={state?.available === true}
        installed={state?.plugins ?? []}
        onInstall={setPreview}
        onUpdate={(plugin) => {
          setUpdateFromCatalog(true);
          setUpdateTarget(plugin);
        }}
      />

      <Card title={t('pages.plugins.installedTitle')} style={{ marginTop: 16 }}>
        <ProcessesNotice state={state} />
        <Table<PluginEntry>
          rowKey="id"
          size="middle"
          columns={columns}
          dataSource={state?.plugins ?? []}
          pagination={false}
          scroll={{ x: 'max-content' }}
          locale={{ emptyText: t('pages.plugins.empty') }}
        />
      </Card>

      {state?.available && (
        <NodePluginsCard
          masterPlugins={state.plugins}
          onToast={(text) => messageApi.success(text)}
        />
      )}
      {state?.available && (
        <MasterManagementCard allowed={state.allowMasterManagement} onChanged={refresh} />
      )}
      {state?.available && (
        <ArchiveMirrorCard key={state.customArchiveBase} state={state} onSaved={refresh} />
      )}

      {preview && (
        <PluginPreviewModal
          key={preview.token}
          preview={preview}
          isolation={state?.processesIsolation}
          onClose={() => setPreview(null)}
          onInstalled={() => {
            setPreview(null);
            setUrl('');
            messageApi.success(t('pages.plugins.toasts.installed'));
            refresh();
          }}
        />
      )}

      {updateTarget && (
        <UpdateModal
          key={`${updateTarget.id}:${updateFromCatalog}`}
          plugin={updateTarget}
          fromCatalog={updateFromCatalog}
          onClose={() => setUpdateTarget(null)}
          onUpdated={() => {
            setUpdateTarget(null);
            messageApi.success(t('pages.plugins.update.applied'));
            refresh();
          }}
        />
      )}
      {rollbackTarget && (
        <RollbackModal
          key={rollbackTarget.id}
          plugin={rollbackTarget}
          onClose={() => setRollbackTarget(null)}
          onDone={() => {
            setRollbackTarget(null);
            messageApi.success(t('pages.plugins.rollback.done'));
            refresh();
          }}
        />
      )}

      {logsTarget && (
        <PluginLogsModal
          key={logsTarget.id}
          id={logsTarget.id}
          name={logsTarget.name}
          onClose={() => setLogsTarget(null)}
        />
      )}

      <Modal
        open={uninstallTarget !== null}
        title={t('pages.plugins.uninstallTitle', { name: uninstallTarget?.name ?? '' })}
        okText={t('pages.plugins.uninstall')}
        okButtonProps={{ danger: true, loading: busyId !== null }}
        cancelText={t('cancel')}
        onOk={() => void confirmUninstall()}
        onCancel={() => setUninstallTarget(null)}
      >
        <Typography.Paragraph>{t('pages.plugins.uninstallBody')}</Typography.Paragraph>
        <Checkbox checked={removeData} onChange={(e) => setRemoveData(e.target.checked)}>
          {t('pages.plugins.removeData')}
        </Checkbox>
      </Modal>
    </>
  );

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin spinning={!ready} delay={200} size="large">
              {ready ? body : <div className="loading-spacer" />}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
