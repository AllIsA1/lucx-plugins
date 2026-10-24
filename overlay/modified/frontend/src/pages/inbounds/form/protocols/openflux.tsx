// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useTranslation } from 'react-i18next';
import { Alert, Input, Select } from 'antd';

import { FormField } from '@/components/form/rhf';

// Carrier fields in the order of the core's priorities. Each takes several links (one per line or
// comma-separated); the panel joins them into one group.
const CARRIER_FIELDS: { name: string; label: string; placeholder: string }[] = [
  {
    name: 'yandexUrl',
    label: 'openfluxYandexUrl',
    placeholder: 'https://docs.yandex.ru/docs/view?url=',
  },
  {
    name: 'volgaUrl',
    label: 'openfluxVolgaUrl',
    placeholder: 'https://docs.yandex.ru/docs/view?url=',
  },
  { name: 'boardUrl', label: 'openfluxBoardUrl', placeholder: 'https://…/?hash=' },
  {
    name: 'telemostUrl',
    label: 'openfluxTelemostUrl',
    placeholder: 'https://telemost.yandex.ru/j/',
  },
  { name: 'mailruUrl', label: 'openfluxMailruUrl', placeholder: 'https://cloud.mail.ru/public/' },
  {
    name: 'cupsUrl',
    label: 'openfluxCupsUrl',
    placeholder: 'https://interview.cups.online/live-coding/?room=',
  },
  { name: 'avitoUrl', label: 'openfluxAvitoUrl', placeholder: 'https://code.avito.ru/?rooms=' },
];

export default function OpenfluxFields() {
  const { t } = useTranslation();
  return (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message={t('pages.inbounds.form.openfluxNote')}
      />
      <FormField
        name={['settings', 'shareHost']}
        label={t('pages.inbounds.form.openfluxShareHost')}
        tooltip={t('pages.inbounds.form.openfluxShareHostHint')}
      >
        <Input placeholder="203.0.113.5" />
      </FormField>
      <FormField
        name={['settings', 'exitMode']}
        label={t('pages.inbounds.form.openfluxExitMode')}
        tooltip={t('pages.inbounds.form.openfluxExitModeHint')}
      >
        <Select
          options={[
            { value: 'l4', label: t('pages.inbounds.form.openfluxModeL4') },
            { value: 'l3', label: t('pages.inbounds.form.openfluxModeL3') },
          ]}
        />
      </FormField>
      <FormField
        name={['settings', 'debugLevel']}
        label={t('pages.inbounds.form.openfluxDebug')}
        tooltip={t('pages.inbounds.form.openfluxDebugHint')}
      >
        <Select
          options={[
            { value: 0, label: t('pages.inbounds.form.openfluxDebugOff') },
            { value: 2, label: t('pages.inbounds.form.openfluxDebugOn') },
          ]}
        />
      </FormField>
      {CARRIER_FIELDS.map((f) => (
        <FormField
          key={f.name}
          name={['settings', f.name]}
          label={t(`pages.inbounds.form.${f.label}`)}
          tooltip={t('pages.inbounds.form.openfluxUrlHint')}
        >
          <Input.TextArea autoSize={{ minRows: 1, maxRows: 4 }} placeholder={f.placeholder} />
        </FormField>
      ))}
      <FormField
        name={['settings', 'secret']}
        label={t('pages.inbounds.form.openfluxSecret')}
        tooltip={t('pages.inbounds.form.openfluxSecretHint')}
      >
        <Input.Password autoComplete="new-password" />
      </FormField>
    </>
  );
}
