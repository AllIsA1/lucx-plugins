// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Modal, Space, Typography } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi, type PluginSystem } from '@/api/plugins';

// How long the page waits for the plugin system to come back as the new release.
const RESTART_WAIT_MS = 60_000;
// The panel restarts too, and has to be up and answering before the page can see the new build.
const PANEL_WAIT_MS = 180_000;
const POLL_MS = 1_500;

interface Props {
  onUpdated: () => void;
  onToast: (text: string) => void;
}

// The plugin system service and the panel's own plugin build: the releases they run and one button to move both
// to the newer ones. The plugin system restarts first (the panel and the plugins keep running), then the panel.
// Hidden when this build does not update itself.
export default function SystemCard({ onUpdated, onToast }: Props) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [checking, setChecking] = useState(false);
  const [waitingFor, setWaitingFor] = useState<string | null>(null);
  const [panelNext, setPanelNext] = useState<string | null>(null);
  const [waitingPanel, setWaitingPanel] = useState<string | null>(null);
  const [panelDone, setPanelDone] = useState('');
  const [problem, setProblem] = useState('');
  // The panel is away for a moment while it restarts; the card must not vanish with it.
  const lastGood = useRef<PluginSystem | null>(null);

  const query = useQuery({
    queryKey: keys.plugins.system(),
    queryFn: () => pluginsApi.system(),
    refetchInterval: (q) =>
      waitingFor || waitingPanel || q.state.data?.obj?.busy || q.state.data?.obj?.panel?.busy
        ? POLL_MS
        : false,
  });
  if (query.data?.success && query.data.obj) lastGood.current = query.data.obj;
  const system = query.data?.success ? query.data.obj : waitingPanel ? lastGood.current : null;
  const panel = system?.panel?.managed ? system.panel : null;

  const startPanel = (target: string) => {
    setWaitingPanel(target);
    // The call may never be answered: the panel restarts under it. The state on the page is what counts.
    void pluginsApi.systemUpdate('panel');
  };

  useEffect(() => {
    if (!waitingFor || !system) return;
    if (system.version === waitingFor && !system.busy) {
      setWaitingFor(null);
      onToast(t('pages.plugins.system.updated', { version: waitingFor }));
      onUpdated();
      if (panelNext) {
        const target = panelNext;
        setPanelNext(null);
        startPanel(target);
      }
    } else if (system.rolledBack === waitingFor) {
      setWaitingFor(null);
      setPanelNext(null);
      setProblem(t('pages.plugins.system.rolledBack', { version: waitingFor }));
    }
    // startPanel only sets state and fires one call
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [waitingFor, system, onToast, onUpdated, t]);

  useEffect(() => {
    if (!waitingPanel || !system?.panel) return;
    const p = system.panel;
    if (p.release === waitingPanel && !p.busy) {
      setWaitingPanel(null);
      setPanelDone(waitingPanel);
      onToast(t('pages.plugins.system.panelUpdated', { release: waitingPanel }));
      onUpdated();
    } else if (p.error && !p.busy && p.release !== waitingPanel) {
      setWaitingPanel(null);
      setProblem(p.error);
    }
  }, [waitingPanel, system, onToast, onUpdated, t]);

  useEffect(() => {
    if (!waitingFor) return;
    const timer = setTimeout(() => {
      setWaitingFor(null);
      setPanelNext(null);
      setProblem(t('pages.plugins.system.timeout'));
    }, RESTART_WAIT_MS);
    return () => clearTimeout(timer);
  }, [waitingFor, t]);

  useEffect(() => {
    if (!waitingPanel) return;
    const timer = setTimeout(() => {
      setWaitingPanel(null);
      setProblem(t('pages.plugins.system.panelTimeout'));
    }, PANEL_WAIT_MS);
    return () => clearTimeout(timer);
  }, [waitingPanel, t]);

  if (!system?.managed && !panel) return null;

  const refetch = () => queryClient.invalidateQueries({ queryKey: keys.plugins.system() });

  const check = async () => {
    setChecking(true);
    setProblem('');
    try {
      const res = await pluginsApi.systemCheck();
      if (!res.success && res.msg) setProblem(res.msg);
    } finally {
      setChecking(false);
      void refetch();
    }
  };

  const hostTarget = system?.managed ? (system?.available ?? '') : '';
  const panelTarget = panel?.available ?? '';

  const update = async () => {
    setConfirming(false);
    setProblem('');
    setPanelDone('');
    if (hostTarget) {
      const res = await pluginsApi.systemUpdate();
      if (res.success) {
        setPanelNext(panelTarget || null);
        setWaitingFor(hostTarget);
      } else void refetch();
    } else if (panelTarget) {
      startPanel(panelTarget);
    }
  };

  const checkedSource = system?.managed ? system.checkedAt : (panel?.checkedAt ?? '');
  const checkedAt = new Date(checkedSource);
  const checkedKnown = checkedAt.getFullYear() > 2000;
  const working = waitingFor !== null || waitingPanel !== null || !!system?.busy || !!panel?.busy;
  const canUpdate = !!hostTarget || !!panelTarget;
  const errorText = problem || system?.error || panel?.error || '';

  const confirmTitle =
    hostTarget && panelTarget
      ? t('pages.plugins.system.confirmBothTitle', { version: hostTarget, release: panelTarget })
      : hostTarget
        ? t('pages.plugins.system.confirmTitle', { version: hostTarget })
        : t('pages.plugins.system.confirmPanelTitle', { release: panelTarget });

  return (
    <Card title={t('pages.plugins.system.title')} style={{ marginTop: 16 }}>
      <Typography.Paragraph type="secondary">{t('pages.plugins.system.desc')}</Typography.Paragraph>
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
              {checkedKnown &&
                ` ${t('pages.plugins.system.checkedAt', { time: checkedAt.toLocaleString() })}`}
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
      {waitingFor && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.system.restarting', { version: waitingFor })}
        />
      )}
      {waitingPanel && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.system.panelRestarting', { release: waitingPanel })}
        />
      )}
      {panelDone && (
        <Alert
          type="success"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.system.panelReload', { release: panelDone })}
          action={
            <Button size="small" onClick={() => window.location.reload()}>
              {t('pages.plugins.system.reload')}
            </Button>
          }
        />
      )}
      {errorText && (
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={errorText} />
      )}
      <Space>
        <Button loading={checking} disabled={working} onClick={() => void check()}>
          {t('pages.plugins.system.check')}
        </Button>
        {canUpdate && (
          <Button
            type="primary"
            loading={working}
            disabled={working}
            onClick={() => setConfirming(true)}
          >
            {t('pages.plugins.system.update')}
          </Button>
        )}
      </Space>
      <Modal
        open={confirming}
        title={confirmTitle}
        okText={t('pages.plugins.system.update')}
        cancelText={t('cancel')}
        onOk={() => void update()}
        onCancel={() => setConfirming(false)}
      >
        {hostTarget && <p>{t('pages.plugins.system.confirmBody')}</p>}
        {panelTarget && <p>{t('pages.plugins.system.confirmPanelBody')}</p>}
      </Modal>
    </Card>
  );
}
