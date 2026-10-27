// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Collapse, Input, InputNumber, Select, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { useOutboundTags } from '@/api/queries/useOutboundTags';

// Providers only the ghostlane-project fork of the core has (the Better olcRTC plugin installs it).
const OLCRTC_FORK_PROVIDERS = ['salutejazz', 'vkcalls', 'mtslink'];

const OLCRTC_ROOM_PLACEHOLDER: Record<string, string> = {
  jitsi: 'https://meet.jit.si/your-room',
  telemost: 'https://telemost.yandex.ru/j/…',
  wbstream: 'room id from stream.wb.ru',
  salutejazz: 'code:password',
  vkcalls: 'https://vk.ru/call/join/…',
  mtslink: 'https://my.mts-link.ru/j/owner/event',
};

// Which transports a provider carries (docs/settings.md of the core). A WB Stream guest cannot publish data, so
// datachannel there needs an account token. Keep in step with olcrtcTransportOK in internal/lucx/tunnel/olcrtc.go.
function olcrtcTransports(provider: string | undefined, hasToken: boolean): string[] {
  switch (provider) {
    case 'telemost':
      return ['vp8channel', 'videochannel'];
    case 'wbstream':
      return hasToken
        ? ['datachannel', 'vp8channel', 'seichannel', 'videochannel']
        : ['vp8channel', 'seichannel', 'videochannel'];
    case 'salutejazz':
      return ['datachannel'];
    case 'vkcalls':
    case 'mtslink':
      return ['vp8channel'];
    default:
      return ['datachannel', 'vp8channel', 'seichannel', 'videochannel'];
  }
}

