// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useTranslation } from 'react-i18next';
import { Alert } from 'antd';

import type { Isolation, PluginsState } from '@/schemas/plugins';

type Warning = 'weak' | 'partial';

function warningFor(iso: Isolation | null | undefined): Warning | null {
  if (!iso) return null;
  if (!iso.user) return 'weak';
  return iso.network ? null : 'partial';
}

export default function ProcessesNotice({ state }: { state?: PluginsState }) {
  const { t } = useTranslation();
  const unavailable = state?.processesAvailable === false;
  const iso = state?.processesIsolation;
  const warning = unavailable ? null : warningFor(iso);
  const runNotice =
    iso?.runner === 'process' ? t('pages.plugins.runNoticeProcess') : t('pages.plugins.runNotice');
  return (
    <>
      <Alert
        type={unavailable ? 'warning' : 'info'}
        showIcon
        style={{ marginBottom: 12 }}
        message={
          unavailable
            ? t('pages.plugins.processesUnavailable', { error: state.processesError })
            : runNotice
        }
      />
      {warning === 'partial' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.isolation.partial')}
        />
      )}
      {warning === 'weak' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('pages.plugins.isolation.weak')}
        />
      )}
    </>
  );
}
