// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import SubConfigsTab from '@/pages/sub/SubConfigsTab';
import { vpnUriFromConf } from '@/lib/awg/vpnuri';

import { renderWithProviders } from './test-utils';

const CONF = (name: string, host: string) =>
  `# ${name}\n[Interface]\nPrivateKey = peerPriv\nAddress = 10.8.0.2/32\nMTU = 1320\nJc = 8\nJmin = 50\nJmax = 200\n\n[Peer]\nPublicKey = serverPub\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = ${host}:51820\n`;

const toBase64Url = (s: string) =>
  btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

function node(name: string, host: string) {
  const conf = CONF(name, host);
  return {
    conf,
    legacy: `amneziawg://peerPriv@${host}:51820?address=10.8.0.2%2F32&jc=8#${name}`,
    vpn: vpnUriFromConf(conf),
    confLink: `amneziawg://${toBase64Url(conf)}#${name}`,
  };
}

const vless = 'vless://u@host.example:443?type=tcp&security=none#plain';

function renderTab(links: string[], onCopy = vi.fn()) {
  renderWithProviders(<SubConfigsTab links={links} onCopy={onCopy} />);
  return onCopy;
}

// the copy button of the nth AmneziaWG row
const rowCopy = (n = 0) =>
  within(screen.getAllByRole('radiogroup')[n].closest('.sub-row') as HTMLElement).getByRole(
    'button',
    { name: 'Copy' },
  );

const formatGroup = () => screen.queryByRole('radiogroup', { name: /AmneziaWG link format/i });

