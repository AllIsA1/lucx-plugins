// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

const PREFIX = 'openflux://v1/';

export interface OpenfluxLinkInput {
  secret: string;
  host: string;
  port: number;
  remark?: string;
  yandexUrl?: string;
  mailruUrl?: string;
  cupsUrl?: string;
  volgaUrl?: string;
  boardUrl?: string;
  avitoUrl?: string;
  telemostUrl?: string;
}

// The carriers a link can hold besides direct, with the core's transport type and failover priority (higher is
// tried first). Same table as the backend's (internal/lucx/tunnel/openflux.go): the first one that is set is also
// the session's encryption context.
const CARRIERS: { type: string; priority: number; field: keyof OpenfluxLinkInput }[] = [
  { type: 'yandex', priority: 50, field: 'yandexUrl' },
  { type: 'mailru', priority: 40, field: 'mailruUrl' },
  { type: 'cupsonline', priority: 30, field: 'cupsUrl' },
  { type: 'vyandex', priority: 48, field: 'volgaUrl' },
  { type: 'boards', priority: 46, field: 'boardUrl' },
  { type: 'telemost', priority: 44, field: 'telemostUrl' },
  { type: 'avito', priority: 35, field: 'avitoUrl' },
];

/** Several links of one carrier go in one value, comma-separated, as the core reads them. */
export function openfluxLinks(raw: string | undefined): string {
  return (raw ?? '')
    .split(/[\s,|]+/)
    .filter((l) => l !== '')
    .join(',');
}

function carrierUrl(input: OpenfluxLinkInput, field: keyof OpenfluxLinkInput): string {
  const v = input[field];
  return typeof v === 'string' ? openfluxLinks(v) : '';
}

function storedDeflate(data: Uint8Array): Uint8Array {
  const blocks: number[] = [];
  let offset = 0;
  do {
    const len = Math.min(data.length - offset, 65535);
    const final = offset + len >= data.length;
    const nlen = len ^ 0xffff;
    blocks.push(final ? 1 : 0, len & 0xff, (len >> 8) & 0xff, nlen & 0xff, (nlen >> 8) & 0xff);
    for (let i = 0; i < len; i++) blocks.push(data[offset + i]);
    offset += len;
  } while (offset < data.length);
  return Uint8Array.from(blocks);
}

function b64url(bytes: Uint8Array): string {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function openfluxContext(input: OpenfluxLinkInput): string {
  for (const c of CARRIERS) {
    const url = carrierUrl(input, c.field);
    if (url) return url;
  }
  return 'http://openflux';
}

export function encodeOpenfluxLink(input: OpenfluxLinkInput): string {
  const secret = input.secret.trim();
  const host = input.host.trim();
  if (secret.length < 16 || !host || input.port < 1) return '';
  const transports: { type: string; priority: number; dial?: string; url?: string }[] = [
    { type: 'direct', priority: 100, dial: `${host}:${input.port}` },
  ];
  for (const c of CARRIERS) {
    const url = carrierUrl(input, c.field);
    if (url) transports.push({ type: c.type, priority: c.priority, url });
  }
  const payload = {
    name: input.remark?.trim() || undefined,
    negotiate: true,
    secret,
    context: openfluxContext(input),
    transports,
  };
  const raw = new TextEncoder().encode(JSON.stringify(payload));
  return PREFIX + b64url(storedDeflate(raw));
}
