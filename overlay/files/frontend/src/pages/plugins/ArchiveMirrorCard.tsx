// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Input, Space, Typography } from 'antd';

import { pluginsApi, type PluginsState } from '@/api/plugins';

interface Props {
  state: PluginsState;
  onSaved: () => void;
}

export default function ArchiveMirrorCard({ state, onSaved }: Props) {
  const { t } = useTranslation();
  const [value, setValue] = useState(state.customArchiveBase);
  const [saving, setSaving] = useState(false);

  const save = async (next: string) => {
    setSaving(true);
    try {
      const res = await pluginsApi.saveArchiveBase(next);
      if (res.success) onSaved();
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card title={t('pages.plugins.mirrorTitle')} style={{ marginTop: 16 }}>
      <Typography.Paragraph type="secondary">{t('pages.plugins.mirrorDesc')}</Typography.Paragraph>
      <Space.Compact style={{ width: '100%' }}>
        <Input
          value={value}
          placeholder={state.defaultArchiveBase}
          onChange={(e) => setValue(e.target.value)}
          onPressEnter={() => void save(value)}
        />
        <Button type="primary" loading={saving} onClick={() => void save(value)}>
          {t('save')}
        </Button>
        <Button
          disabled={saving || state.customArchiveBase === ''}
          onClick={() => {
            setValue('');
            void save('');
          }}
        >
          {t('reset')}
        </Button>
      </Space.Compact>
      <Typography.Text type="secondary">
        {t('pages.plugins.mirrorEffective', { base: state.archiveBase })}
      </Typography.Text>
    </Card>
  );
}