describe('the AmneziaWG row of the subscription page', () => {
  it('shows one row with a format switch where there used to be one row for the vpn:// line', async () => {
    const a = node('awg-one', '203.0.113.1');
    renderTab([vless, a.legacy, a.vpn, a.confLink]);
    const group = formatGroup();
    expect(group).not.toBeNull();
    await waitFor(() => expect(within(group as HTMLElement).queryByText('.conf')).not.toBeNull());
    for (const label of ['vpn://', 'amneziawg://', 'amneziawg:// (legacy)', '.conf']) {
      expect(within(group as HTMLElement).queryByText(label), label).not.toBeNull();
    }
    // the plain link keeps its own row, the three AmneziaWG lines are one row
    expect(screen.getAllByRole('radiogroup')).toHaveLength(1);
  });

  it('offers the .conf only once the config has been read out of the vpn:// link', async () => {
    const a = node('awg-one', '203.0.113.1');
    renderTab([a.legacy, a.vpn, a.confLink]);
    expect(screen.queryByText('.conf')).toBeNull();
    await waitFor(() => expect(screen.queryByText('.conf')).not.toBeNull());
  });

  it('copies the vpn:// link first, then whatever format is chosen', async () => {
    const a = node('awg-one', '203.0.113.1');
    const onCopy = renderTab([a.legacy, a.vpn, a.confLink]);
    await waitFor(() => expect(screen.queryByText('.conf')).not.toBeNull());
    const copy = () => rowCopy();
    fireEvent.click(copy());
    expect(onCopy).toHaveBeenLastCalledWith(a.vpn);

    fireEvent.click(screen.getByText('amneziawg://'));
    fireEvent.click(copy());
    expect(onCopy).toHaveBeenLastCalledWith(a.confLink);

    fireEvent.click(screen.getByText('amneziawg:// (legacy)'));
    fireEvent.click(copy());
    expect(onCopy).toHaveBeenLastCalledWith(a.legacy);

    fireEvent.click(screen.getByText('.conf'));
    fireEvent.click(copy());
    await waitFor(() =>
      expect(onCopy).toHaveBeenLastCalledWith(expect.stringContaining('[Interface]')),
    );
    expect(onCopy.mock.lastCall?.[0]).toContain('Endpoint = 203.0.113.1:51820');
  });

  it('offers a QR for the links but not for the .conf text', async () => {
    const a = node('awg-one', '203.0.113.1');
    renderTab([a.legacy, a.vpn, a.confLink]);
    await waitFor(() => expect(screen.queryByText('.conf')).not.toBeNull());
    const row = () => screen.getByRole('radiogroup').closest('.sub-row') as HTMLElement;
    expect(within(row()).queryByRole('button', { name: 'QR' })).not.toBeNull();
    fireEvent.click(screen.getByText('.conf'));
    expect(within(row()).queryByRole('button', { name: 'QR' })).toBeNull();
    fireEvent.click(screen.getByText('vpn://'));
    expect(within(row()).queryByRole('button', { name: 'QR' })).not.toBeNull();
  });

  it('offers only the formats the subscription carries', async () => {
    const a = node('awg-native', '203.0.113.7');
    renderTab([a.vpn, a.confLink]);
    const group = formatGroup() as HTMLElement;
    await waitFor(() => expect(within(group).queryByText('.conf')).not.toBeNull());
    expect(within(group).queryByText('amneziawg:// (legacy)')).toBeNull();
    expect(within(group).queryByText('amneziawg://')).not.toBeNull();
  });

  it('does not offer the config form when the subscription has no such line', async () => {
    const a = node('awg-one', '203.0.113.1');
    renderTab([a.legacy, a.vpn]);
    const group = formatGroup() as HTMLElement;
    await waitFor(() => expect(within(group).queryByText('.conf')).not.toBeNull());
    expect(within(group).queryByText('amneziawg://')).toBeNull();
    expect(within(group).queryByText('amneziawg:// (legacy)')).not.toBeNull();
  });

  it('falls back to vpn:// when the chosen form is gone after the subscription is refreshed', async () => {
    const a = node('awg-one', '203.0.113.1');
    const onCopy = vi.fn();
    function Holder() {
      const [links, setLinks] = useState([a.legacy, a.vpn, a.confLink]);
      return (
        <>
          <button onClick={() => setLinks([a.vpn, a.confLink])}>refresh</button>
          <SubConfigsTab links={links} onCopy={onCopy} />
        </>
      );
    }
    renderWithProviders(<Holder />);
    fireEvent.click(screen.getByText('amneziawg:// (legacy)'));
    fireEvent.click(screen.getByText('refresh'));
    expect(screen.queryByText('amneziawg:// (legacy)')).toBeNull();
    fireEvent.click(rowCopy());
    expect(onCopy).toHaveBeenLastCalledWith(a.vpn);
  });

  it('still offers vpn:// and the .conf when the subscription has only the vpn:// line', async () => {
    const a = node('awg-old', '203.0.113.9');
    renderTab([a.vpn]);
    await waitFor(() => expect(screen.queryByText('.conf')).not.toBeNull());
    const group = screen.getByRole('radiogroup');
    expect(within(group).queryByText('amneziawg://')).toBeNull();
    expect(within(group).queryByText('vpn://')).not.toBeNull();
  });

  it('remembers the format of each node apart from the others', async () => {
    const a = node('awg-one', '203.0.113.1');
    const b = node('awg-two', '198.51.100.7');
    const onCopy = renderTab([a.legacy, a.vpn, a.confLink, b.legacy, b.vpn, b.confLink]);
    expect(screen.getAllByRole('radiogroup')).toHaveLength(2);
    await waitFor(() => expect(screen.getAllByText('.conf')).toHaveLength(2));
    const groups = screen.getAllByRole('radiogroup');
    fireEvent.click(within(groups[0]).getByText('amneziawg://'));
    fireEvent.click(rowCopy(0));
    expect(onCopy).toHaveBeenLastCalledWith(a.confLink);
    fireEvent.click(rowCopy(1));
    expect(onCopy).toHaveBeenLastCalledWith(b.vpn);
  });

  it('shows no separate config block under the row: the .conf is a format of the switch', async () => {
    const a = node('awg-one', '203.0.113.1');
    renderTab([a.legacy, a.vpn, a.confLink]);
    await waitFor(() => expect(screen.queryByText('.conf')).not.toBeNull());
    expect(screen.queryByText(/AmneziaWG config/i)).toBeNull();
    expect(document.querySelector('.config-block')).toBeNull();
  });
});
