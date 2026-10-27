// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, Space, Typography } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi, type PluginSystem } from '@/api/plugins';

// How long to wait for a node to come back as the new release; its panel restarts too, which takes longer.
const HOST_WAIT_MS = 90_000;
const PANEL_WAIT_MS = 240_000;
const POLL_MS = 2_000;

interface Props {
  nodeId: number;
  nodeName: string;
  onClose: () => void;
  onDone: () => void;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// The plugin system of one node, read through the master: the release it runs, the newer one it found, and the
// buttons to make it look and to move it (the service first, then the panel's plugin build). The node restarts
// its service and its panel on its own a moment after it answers, so the outcome is read from the node's state.
export default function NodeSystemModal({ nodeId, nodeName, onClose, onDone }: Props) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [checking, setChecking] = useState(false);
  const [working, setWorking] = useState('');
  const [problem, setProblem] = useState('');
  const [finished, setFinished] = useState(false);
  const alive = useRef(true);

  const query = useQuery({
    queryKey: keys.plugins.nodeSystem(nodeId),
    queryFn: () => pluginsApi.nodeSystem(nodeId),
    refetchInterval: false,
    retry: false,
  });
  const reply = query.data;
  const system: PluginSystem | null = reply?.success ? (reply.obj ?? null) : null;
  const panel = system?.panel?.managed ? system.panel : null;
  const hostTarget = system?.managed ? system.available : '';
  const panelTarget = panel?.available ?? '';
  const refetch = () =>
    queryClient.invalidateQueries({ queryKey: keys.plugins.nodeSystem(nodeId) });

  // Polls the node's own state until the check passes; the node is away while it restarts, which is not an error.
  const waitFor = async (check: (s: PluginSystem) => boolean, ms: number) => {
    const until = Date.now() + ms;
    while (Date.now() < until && alive.current) {
      await sleep(POLL_MS);
      const res = await pluginsApi.nodeSystem(nodeId);
      if (res.success && res.obj && check(res.obj)) return true;
    }
    return false;
  };

  const check = async () => {
    setChecking(true);
    setProblem('');
    try {
      const res = await pluginsApi.nodeSystemCheck(nodeId);
      if (!res.success && res.msg) setProblem(res.msg);
    } finally {
      setChecking(false);
      void refetch();
    }
  };

  const update = async () => {
    setProblem('');
    setFinished(false);
    try {
      if (hostTarget) {
        setWorking(t('pages.plugins.nodes.system.updatingHost', { version: hostTarget }));
        const res = await pluginsApi.nodeSystemUpdate(nodeId);
        if (!res.success) {
          setProblem(res.msg || t('pages.plugins.nodes.system.failed'));
          return;
        }
        const ok = await waitFor((s) => s.version === hostTarget && !s.busy, HOST_WAIT_MS);
        if (!ok) {
          setProblem(t('pages.plugins.nodes.system.timeout'));
          return;
        }
      }
      if (panelTarget) {
        setWorking(t('pages.plugins.nodes.system.updatingPanel', { release: panelTarget }));
        // The node's panel restarts under this call, so it may never be answered: the state is what counts.
        const res = await pluginsApi.nodeSystemUpdate(nodeId, 'panel');
        if (!res.success && res.msg) {
          setProblem(res.msg);
          return;
        }
        const ok = await waitFor(
          (s) => s.panel?.release === panelTarget && !s.panel?.busy,
          PANEL_WAIT_MS,
        );
        if (!ok) {
          setProblem(t('pages.plugins.nodes.system.panelTimeout'));
          return;
        }
      }
      setFinished(true);
      onDone();
    } finally {
      setWorking('');
      void refetch();
    }
  };

  const busy = working !== '' || checking;
  const errorText = problem || system?.error || panel?.error || '';
  const unavailable = !query.isLoading && !system;

  return (
    <Modal
      open
      width={560}
      title={t('pages.plugins.nodes.system.title', { node: nodeName })}
      onCancel={() => {
        alive.current = false;
        onClose();
      }}
      footer={
        <Space>
          <Button
            onClick={() => {
              alive.current = false;
              onClose();
            }}
          >
            {t('pages.plugins.nodes.system.close')}
          </Button>
          <Button loading={checking} disabled={busy || unavailable} onClick={() => void check()}>
            {t('pages.plugins.system.check')}
          </Button>
          <Button
            type="primary"
            loading={working !== ''}
            disabled={busy || (!hostTarget && !panelTarget)}
            onClick={() => void update()}
          >
            {t('pages.plugins.system.update')}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">
        {t('pages.plugins.nodes.system.desc')}
      </Typography.Paragraph>
      {unavailable && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={reply?.msg || t('pages.plugins.nodes.system.unavailable')}
        />
      )}
      {system?.managed && (
        <>
          <Typography.Paragraph>
            {t('pages.plugins.system.version', { version: system.version })}
          </Typography.Paragraph>
          {system.available ? (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 12 }}
              message={t('pages.plugins.system.available', { version: system.available })}
            />
          ) : (
            <Typography.Paragraph type="secondary">
              {t('pages.plugins.system.upToDate')}
            </Typography.Paragraph>
          )}
        </>
      )}
      {panel && (
        <>
          <Typography.Paragraph>
            {t('pages.plugins.system.panelBuild', {
              release: panel.release || t('pages.plugins.system.panelOriginal'),
            })}
          </Typography.Paragraph>
          {panel.available ? (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 12 }}
              message={t('pages.plugins.system.panelAvailable', { release: panel.available })}
            />
          ) : (
            <Typography.Paragraph type="secondary">
              {t('pages.plugins.system.panelUpToDate')}
            </Typography.Paragraph>
          )}
        </>
      )}
      {system && !system.managed && !panel && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.nodes.system.notManaged')}
        />
      )}
      {working && <Alert type="info" showIcon style={{ marginBottom: 12 }} message={working} />}
      {finished && (
        <Alert
          type="success"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.nodes.system.done')}
        />
      )}
      {errorText && (
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={errorText} />
      )}
    </Modal>
  );
}
