// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Segmented, Tag } from 'antd';
import { CopyOutlined } from '@ant-design/icons';

import { vpnConfFromLink } from '@/lib/awg/vpnuri';
import type { AwgLinkGroup } from '@/lib/sub/awgGroups';
import { LinkTags, type LinkParts } from '@/lib/xray/link-label';

import SubQrButton from './SubQrButton';

type Format = 'vpn' | 'conf-link' | 'legacy' | 'conf';

interface AwgFormatRowProps {
  group: AwgLinkGroup;
  parts: LinkParts | null;
  rowTitle: string;
  onCopy: (value: string) => void;
}

// One AmneziaWG node: the forms the subscription carries, with a switch for the one to copy.
export default function AwgFormatRow({ group, parts, rowTitle, onCopy }: AwgFormatRowProps) {
  const { t } = useTranslation();
  const [format, setFormat] = useState<Format>('vpn');
  const [confText, setConfText] = useState('');

  useEffect(() => {
    let cancelled = false;
    void vpnConfFromLink(group.vpn).then((conf) => {
      if (!cancelled && conf) setConfText(conf);
    });
    return () => {
      cancelled = true;
    };
  }, [group.vpn]);

  const values: Record<Format, string> = {
    vpn: group.vpn,
    'conf-link': group.conf ?? '',
    legacy: group.legacy ?? '',
    conf: confText,
  };
  const options: { label: string; value: Format }[] = [{ label: 'vpn://', value: 'vpn' }];
  if (group.conf) options.push({ label: 'amneziawg://', value: 'conf-link' });
  if (group.legacy) options.push({ label: t('subscription.awgFormatLegacy'), value: 'legacy' });
  if (confText) options.push({ label: '.conf', value: 'conf' });
  const shown: Format = values[format] ? format : 'vpn';
  const value = values[shown];

  return (
    <div className="sub-row sub-row-awg">
      {parts ? <LinkTags parts={parts} /> : <Tag className="sub-row-tag">LINK</Tag>}
      <span className="sub-row-title" dir="auto" title={rowTitle}>
        {rowTitle}
      </span>
      <Segmented<Format>
        className="sub-row-formats"
        size="small"
        aria-label={t('subscription.awgFormatLabel')}
        value={shown}
        onChange={setFormat}
        options={options}
      />
      <div className="sub-row-actions">
        <Button
          icon={<CopyOutlined />}
          onClick={() => onCopy(value)}
          aria-label={t('copy')}
          title={t('copy')}
        />
        {shown !== 'conf' && <SubQrButton value={value} label={rowTitle} onCopy={onCopy} />}
      </div>
    </div>
  );
}
