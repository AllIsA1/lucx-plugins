// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useTranslation } from 'react-i18next';
import { Tag, Typography } from 'antd';

import { isDangerousPermission, permissionKind } from '@/pages/plugins/permissions';

export default function PermissionList({ permissions }: { permissions: string[] }) {
  const { t } = useTranslation();
  if (permissions.length === 0) {
    return <Typography.Text type="secondary">{t('pages.plugins.noPermissions')}</Typography.Text>;
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {permissions.map((permission) => {
        const kind = permissionKind(permission);
        return (
          <div key={permission}>
            <Tag color={isDangerousPermission(permission) ? 'red' : 'default'}>{permission}</Tag>
            <Typography.Text type="secondary">{t(`pages.plugins.perm.${kind}`)}</Typography.Text>
          </div>
        );
      })}
    </div>
  );
}
