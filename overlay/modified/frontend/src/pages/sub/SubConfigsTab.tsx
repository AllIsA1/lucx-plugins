import { Fragment } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Tag } from 'antd';
import { CopyOutlined } from '@ant-design/icons';

import ConfigBlock from '@/components/clients/ConfigBlock';
import { isPostQuantumLink, wireguardConfigFromLink } from '@/lib/xray/inbound-link';
import { LinkTags, displaySubLinks } from '@/lib/xray/link-label';
import { awgGroups } from '@/lib/sub/awgGroups'; // LUCX-HOOK: one row per AmneziaWG node with a format switch
import AwgFormatRow from './AwgFormatRow'; // LUCX-HOOK
import SubQrButton from './SubQrButton';

interface SubConfigsTabProps {
  links: string[];
  onCopy: (value: string, toast?: string) => void;
}

export default function SubConfigsTab({ links, onCopy }: SubConfigsTabProps) {
  const { t } = useTranslation();
  const awgByVpn = awgGroups(links); // LUCX-HOOK

  return (
    <div className="sub-rows">
      <div className="sub-configs-bar">
        <Button
          icon={<CopyOutlined />}
          onClick={() => onCopy(links.join('\n'), t('subscription.copyAllConfigsCopied'))}
        >
          {t('subscription.copyAllConfigs')}
        </Button>
      </div>
      {displaySubLinks(links).map(({ link, parts }, idx) => {
        const rowTitle = parts?.remark || `Link ${idx + 1}`;
        const isWireguardLink = link.startsWith('wireguard://') || link.startsWith('wg://');
        const awgForms = awgByVpn.get(link); // LUCX-HOOK: every AmneziaWG node gets the switch (its .conf is one of the forms)
        return (
          <Fragment key={link}>
            {awgForms ? ( // LUCX-HOOK
              <AwgFormatRow group={awgForms} parts={parts} rowTitle={rowTitle} onCopy={onCopy} />
            ) : (
              <div className="sub-row">
                {parts ? <LinkTags parts={parts} /> : <Tag className="sub-row-tag">LINK</Tag>}
                <span className="sub-row-title" dir="auto" title={rowTitle}>
                  {rowTitle}
                </span>
                <div className="sub-row-actions">
                  <Button
                    icon={<CopyOutlined />}
                    onClick={() => onCopy(link)}
                    aria-label={t('copy')}
                    title={t('copy')}
                  />
                  {!isPostQuantumLink(link) && (
                    <SubQrButton value={link} label={rowTitle} onCopy={onCopy} />
                  )}
                </div>
              </div>
            )}
            {isWireguardLink && (
              <ConfigBlock
                label={t('pages.clients.wireguardConfig')}
                text={wireguardConfigFromLink(link, rowTitle)}
                fileName={`${rowTitle || 'peer'}.conf`}
                qrRemark={rowTitle}
                tagColor="cyan"
              />
            )}
          </Fragment>
        );
      })}
    </div>
  );
}
