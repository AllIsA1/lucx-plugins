// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useTranslation } from 'react-i18next';
import { Alert, Collapse, Input, InputNumber, Select, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { WARP_PRESETS } from '@/schemas/protocols/inbound/warp';

// Cloudflare WARP over AmneziaWG: the settings of the nodes the subscription gives the clients attached to this inbound.
export default function WarpFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const preset = useWatch({ control, name: 'settings.preset' }) as string | undefined;

  const onPreset = (value: string) => {
    const p = WARP_PRESETS[value];
    if (!p) return;
    setValue('settings.awg.jc', p.jc, { shouldDirty: true });
    setValue('settings.awg.jmin', p.jmin, { shouldDirty: true });
    setValue('settings.awg.jmax', p.jmax, { shouldDirty: true });
    setValue('settings.awg.s1', 0, { shouldDirty: true });
    setValue('settings.awg.s2', 0, { shouldDirty: true });
  };
  const toCustom = () => {
    if (preset !== 'custom') setValue('settings.preset', 'custom', { shouldDirty: true });
  };

  return (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message={t('pages.inbounds.form.warpNote')}
      />
      <FormField
        name={['settings', 'endpoints']}
        label={t('pages.inbounds.form.warpEndpoints')}
        tooltip={t('pages.inbounds.form.warpEndpointsHint')}
      >
        <Select
          mode="tags"
          notFoundContent={null}
          tokenSeparators={[',', ' ']}
          maxCount={3}
          placeholder="engage.cloudflareclient.com:2408"
        />
      </FormField>
      <FormField name={['settings', 'mtu']} label="MTU">
        <InputNumber min={576} max={1420} style={{ width: '100%' }} />
      </FormField>
      <FormField name={['settings', 'dns']} label="DNS">
        <Input />
      </FormField>
      <FormField
        name={['settings', 'ipv6']}
        label={t('pages.inbounds.form.warpIpv6')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField name={['settings', 'keepalive']} label={t('pages.inbounds.form.warpKeepalive')}>
        <InputNumber min={0} max={600} style={{ width: '100%' }} />
      </FormField>
      <FormField
        name={['settings', 'preset']}
        label={t('pages.inbounds.form.warpPreset')}
        tooltip={t('pages.inbounds.form.warpPresetHint')}
      >
        <Select
          onChange={onPreset}
          options={[
            { value: 'standard', label: t('pages.inbounds.form.warpPresetStandard') },
            { value: 'light', label: t('pages.inbounds.form.warpPresetLight') },
            { value: 'strong', label: t('pages.inbounds.form.warpPresetStrong') },
            { value: 'custom', label: t('pages.inbounds.form.warpPresetCustom') },
          ]}
        />
      </FormField>
      <Collapse
        ghost
        items={[
          {
            key: 'awg',
            label: t('pages.inbounds.form.warpAdvanced'),
            children: (
              <>
                <Alert
                  type="warning"
                  showIcon
                  style={{ marginBottom: 12 }}
                  message={t('pages.inbounds.form.warpAdvancedWarn')}
                />
                {(['jc', 'jmin', 'jmax', 's1', 's2'] as const).map((k) => (
                  <FormField key={k} name={['settings', 'awg', k]} label={k.toUpperCase()}>
                    <InputNumber min={0} style={{ width: '100%' }} onChange={toCustom} />
                  </FormField>
                ))}
                {(['h1', 'h2', 'h3', 'h4', 'i1', 'i2', 'i3', 'i4', 'i5'] as const).map((k) => (
                  <FormField key={k} name={['settings', 'awg', k]} label={k.toUpperCase()}>
                    <Input onChange={toCustom} />
                  </FormField>
                ))}
              </>
            ),
          },
        ]}
      />
    </>
  );
}
