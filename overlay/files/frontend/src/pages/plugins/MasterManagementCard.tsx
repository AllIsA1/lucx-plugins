// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Card, Switch, Typography } from 'antd';

import { pluginsApi } from '@/api/plugins';

interface Props {
  allowed: boolean;
  onChanged: () => void;
}

export default function MasterManagementCard({ allowed, onChanged }: Props) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);

  const change = async (next: boolean) => {
    setSaving(true);
    try {
      const res = await pluginsApi.setMasterManagement(next);
      if (res.success) onChanged();
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card title={t('pages.plugins.masterTitle')} style={{ marginTop: 16 }}>
      <Typography.Paragraph type="secondary">{t('pages.plugins.masterDesc')}</Typography.Paragraph>
      {allowed && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.masterWarning')}
        />
      )}
      <Switch
        checked={allowed}
        loading={saving}
        aria-label={t('pages.plugins.masterSwitch')}
        onChange={(on) => void change(on)}
      />{' '}
      <Typography.Text>{t('pages.plugins.masterSwitch')}</Typography.Text>
    </Card>
  );
}
