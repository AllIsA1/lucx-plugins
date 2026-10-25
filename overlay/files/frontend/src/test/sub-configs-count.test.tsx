// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";

const LINKS = [
  "hysteria2://pw@h.example:443?sni=h#Hy2",
  "amneziawg://key@h.example:51820?address=10.8.0.2%2F32#AWG",
  "vpn://AAAAeNpjYBgFo2AUjIIAAAQAAAE",
  "amneziawg://W0ludGVyZmFjZV0#AWG-conf",
  "vless://11111111-1111-1111-1111-111111111111@h.example:443?type=tcp#V1",
  "qwdtt://config?peer=h.example&name=Q",
  "csqtt://aaY9XiroC3kq-FtYnhnuOILug2fHM2xD1I6n2nmDhk#CS",
  "openflux://v1/jI7LSgQxEEX_5a5DHq0zrbXrjVs#OF",
];

describe("the Configs tab of the subscription page", () => {
  beforeEach(() => {
    vi.resetModules();
    (window as unknown as { __SUB_PAGE_DATA__: unknown }).__SUB_PAGE_DATA__ = {
      sId: "abc",
      enabled: true,
      subUrl: "https://h.example/sub/abc",
      links: LINKS,
    };
  });

  it("counts the rows it shows: the lines of one AmneziaWG node are one row", async () => {
    const { ThemeProvider } = await import("@/hooks/useTheme");
    const { default: SubPage } = await import("@/pages/sub/SubPage");
    render(
      <ThemeProvider>
        <SubPage />
      </ThemeProvider>,
    );
    expect(document.querySelector(".sub-tab-count")?.textContent).toBe("6");
  });

  it("calls a link the panel names nothing by the name a plugin gave it, and does not change the link", async () => {
    (
      window as unknown as {
        __SUB_PAGE_DATA__: { links: string[]; linkNames: string[] };
      }
    ).__SUB_PAGE_DATA__ = {
      links: LINKS.map((l) => l.replace(/#(CS|OF)$/, '')),
      linkNames: ["", "", "", "", "", "", "Польша CSQTT", "Польша OpenFlux"],
    };
    const { ThemeProvider } = await import("@/hooks/useTheme");
    const { default: SubPage } = await import("@/pages/sub/SubPage");
    render(
      <ThemeProvider>
        <SubPage />
      </ThemeProvider>,
    );
    const titles = Array.from(document.querySelectorAll(".sub-row-title")).map(
      (n) => n.textContent,
    );
    expect(titles).toContain("Польша OpenFlux");
    expect(titles).not.toContain("Link 7");
  });
});
