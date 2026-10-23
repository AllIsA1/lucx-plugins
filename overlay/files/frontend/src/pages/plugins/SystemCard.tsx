// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Modal, Space, Typography } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { keys } from '@/api/queryKeys';
import { pluginsApi } from '@/api/plugins';

// How long the page waits for the plugin system to come back as the new release.
const RESTART_WAIT_MS = 60_000;
const POLL_MS = 1_500;

interface Props {
  onUpdated: () => void;
  onToast: (text: string) => void;
}

// The plugin system service itself: the release it runs and a button to move to a newer one. The
// panel and the plugins keep running while it restarts. Hidden when this build does not update itself.
export default function SystemCard({ onUpdated, onToast }: Props) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [checking, setChecking] = useState(false);
  const [waitingFor, setWaitingFor] = useState<string | null>(null);
  const [problem, setProblem] = useState('');

  const query = useQuery({
    queryKey: keys.plugins.system(),
    queryFn: () => pluginsApi.system(),
    refetchInterval: (q) => (waitingFor || q.state.data?.obj?.busy ? POLL_MS : false),
  });
  const system = query.data?.success ? query.data.obj : null;

  useEffect(() => {
    if (!waitingFor || !system) return;
    if (system.version === waitingFor && !system.busy) {
      setWaitingFor(null);
      onToast(t('pages.plugins.system.updated', { version: waitingFor }));
      onUpdated();
    } else if (system.rolledBack === waitingFor) {
      setWaitingFor(null);
      setProblem(t('pages.plugins.system.rolledBack', { version: waitingFor }));
    }
  }, [waitingFor, system, onToast, onUpdated, t]);

  useEffect(() => {
    if (!waitingFor) return;
    const timer = setTimeout(() => {
      setWaitingFor(null);
      setProblem(t('pages.plugins.system.timeout'));
    }, RESTART_WAIT_MS);
    return () => clearTimeout(timer);
  }, [waitingFor, t]);

  if (!system?.managed) return null;

  const refetch = () => queryClient.invalidateQueries({ queryKey: keys.plugins.system() });

  const check = async () => {
    setChecking(true);
    setProblem('');
    try {
      await pluginsApi.systemCheck();
    } finally {
      setChecking(false);
      void refetch();
    }
  };

  const update = async () => {
    setConfirming(false);
    setProblem('');
    const target = system.available;
    const res = await pluginsApi.systemUpdate();
    if (res.success) setWaitingFor(target);
    else void refetch();
  };

  const checkedAt = new Date(system.checkedAt);
  const checkedKnown = checkedAt.getFullYear() > 2000;
  const working = waitingFor !== null || system.busy;

  return (
    <Card title={t('pages.plugins.system.title')} style={{ marginTop: 16 }}>
      <Typography.Paragraph type="secondary">{t('pages.plugins.system.desc')}</Typography.Paragraph>
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
          {checkedKnown && ` ${t('pages.plugins.system.checkedAt', { time: checkedAt.toLocaleString() })}`}
        </Typography.Paragraph>
      )}
      {waitingFor && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.system.restarting', { version: waitingFor })}
        />
      )}
      {(problem || system.error) && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={problem || system.error}
        />
      )}
      <Space>
        <Button loading={checking} disabled={working} onClick={() => void check()}>
          {t('pages.plugins.system.check')}
        </Button>
        {system.available && (
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
        title={t('pages.plugins.system.confirmTitle', { version: system.available })}
        okText={t('pages.plugins.system.update')}
        cancelText={t('cancel')}
        onOk={() => void update()}
        onCancel={() => setConfirming(false)}
      >
        {t('pages.plugins.system.confirmBody')}
      </Modal>
    </Card>
  );
}
