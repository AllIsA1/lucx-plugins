# Licensing

This repository is **the LucX plugin system and nothing else**. It is an independent project. It is not part of
[LucX-UI](https://github.com/AlexeyLCP/lucx-ui) or [3x-ui](https://github.com/MHSanaei/3x-ui), and the authors of those
projects are separate from, and independent of, the author of this repository. This license grants rights in the plugin system
only; it grants none in LucX-UI or 3x-ui.

## 1. What this license covers

Required Notice: Copyright (c) 2026 AllIsA1 (https://github.com/AllIsA1/lucx-plugins)

| What | License |
|---|---|
| The plugin system that this repository adds to a panel: `overlay/files/` except `examples/` (backend in `internal/lucx/plugins/`, API and services, the Plugins page, the catalog tool `tools/plugincatalog/`, the guides in `docs/`) | **PolyForm Noncommercial 1.0.0**, [LICENSE-PolyForm-Noncommercial.txt](LICENSE-PolyForm-Noncommercial.txt) |
| The tooling of this repository: `cmd/`, `internal/`, `keys/`, `scripts/`, `installer/`, `tests/`, `.github/` | **PolyForm Noncommercial 1.0.0** |
| The plugin examples and the page SDK: `overlay/files/examples/` (each directory has its own `LICENSE`) | **MIT**: meant to be copied; a plugin built from them is under its author's own license |

PolyForm Noncommercial in practice: free for personal, educational, research, charity and government use. **Commercial use of the
plugin system, including reselling VPN access built on it, needs the written permission of this repository's owner.** That
permission covers the plugin system only and says nothing about LucX-UI or 3x-ui.

Every such file carries an SPDX header (`PolyForm-Noncommercial-1.0.0` or `MIT`) naming the copyright holder.

## 2. What it does not cover

- **LucX-UI** is by a separate author and has its own licenses (GPL-3.0 for the code it took from 3x-ui, PolyForm Noncommercial
  for its own components). Using or selling LucX-UI is governed by those licenses and needs that author's permission where they
  say so, whatever this repository's license says.
- **3x-ui** is GPL-3.0 ([LICENSE-GPL-3.0.txt](LICENSE-GPL-3.0.txt) is its text).
- `overlay/base/` and `overlay/modified/` are copies of about twenty LucX-UI/3x-ui source files: `base/` as they are upstream,
  `modified/` with the small integration hooks the plugin system needs (marked `LUCX-HOOK`). They stay under the license of the
  file they come from (GPL-3.0 unless a file says otherwise); this repository claims no new rights in them.
- **Plugins** installed through the plugin system are separate programs; each is under its author's license.

## 3. Builds

A build published from this repository is a LucX-UI release, built from its published source, with the plugin system laid over
it. It therefore contains code under all of the licenses above: the panel's own (as above) and this plugin system's
(PolyForm Noncommercial). Each release says which upstream tag it was made from. A build gives no rights beyond what each of
those licenses gives; in particular, commercial use is restricted by LucX-UI's license as well as this one.

This text describes the intent of the author; it is not legal advice.
