# lucx-plugins

The plugin system for the [LucX-UI](https://github.com/AlexeyLCP/lucx-ui) panel, kept as an **overlay**: this repository is not
a fork. It holds what the plugin system adds to a LucX-UI release, and a build that lays it over each upstream release.

> Status: stages 7a–7c of the plugin system (the overlay, the automatic build and signed list, the installer). Publishing builds is
> still switched off, so the installer has nothing to download yet; do not use it on a real server until this note is gone.

## What a user will get

One command on a server that already runs the panel, then a restart, and the panel has a **Plugins** page. Updating the panel
the usual way (`x-ui update`) keeps the plugin system. Removing it puts the original panel binary back; the panel's own data is
never touched. (Installer: stage 7c.)

## How it works

The panel is one compiled binary with its web interface embedded, so the plugin system can only arrive as a rebuilt binary. This
repository builds it automatically for every upstream release from the upstream source plus the overlay, and publishes a signed
list that maps the hash of each original `x-ui` to the matching build. On the server a small script recognises the original by
its hash and swaps in the build with plugins. A binary it does not recognise is never replaced.

## Installing (when builds are published)

On a server that already runs LucX-UI under systemd, as root:

```
curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/install.sh | sudo bash
```

Pass options after `bash -s --`, for example `… | sudo bash -s -- --no-restart`. (Do not use `sudo bash <(curl …)`: sudo does not pass that file
descriptor on. As root, drop the `sudo`.) Options: `--service NAME` (default `x-ui`), `--panel PATH`, `--restart-at HH:MM` (default `04:00`), `--no-restart`.

It installs `lucx-plugins-sync` in `/usr/local/lucx-plugins`, a systemd drop-in `x-ui.service.d/30-lucx-plugins.conf` that runs it
before every start of the panel (`ExecStartPre=-+…`: it cannot stop the panel from starting), and two timers: one checks for new
builds every 30 minutes, the other restarts the panel at the quiet hour when a build is waiting. If a build for the installed panel
exists, the panel restarts once and has the **Plugins** page. After `x-ui update` the new original is recognised at the next start and
swapped again, or, if our build for that release is not published yet, at the first quiet hour after it is.

```
curl -fL https://raw.githubusercontent.com/AllIsA1/lucx-plugins/main/installer/uninstall.sh | sudo bash -s -- [--purge]
```

puts the original binary back (from the copy it kept, or from the upstream release, checked against the signed hash) and removes
everything it installed. Plugins and their data stay unless `--purge` is given. The panel's own data is never touched.

`lucx-plugins-sync status` says what it did and the last problem it met. `/etc/lucx-plugins/config` (`PANEL_BINARY`, `SERVICE`,
`STATE_DIR`, `RESTART_AT`, `INDEX_URL`) is created once and then left alone; `LUCX_PLUGINS_INDEX_URL` or `INDEX_URL` points the
program at a mirror of the signed list.

### What it trusts

- A panel binary is replaced only if its sha256 is listed as an upstream original in `builds.json`, the list's ed25519 signature
  verifies against the keys built into the program (`keys/builds.pub`), and the build's archive and binary hashes match the list.
  Any other binary, including your own build, is never touched.
- The list only ever grows (`sequence`): an older, validly signed list is ignored. The last good list is cached, so the check
  keeps working offline. A new binary must print its version before it is installed.
- `install.sh` and the program itself are downloaded over HTTPS from this repository, the same trust the panel's own installer
  has in its repository.

Limits: systemd only (no OpenRC, no Docker yet); the panel restarts once when the plugin build arrives, at the chosen hour unless
the installer finds a build right away.

## The overlay

```
overlay/BASE         the upstream release the overlay was made from
overlay/files/       files the plugin system adds (backend, pages, tools, docs, examples)
overlay/modified/    the plugin system's version of each upstream file it changes (about 20, small hook blocks marked LUCX-HOOK)
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

## Automatic builds

`.github/workflows/build.yml` runs every hour. For each upstream release newer than `build/since` that has no build yet it applies
the overlay, builds the frontend, runs the tests, builds static `amd64` and `arm64` binaries the way the upstream release workflow
does, checks the original release against its `.sha256`, and packs a reproducible tarball. When publishing is on it creates the
release `<upstream tag>-plugins.<n>` and adds both builds to `builds.json` on the `builds` branch, signed with the key in the
`BUILDS_SIGNING_KEY` secret (the matching public keys are in `keys/builds.pub`). A failed build opens an issue.

Publishing needs the repository variable `PUBLISH_ENABLED=true`; a manual run also has to pass `publish`. `workflow_dispatch` with
`tag` builds one release without publishing. `builds.json` is managed with `go run ./cmd/buildsctl`; its entries map the hash of an
original `x-ui` to our build of the same release, and the sequence number only grows.

## License

MIT: the author does not mind how you use the plugin system. It is an add-on for LucX-UI, which has its own, separate licenses
(and is by a separate, independent author): to install it you need LucX-UI, and you must follow its licenses. Details:
[LICENSING.md](LICENSING.md).
