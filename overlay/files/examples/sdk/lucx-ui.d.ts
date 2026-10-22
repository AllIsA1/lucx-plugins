// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

interface LucxContext {
  pluginId: string;
  theme: { dark: boolean; ultra: boolean; palette: string };
  lang: string;
}

interface LucxResult {
  ok: boolean;
  /** HTTP status of the plugin's answer; 0 when the call never reached it. */
  status: number;
  body?: unknown;
  error?: string;
}

declare const lucx: {
  ready(): Promise<LucxContext>;
  context(): LucxContext | null;
  onContext(callback: (context: LucxContext) => void): () => void;
  api(path: string, options?: { method?: 'GET' | 'POST' | 'PUT' | 'DELETE'; body?: unknown }): Promise<LucxResult>;
  applyTheme(root?: Element): void;
};
