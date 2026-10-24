// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useTranslation } from 'react-i18next';
import { Tag, Tooltip } from 'antd';

import type { PluginSeam } from '@/schemas/plugins';

// The places where a plugin takes part in what the panel does with a request.
export default function SeamList({ seams }: { seams: PluginSeam[] }) {
  const { t } = useTranslation();
  if (seams.length === 0) return null;
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, maxWidth: 360 }}>
      {seams.map((seam) => {
        const methods = seam.methods?.length
          ? seam.methods.join(', ')
          : t('pages.plugins.seamAllMethods');
        return (
          <Tooltip
            key={`${seam.engine}|${seam.mode}|${seam.route}|${methods}`}
            title={`${t(`pages.plugins.seamModeHint.${seam.mode}`)} (${methods})`}
          >
            <Tag style={{ marginInlineEnd: 0 }}>
              {t(`pages.plugins.seamEngine.${seam.engine}`)} ·{' '}
              {t(`pages.plugins.seamMode.${seam.mode}`)} · {seam.route}
            </Tag>
          </Tooltip>
        );
      })}
    </div>
  );
}
