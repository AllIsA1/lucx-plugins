// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { awgGroups, isAwgConfLink, isAwgLegacyLink } from '@/lib/sub/awgGroups';

const legacy = 'amneziawg://peerPriv@203.0.113.1:51820?address=10.8.0.2%2F32&jc=8#awg-user';
const legacy2 = 'amneziawg://peerPriv@198.51.100.7:443?address=10.8.0.2%2F32&jc=8#awg-user-2';
const vpn = 'vpn://AAAEtnicdFRhT9swEP0r1u1rWpqkRV2kfGBsWgENVYJp';
const vpn2 = 'vpn://AAAEtnicSECOND';
const conf = 'amneziawg://W0ludGVyZmFjZV0KUHJpdmF0ZUtleSA9IHBlZXJQcml2#awg-user';
const conf2 = 'amneziawg://W0ludGVyZmFjZV0KU0VDT05E#awg-user-2';
const vless = 'vless://u@host.example:443?type=tcp#one';

describe('telling the two amneziawg:// forms apart', () => {
  it('the config form has no credentials and no query', () => {
    expect(isAwgConfLink(conf)).toBe(true);
    expect(isAwgConfLink('amneziawg://W0ludGVyZmFjZV0')).toBe(true);
    expect(isAwgConfLink(legacy)).toBe(false);
    expect(isAwgConfLink(vpn)).toBe(false);
    expect(isAwgConfLink(vless)).toBe(false);
  });

  it('the body is base64url characters, and there is one', () => {
    expect(isAwgConfLink('amneziawg://')).toBe(false);
    expect(isAwgConfLink('amneziawg://#name')).toBe(false);
    expect(isAwgConfLink('amneziawg://ab-c_dEF0')).toBe(true);
    expect(isAwgConfLink('amneziawg://ab+c/d=')).toBe(false); // standard base64 is not what the panel writes
    expect(isAwgConfLink('amneziawg://ab c')).toBe(false);
  });

  it('the legacy form carries the key before @ and the settings after ?', () => {
    expect(isAwgLegacyLink(legacy)).toBe(true);
    expect(isAwgLegacyLink('amneziawg://key@host:1')).toBe(true);
    expect(isAwgLegacyLink('amneziawg://host:1?jc=8')).toBe(true);
    expect(isAwgLegacyLink(conf)).toBe(false);
    expect(isAwgLegacyLink(vpn)).toBe(false);
  });

  it('a # inside the name does not make a config link a legacy one', () => {
    expect(isAwgConfLink('amneziawg://W0ludGVy#name%3Fwith%40marks')).toBe(true);
    expect(isAwgLegacyLink('amneziawg://W0ludGVy#name%3Fwith%40marks')).toBe(false);
  });
});

describe('grouping the lines of one AmneziaWG node', () => {
  it('joins legacy, vpn:// and the config link into one group keyed by the vpn:// line', () => {
    const groups = awgGroups([vless, legacy, vpn, conf]);
    expect(groups.size).toBe(1);
    expect(groups.get(vpn)).toEqual({ vpn, legacy, conf });
  });

  it('keeps the groups of several nodes apart', () => {
    const groups = awgGroups([legacy, vpn, conf, legacy2, vpn2, conf2]);
    expect(groups.get(vpn)).toEqual({ vpn, legacy, conf });
    expect(groups.get(vpn2)).toEqual({ vpn: vpn2, legacy: legacy2, conf: conf2 });
  });

  it('knows the AmneziaWG module form: vpn:// and the config link only', () => {
    expect(awgGroups([vpn, conf]).get(vpn)).toEqual({ vpn, conf });
  });

  it('knows a subscription from before the config link existed', () => {
    expect(awgGroups([legacy, vpn]).get(vpn)).toEqual({ vpn, legacy });
    expect(awgGroups([vpn]).get(vpn)).toEqual({ vpn });
  });

  it('does not take a line that belongs to another node', () => {
    // legacy2 and conf of the first node must not be glued onto the second vpn:// line
    const groups = awgGroups([legacy, vpn, conf, vpn2]);
    expect(groups.get(vpn2)).toEqual({ vpn: vpn2 });
    const other = awgGroups([vpn, vless, conf]);
    expect(other.get(vpn)).toEqual({ vpn });
  });

  it('ignores lines that are not AmneziaWG', () => {
    expect(awgGroups([vless, 'trojan://p@h:443#t']).size).toBe(0);
    expect(awgGroups([]).size).toBe(0);
  });

  it('does not use a legacy line that is not right before the vpn:// line', () => {
    expect(awgGroups([legacy, vless, vpn]).get(vpn)).toEqual({ vpn });
  });
});