export default function OlcrtcFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const transport = useWatch({ control, name: 'settings.transport' }) as string | undefined;
  const provider = useWatch({ control, name: 'settings.provider' }) as string | undefined;
  const routeThroughXray = useWatch({ control, name: 'settings.routeThroughXray' }) as
    | boolean
    | undefined;
  const udpEnabled = useWatch({ control, name: 'settings.udpEnabled' }) as boolean | undefined;
  const { data: outboundTags } = useOutboundTags();

  const authToken = useWatch({ control, name: 'settings.authToken' }) as string | undefined;
  const videoCodec = useWatch({ control, name: 'settings.videoCodec' }) as string | undefined;
  const hasToken = !!authToken?.trim();
  const allowed = useMemo(() => olcrtcTransports(provider, hasToken), [provider, hasToken]);
  const transportOptions = allowed.map((value) => ({ value, label: value }));

  useEffect(() => {
    if (!transport || allowed.includes(transport)) return;
    setValue('settings.transport', allowed.includes('vp8channel') ? 'vp8channel' : allowed[0], {
      shouldDirty: true,
    });
  }, [allowed, transport, setValue]);

  return (
    <>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message={t('pages.inbounds.form.olcrtcSingleCredNote')}
      />
      {OLCRTC_FORK_PROVIDERS.includes(provider ?? '') && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.inbounds.form.olcrtcForkNote')}
        />
      )}
      {provider === 'wbstream' && transport !== 'datachannel' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.inbounds.form.olcrtcWbDatachannelNote')}
        />
      )}
      {provider === 'telemost' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.inbounds.form.olcrtcTelemostVp8Note')}
        />
      )}
      <FormField
        name={['settings', 'routeThroughXray']}
        label={t('pages.inbounds.form.olcrtcRouteThroughXray')}
        tooltip={t('pages.inbounds.form.olcrtcRouteThroughXrayHint')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      {routeThroughXray && (
        <>
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message={t('pages.inbounds.form.olcrtcRouteThroughXrayWarn')}
          />
          <FormField
            name={['settings', 'outboundTag']}
            label={t('pages.inbounds.form.olcrtcRouteOutbound')}
            tooltip={t('pages.inbounds.form.olcrtcRouteOutboundHint')}
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={[
                { value: '', label: t('pages.inbounds.form.olcrtcRouteOutboundPlaceholder') },
                ...(outboundTags || []).map((tag) => ({ value: tag, label: tag })),
              ]}
            />
          </FormField>
        </>
      )}
      <FormField name={['settings', 'provider']} label={t('pages.inbounds.form.olcrtcProvider')}>
        <Select
          options={[
            { value: 'jitsi', label: 'Jitsi' },
            { value: 'telemost', label: 'Yandex Telemost' },
            { value: 'wbstream', label: 'WB Stream' },
            { value: 'salutejazz', label: 'SaluteJazz' },
            { value: 'vkcalls', label: 'VK Calls' },
            { value: 'mtslink', label: 'MTS Link' },
          ]}
        />
      </FormField>
      <FormField
        name={['settings', 'roomId']}
        label={t('pages.inbounds.form.olcrtcRoomId')}
        tooltip={t('pages.inbounds.form.olcrtcRoomIdHint')}
      >
        <Input placeholder={OLCRTC_ROOM_PLACEHOLDER[provider ?? 'jitsi'] ?? ''} />
      </FormField>
      <FormField
        name={['settings', 'cryptoKey']}
        label={t('pages.inbounds.form.olcrtcCryptoKey')}
        tooltip={t('pages.inbounds.form.olcrtcCryptoKeyHint')}
      >
        <Input placeholder="openssl rand -hex 32" />
      </FormField>
      <FormField name={['settings', 'transport']} label={t('pages.inbounds.form.olcrtcTransport')}>
        <Select options={transportOptions} />
      </FormField>
      {transport === 'vp8channel' && (
        <>
          <FormField name={['settings', 'vp8Fps']} label={t('pages.inbounds.form.olcrtcVp8Fps')}>
            <InputNumber min={1} max={120} style={{ width: '100%' }} />
          </FormField>
          <FormField
            name={['settings', 'vp8Batch']}
            label={t('pages.inbounds.form.olcrtcVp8Batch')}
          >
            <InputNumber min={1} max={64} style={{ width: '100%' }} />
          </FormField>
        </>
      )}
      {transport === 'seichannel' && (
        <>
          <FormField name={['settings', 'seiFps']} label={t('pages.inbounds.form.olcrtcSeiFps')}>
            <InputNumber min={1} max={120} style={{ width: '100%' }} />
          </FormField>
          <FormField
            name={['settings', 'seiBatch']}
            label={t('pages.inbounds.form.olcrtcSeiBatch')}
          >
            <InputNumber min={1} max={64} style={{ width: '100%' }} />
          </FormField>
          <FormField name={['settings', 'seiFrag']} label={t('pages.inbounds.form.olcrtcSeiFrag')}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </FormField>
          <FormField name={['settings', 'seiAck']} label={t('pages.inbounds.form.olcrtcSeiAck')}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </FormField>
        </>
      )}
      {transport === 'videochannel' && (
        <>
          <FormField
            name={['settings', 'videoW']}
            label={t('pages.inbounds.form.olcrtcVideoWidth')}
          >
            <InputNumber min={1} style={{ width: '100%' }} />
          </FormField>
          <FormField
            name={['settings', 'videoH']}
            label={t('pages.inbounds.form.olcrtcVideoHeight')}
          >
            <InputNumber min={1} style={{ width: '100%' }} />
          </FormField>
          <FormField
            name={['settings', 'videoFps']}
            label={t('pages.inbounds.form.olcrtcVideoFps')}
          >
            <InputNumber min={1} max={120} style={{ width: '100%' }} />
          </FormField>
          <FormField
            name={['settings', 'videoCodec']}
            label={t('pages.inbounds.form.olcrtcVideoCodec')}
          >
            <Select
              options={[
                { value: 'qrcode', label: 'qrcode' },
                { value: 'tile', label: 'tile' },
              ]}
            />
          </FormField>
          {videoCodec === 'tile' ? (
            <>
              <FormField
                name={['settings', 'videoTileModule']}
                label={t('pages.inbounds.form.olcrtcVideoTileModule')}
                tooltip={t('pages.inbounds.form.olcrtcZeroDefaultHint')}
              >
                <InputNumber min={0} max={270} style={{ width: '100%' }} />
              </FormField>
              <FormField
                name={['settings', 'videoTileRs']}
                label={t('pages.inbounds.form.olcrtcVideoTileRs')}
                tooltip={t('pages.inbounds.form.olcrtcZeroDefaultHint')}
              >
                <InputNumber min={0} max={200} style={{ width: '100%' }} />
              </FormField>
            </>
          ) : (
            <>
              <FormField
                name={['settings', 'videoQrSize']}
                label={t('pages.inbounds.form.olcrtcVideoQrSize')}
                tooltip={t('pages.inbounds.form.olcrtcVideoQrSizeHint')}
              >
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormField>
              <FormField
                name={['settings', 'videoQrRecovery']}
                label={t('pages.inbounds.form.olcrtcVideoQrRecovery')}
              >
                <Select
                  options={[
                    { value: '', label: t('pages.inbounds.form.olcrtcDefault') },
                    { value: 'low', label: 'low' },
                    { value: 'medium', label: 'medium' },
                    { value: 'high', label: 'high' },
                    { value: 'highest', label: 'highest' },
                  ]}
                />
              </FormField>
            </>
          )}
        </>
      )}
      <FormField name={['settings', 'dns']} label={t('pages.inbounds.form.olcrtcDns')}>
        <Input placeholder="8.8.8.8:53" />
      </FormField>
      <FormField
        name={['settings', 'debug']}
        label={t('pages.inbounds.form.olcrtcDebug')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['settings', 'noLinkName']}
        label={t('pages.inbounds.form.olcrtcNoLinkName')}
        tooltip={t('pages.inbounds.form.olcrtcNoLinkNameHint')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <Collapse
        style={{ marginTop: 8, marginBottom: 8 }}
        items={[
          {
            key: 'fork',
            label: t('pages.inbounds.form.olcrtcAdvanced'),
            forceRender: true,
            children: (
              <>
                <FormField
                  name={['settings', 'authToken']}
                  label={t('pages.inbounds.form.olcrtcAuthToken')}
                  tooltip={t('pages.inbounds.form.olcrtcAuthTokenHint')}
                >
                  <Input.Password autoComplete="off" />
                </FormField>
                <FormField
                  name={['settings', 'extraKeys']}
                  label={t('pages.inbounds.form.olcrtcExtraKeys')}
                  tooltip={t('pages.inbounds.form.olcrtcExtraKeysHint')}
                >
                  <Select mode="tags" tokenSeparators={[',', ' ', '\n']} open={false} />
                </FormField>
                <FormField
                  name={['settings', 'dtlsProfile']}
                  label={t('pages.inbounds.form.olcrtcDtlsProfile')}
                  tooltip={t('pages.inbounds.form.olcrtcDtlsProfileHint')}
                >
                  <Select
                    options={[
                      { value: '', label: t('pages.inbounds.form.olcrtcDefault') },
                      { value: 'off', label: 'off' },
                      { value: 'chrome-linux-138-compat-v1', label: 'chrome-linux-138-compat-v1' },
                    ]}
                  />
                </FormField>
                <FormField
                  name={['settings', 'udpEnabled']}
                  label={t('pages.inbounds.form.olcrtcUdp')}
                  tooltip={t('pages.inbounds.form.olcrtcUdpHint')}
                  valueProp="checked"
                >
                  <Switch />
                </FormField>
                {udpEnabled && (
                  <FormField
                    name={['settings', 'udpMaxFlows']}
                    label={t('pages.inbounds.form.olcrtcUdpMaxFlows')}
                    tooltip={t('pages.inbounds.form.olcrtcZeroDefaultHint')}
                  >
                    <InputNumber min={0} style={{ width: '100%' }} />
                  </FormField>
                )}
                <FormField
                  name={['settings', 'livenessInterval']}
                  label={t('pages.inbounds.form.olcrtcLivenessInterval')}
                  tooltip={t('pages.inbounds.form.olcrtcDurationHint')}
                >
                  <Input placeholder="10s" />
                </FormField>
                <FormField
                  name={['settings', 'livenessTimeout']}
                  label={t('pages.inbounds.form.olcrtcLivenessTimeout')}
                  tooltip={t('pages.inbounds.form.olcrtcDurationHint')}
                >
                  <Input placeholder="15s" />
                </FormField>
                <FormField
                  name={['settings', 'livenessFailures']}
                  label={t('pages.inbounds.form.olcrtcLivenessFailures')}
                  tooltip={t('pages.inbounds.form.olcrtcZeroDefaultHint')}
                >
                  <InputNumber min={0} style={{ width: '100%' }} />
                </FormField>
                <FormField
                  name={['settings', 'maxSessionDuration']}
                  label={t('pages.inbounds.form.olcrtcMaxSessionDuration')}
                  tooltip={t('pages.inbounds.form.olcrtcMaxSessionDurationHint')}
                >
                  <Input placeholder="6h" />
                </FormField>
                <FormField
                  name={['settings', 'trafficMaxPayload']}
                  label={t('pages.inbounds.form.olcrtcTrafficMaxPayload')}
                  tooltip={t('pages.inbounds.form.olcrtcTrafficHint')}
                >
                  <InputNumber min={0} style={{ width: '100%' }} />
                </FormField>
                <FormField
                  name={['settings', 'trafficMinDelay']}
                  label={t('pages.inbounds.form.olcrtcTrafficMinDelay')}
                  tooltip={t('pages.inbounds.form.olcrtcTrafficHint')}
                >
                  <Input placeholder="5ms" />
                </FormField>
                <FormField
                  name={['settings', 'trafficMaxDelay']}
                  label={t('pages.inbounds.form.olcrtcTrafficMaxDelay')}
                  tooltip={t('pages.inbounds.form.olcrtcTrafficHint')}
                >
                  <Input placeholder="30ms" />
                </FormField>
              </>
            ),
          },
        ]}
      />
    </>
  );
}
