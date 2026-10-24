# lucx-plugins

The plugin system for the [LucX-UI](https://github.com/AlexeyLCP/lucx-ui) panel, kept as an **overlay**: this repository is not
a fork. It holds what the plugin system adds to a LucX-UI release, and a build that lays it over each upstream release.

## What a user gets

One command on a server that already runs the panel. Afterwards the panel has a **Plugins** page, plugins run as services of
their own, and the plugin system updates from a button on that page without restarting the panel. Updating the panel the usual
way (`x-ui update`) keeps the plugin system. Removing it puts the original panel binary back; the panel's own data is never
touched.

## How it works: two parts

**The panel build.** The panel is one compiled binary with its web interface embedded, so what the plugin system needs inside
it arrives as a rebuilt binary. That part is small: the Plugins page and the menu entries and pages plugins add, a proxy that
forwards `/panel/api/plugins/*` and plugin pages to the service, a socket on which the service calls the panel's API for a plugin
(only the routes that plugin was granted), and the hooks that hand panel events and the generated Xray configuration to the
service. This repository builds it for every upstream release (upstream source plus the overlay) and publishes a signed list
mapping the hash of each original `x-ui` to the matching build. On the server a small program recognises the original by its hash
and swaps in the build. A binary it does not recognise is never replaced.

**The plugin system service** (`lucx-plugins-host.service`, the binary `lucx-plugin-host`). Everything else: installing, running
and confining plugins, permissions, the catalog, updates of plugins, the management of plugins on nodes. It is a separate
program with its own releases (`host-N`, recorded in the signed `host.json`), so it can be updated, by the button on the Plugins
page, without rebuilding or restarting the panel. A new binary that fails to start is replaced by the previous one. Without the
service the panel is exactly the panel it would be without plugins.

## Installing

On a server that already runs LucX-UI under systemd, as root:

```
curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/install.sh | sudo bash
```

Pass options after `bash -s --`, for example `… | sudo bash -s -- --no-restart`. (Do not use `sudo bash <(curl …)`: sudo does not pass that file
descriptor on. As root, drop the `sudo`.) Options: `--service NAME` (default `x-ui`), `--panel PATH`, `--restart-at HH:MM` (default `04:00`),
`--no-restart`, `--gateway 127.0.0.1:PORT` (where plugins reach the panel's API; loopback only, default `127.0.0.1:19480`).

It installs, in `/usr/local/lucx-plugins`:

- `lucx-plugins-sync`, the program that keeps the panel binary in step with the signed list. A systemd drop-in
  `x-ui.service.d/30-lucx-plugins.conf` runs it before every start of the panel (`ExecStartPre=-+…`: it cannot stop the panel
  from starting); two timers check for new builds every 30 minutes and restart the panel at the quiet hour when a build is waiting.
- `lucx-plugin-host`, the service, fetched by `lucx-plugins-sync host-install` from the signed `host.json` (signature, hash and a
  trial run are checked) and started as `lucx-plugins-host.service`. Its unit restarts it if it stops, leaves plugin processes
  running when it stops, and runs `lucx-plugins-sync host-guard` before each start to roll back a failed update.

If the service cannot be installed the installer stops before it changes anything else. After `x-ui update` the new original panel
binary is recognised at the next start and swapped again, or, if our build for that release is not published yet, at the first
quiet hour after it is.

```
curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/uninstall.sh | sudo bash -s -- [--purge]
```

stops the service and the plugins it started, puts the original binary back (from the copy it kept, or from the upstream release,
checked against the signed hash) and removes everything it installed. Plugins and their data stay unless `--purge` is given. The
panel's own data is never touched.

**Updating the panel's plugin build by hand.** The timers do it by themselves (a check every 30 minutes, the restart at the quiet hour),
but an operator can ask for it now:

```bash
sudo lucx-plugins update               # read the signed list, get the newest build, restart the panel onto it, check it is in place
sudo lucx-plugins update --no-restart  # only get the build ready; the next panel start installs it
sudo lucx-plugins status               # what is installed, whether a build waits, the last problem
```

`lucx-plugins` is a link to `lucx-plugins-sync` that the installer creates in `/usr/local/bin` (a file of that name that is not ours
is left alone, and the uninstaller removes only its own link). Without a newer build nothing is restarted. A list that does not verify,
a panel binary that is not a known build, or a restart that does not install the build are reported as errors with the reason.
Running `install.sh` again does the same and also refreshes the program itself. The plugin system service is updated from the Plugins page.

`lucx-plugins-sync status` says what it did to the panel binary and the last problem it met; `systemctl status
lucx-plugins-host` and `journalctl -u lucx-plugins-host` say how the service is doing. `/etc/lucx-plugins/config` (`PANEL_BINARY`,
`SERVICE`, `STATE_DIR`, `RESTART_AT`, `INDEX_URL`) and `/etc/lucx-plugins/host.env` (`LUCX_HOST_GATEWAY`) are created once and then
left alone; `LUCX_PLUGINS_INDEX_URL` or `INDEX_URL` points the program at a mirror of the signed list. Plugins installed with the
earlier, built-in version of the plugin system are not migrated: install them again from the Plugins page.

### What it trusts

- A panel binary is replaced only if its sha256 is listed as an upstream original in `builds.json`, the list's ed25519 signature
  verifies against the keys built into the program (`keys/builds.pub`), and the build's archive and binary hashes match the list.
  Any other binary, including your own build, is never touched.
- The service binary comes only from `host.json`, signed by the same keys under a different domain (a signature of one list is
  never valid for the other), whose `sequence` only grows and whose releases are never rewritten. The hash is checked and the
  binary must print the version the list names before it is installed or replaces the running one.
- The lists only ever grow: an older, validly signed list is ignored. The last good `builds.json` is cached, so the check keeps
  working offline.
- `install.sh` and `lucx-plugins-sync` are downloaded over HTTPS from this repository, the same trust the panel's own installer has
  in its repository; everything the sync program then installs is verified by signature.

Limits: systemd only (no OpenRC, no Docker yet); the panel restarts once when the plugin build arrives, at the chosen hour unless
the installer finds a build right away.

## The overlay

```
overlay/BASE         the upstream release the overlay was made from
overlay/files/       files the plugin system adds to the panel (the link to the service, the Plugins page, tests)
overlay/modified/    the plugin system's version of each upstream file it changes (17, small hook blocks marked LUCX-HOOK)
overlay/base/        the upstream version of those files the changes were made against (common ancestor for the merge)
overlay/locales/     only the translation keys the plugin system adds, one file per language
overlay/exclude.txt  paths that are never exported or applied
```

`scripts/apply.sh <tree>` lays the overlay over an upstream checkout: new files are copied (an existing path is a collision),
changed files are three-way merged (so unrelated upstream changes in the same file are kept; a real conflict fails and names the
file), locale files get the keys added. It never guesses.

## Working on it

```
scripts/dev.sh [<tag> [<dir>]]      clone an upstream release and apply the overlay
  … edit, build and test the tree the way the panel project does …
scripts/export.sh <dir>             write the result back into overlay/
scripts/generate.sh <dir>           rebuild the generated OpenAPI document and schemas (the overlay does not carry them)
tests/run.sh                        round trip (apply, then export, must reproduce the overlay), collisions, conflicts, merges
```

`LUCX_UPSTREAM` points the scripts at another copy of the upstream repository (a path or URL).

The service itself lives in this repository's own Go packages (`cmd/lucx-plugin-host`, `internal/plugins`, `internal/hostapi`,
`internal/hostupdate`); the examples, the plugin documentation and the catalog tool (`cmd/plugincatalog`) are here too. Tests of this
repository also keep the two sides in step by reading the overlay: the routes the panel forwards, the pages a plugin may replace,
the icons the examples use, the calls a master may make on a node.

## Automatic builds of the panel

`.github/workflows/build.yml` runs every hour. For each upstream release newer than `build/since` that has no build yet it applies
the overlay, builds the frontend, runs the tests, builds static `amd64` and `arm64` binaries the way the upstream release workflow
does, checks the original release against its `.sha256`, and packs a reproducible tarball. When publishing is on it creates the
release `<upstream tag>-plugins.<n>` and adds both builds to `builds.json` on the `builds` branch, signed with the key in the
`BUILDS_SIGNING_KEY` secret (the matching public keys are in `keys/builds.pub`). A failed build opens an issue.

Publishing needs the repository variable `PUBLISH_ENABLED=true`; a manual run also has to pass `publish`. `workflow_dispatch` with
`tag` builds one release without publishing. `builds.json` is managed with `go run ./cmd/buildsctl`; its entries map the hash of an
original `x-ui` to our build of the same release, and the sequence number only grows.

## Releases of the service

`.github/workflows/host.yml` builds and tests the service on every change to its code. A release is made **by hand**: run the
workflow with `publish` on (it also needs `PUBLISH_ENABLED=true` and the `BUILDS_SIGNING_KEY` secret). It builds static, reproducible
`amd64` and `arm64` binaries named `host-N` (`scripts/build-host.sh`), creates the release `host-N` and records it in the signed
`host.json` on the `builds` branch (`scripts/host-list.sh`, which touches the branch only after the signature verifies against
`keys/builds.pub`). Servers update to it from the Plugins page; a release is never changed afterwards.

## License

MIT: the author does not mind how you use the plugin system. It is an add-on for LucX-UI, which has its own, separate licenses
(and is by a separate, independent author): to install it you need LucX-UI, and you must follow its licenses. Details:
[LICENSING.md](LICENSING.md).
