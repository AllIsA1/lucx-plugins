# lucx-plugins

The plugin system for the [LucX-UI](https://github.com/AlexeyLCP/lucx-ui) panel, kept as an **overlay**: this repository is not
a fork. It holds what the plugin system adds to a LucX-UI release, and a build that lays it over each upstream release.

> Status: stages 7a and 7b of the plugin system (the overlay, and the automatic build and signed list). The installer that puts
> the result on a server comes next; there is nothing to install yet, and publishing builds is switched off.

## What a user will get

One command on a server that already runs the panel, then a restart, and the panel has a **Plugins** page. Updating the panel
the usual way (`x-ui update`) keeps the plugin system. Removing it puts the original panel binary back; the panel's own data is
never touched. (Installer: stage 7c.)

## How it works

The panel is one compiled binary with its web interface embedded, so the plugin system can only arrive as a rebuilt binary. This
repository builds it automatically for every upstream release from the upstream source plus the overlay, and publishes a signed
list that maps the hash of each original `x-ui` to the matching build. On the server a small script recognises the original by
its hash and swaps in the build with plugins. A binary it does not recognise is never replaced.

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

Not decided yet; see the notes in each file's header. Do not redistribute builds until this section says otherwise.
