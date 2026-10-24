// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// The panel hands one AmneziaWG node to a client as up to three lines that are the same connection in
// different forms, always in this order: the legacy amneziawg:// link (the key before @, the settings after
// ?), the vpn:// link (Amnezia's own), and amneziawg://<base64url .conf>#name (the config itself, which INCY
// imports). The subscription page shows them as one row with a switch between the forms.

export interface AwgLinkGroup {
  vpn: string;
  legacy?: string;
  conf?: string;
}

const SCHEME = 'amneziawg://';
const CONF_LINK = /^amneziawg:\/\/[A-Za-z0-9_-]+(#.*)?$/;

/** amneziawg://<base64url .conf>#name: a body of base64url characters only, no key@host and no query. */
export function isAwgConfLink(link: string): boolean {
  return CONF_LINK.test(link);
}

/** The older amneziawg://key@host:port?settings form. */
export function isAwgLegacyLink(link: string): boolean {
  return link.startsWith(SCHEME) && !isAwgConfLink(link);
}

/** The forms of each node, keyed by the node's vpn:// line. A line next to a vpn:// line joins it only in its place. */
export function awgGroups(links: string[]): Map<string, AwgLinkGroup> {
  const groups = new Map<string, AwgLinkGroup>();
  links.forEach((link, i) => {
    if (!link.startsWith('vpn://')) return;
    const group: AwgLinkGroup = { vpn: link };
    const before = links[i - 1];
    const after = links[i + 1];
    if (before !== undefined && isAwgLegacyLink(before)) group.legacy = before;
    if (after !== undefined && isAwgConfLink(after)) group.conf = after;
    groups.set(link, group);
  });
  return groups;
}
