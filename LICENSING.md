# Licensing

## The plugin system is MIT: use it however you like

Everything this repository adds (the plugin system and its service binary `lucx-plugin-host`, the installer, the build tooling, the examples) is under the
[MIT License](LICENSE). The author does not mind how you use it: personal or commercial, modified or not, no permission needed.
Keep the copyright notice.

## But it is an add-on to LucX-UI: follow LucX-UI's licenses

The plugin system does nothing without [LucX-UI](https://github.com/AlexeyLCP/lucx-ui), and a build of it *contains* LucX-UI.
LucX-UI is a separate project by a separate, independent author, and it builds on
[3x-ui](https://github.com/MHSanaei/3x-ui). Both have their own licenses (3x-ui and the code LucX-UI took from it: GPL-3.0; the
components LucX-UI wrote itself: its own terms, including limits on commercial use). **To install and use the plugin system you
need LucX-UI and you must comply with its licenses.** This repository grants no rights in LucX-UI or 3x-ui and changes none of
them.

## What stays under other licenses

- `overlay/base/` and `overlay/modified/` are copies of seventeen LucX-UI/3x-ui source files: `base/` as they are upstream,
  `modified/` with the small integration hooks the plugin system needs (marked `LUCX-HOOK`). They keep the license of the file
  they come from (GPL-3.0, text in [LICENSE-GPL-3.0.txt](LICENSE-GPL-3.0.txt), unless a file says otherwise).
- Plugins installed through the plugin system are separate programs under their authors' licenses.

## Builds and their source

A build published here is a LucX-UI release built from its published source with the overlay in this repository laid over it. Each
release names the upstream tag and commit and the overlay commit it was made from, with links: together they are the complete
source of that build, and `scripts/dev.sh` rebuilds the tree from them.

A helper program released here (`sidecar-<name>-<version>-<n>`, today the OpenFlux exit) is the unchanged source of its authors
(OpenFlux: GPL-3.0-or-later, https://github.com/p1neappleXpress/OpenFlux) built by `scripts/build-sidecar.sh`; the release names
the upstream tag and commit, which are the complete source, and the license of the program is the one its authors chose.

This text states the author's intent; it is not legal advice.
