// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import { useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Spin } from 'antd';
import { useQuery } from '@tanstack/react-query';

import { httpRequest, withBasePath } from '@/api/http-init';
import { pluginsApi } from '@/api/plugins';
import { keys } from '@/api/queryKeys';
import { useTheme } from '@/hooks/useTheme';
import { createBridge, type Bridge, type BridgeContext } from '@/pages/plugins/pluginBridge';
import './PluginFrame.css';

interface Props {
  pluginId: string;
  path: string;
  title?: string;
}

export default function PluginFrame({ pluginId, path, title }: Props) {
  const { t, i18n } = useTranslation();
  // The sandboxed page has no cookie, so its URL carries a token; one token per mounted plugin keeps the page from reloading.
  const tokenQuery = useQuery({
    queryKey: keys.plugins.frameToken(pluginId),
    queryFn: () => pluginsApi.frameToken(pluginId),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const token = tokenQuery.data?.success ? tokenQuery.data.obj?.token : undefined;
  const { isDark, isUltra, palette } = useTheme();
  const frameRef = useRef<HTMLIFrameElement>(null);
  const bridgeRef = useRef<Bridge | null>(null);
  const context = useMemo<BridgeContext>(
    () => ({ theme: { dark: isDark, ultra: isUltra, palette }, lang: i18n.language }),
    [isDark, isUltra, palette, i18n.language],
  );
  const contextRef = useRef(context);

  useEffect(() => {
    const bridge = createBridge({
      pluginId,
      frame: () => frameRef.current?.contentWindow ?? null,
      context: () => contextRef.current,
      request: httpRequest,
    });
    bridgeRef.current = bridge;
    const onMessage = (event: MessageEvent) => void bridge.handleMessage(event);
    window.addEventListener('message', onMessage);
    return () => {
      window.removeEventListener('message', onMessage);
      bridge.dispose();
      bridgeRef.current = null;
    };
  }, [pluginId]);

  useEffect(() => {
    contextRef.current = context;
    bridgeRef.current?.pushContext();
  }, [context]);

  if (!token) {
    return tokenQuery.isLoading ? (
      <Spin size="large" style={{ margin: 'auto' }} />
    ) : (
      <Alert type="warning" showIcon message={t('pages.plugins.frame.unavailable')} />
    );
  }
  // No allow-same-origin: the page gets an opaque origin and never sees the panel's cookies.
  return (
    <iframe
      ref={frameRef}
      key={`${pluginId}${path}`}
      className="plugin-frame"
      title={title ?? pluginId}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      src={withBasePath(`/panel/plugin-frame/${token}${path}`)}
      onLoad={() => bridgeRef.current?.pushContext()}
    />
  );
}
