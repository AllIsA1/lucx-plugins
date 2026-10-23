# The plugin catalog

The catalog is a signed list of plugins that someone reviewed, kept in a separate repository
(`lucx-plugins-index`). The panel shows it on the Plugins page; installing from it puts the plugin on exactly the
commit the catalog signed. Installing from a link still works, and says "not in the signed catalog" for any commit
the catalog does not list.

## What the panel checks

- The catalog is two files, `index.json` and `index.json.sig`: an ed25519 signature over the exact bytes of
  `index.json` (with a fixed prefix, so the signature cannot be reused for anything else). It is checked against the
  public keys built into the plugin system. A mirror can serve the files but cannot change them.
- The plugin system ships with two public keys, the current one and a spare, so a key can be rotated by shipping the spare in a
  release of the service (`builtinCatalogKeys` in `internal/plugins/catalog_keys.go`). A self-hosted catalog is trusted by
  adding its public key to `XUI_PLUGINS_CATALOG_KEYS` (comma separated, base64). With no key at all the catalog is simply
  off ("unconfigured"). An address that answers 404 for `index.json` (nothing published there yet) is shown as "not
  published yet", not as an error; a missing `index.json.sig` next to a present index is an error.
- `sequence` only grows. The highest sequence ever seen is stored; an older, validly signed index (a rollback attack)
  is refused and the last good catalog stays on screen, marked stale.
- The panel fetches at most every 6 hours (or on Refresh), with a 10 second timeout and a 1 MiB limit, and keeps the
  last good catalog on disk (`plugins-catalog.json` next to the registry), verified again at every start.
- **Install** downloads the repository at the commit in the index and refuses unless `lucx-plugin.json` hashes to
  `manifestSha256` and the id, permissions, type and scope are exactly the indexed ones. **Update** does the same for
  the commit the catalog names. Permissions are still shown and confirmed by the operator as for any install.
- Settings: `XUI_PLUGINS_CATALOG_BASE` or the "Catalog source" field set a mirror (https, or http for loopback).

## Publishing (the people who keep the catalog)

The tool lives in the plugin system repository: `go run ./cmd/plugincatalog <command>`.

```bash
# once: make the signing key. Keep catalog.key secret and backed up; it is never overwritten.
go run ./cmd/plugincatalog keygen -out ~/catalog.key        # prints the public key: ship it in the plugin system

# for each reviewed plugin: check out the exact commit, then
go run ./cmd/plugincatalog add -index index.json \
  -repo https://github.com/owner/plugin -commit <40-hex sha> -dir /path/to/checkout \
  -summary "One line for the card" -verified-by "Your name" -verified-at 2026-10-19 -notes "What was checked"

# sign (validates the index first) and publish index.json + index.json.sig
go run ./cmd/plugincatalog sign -key ~/catalog.key -index index.json
go run ./cmd/plugincatalog verify -index index.json -sig index.json.sig -pub <public key>
```

`add` reads the plugin's `lucx-plugin.json` (id, name, version, type, scope, permissions, license, author, menu icon),
hashes it, replaces an existing entry of the same id, keeps the list in id order and raises `sequence`. The permissions
in the index are the plugin's own: a review is only worth something for the commit it was done on, so every new
commit is a new `add`.

## The index format (schema 1)

```json
{
  "schema": 1,
  "sequence": 7,
  "generatedAt": "2026-10-19T12:00:00Z",
  "plugins": [
    {
      "id": "block-ads", "name": "Block ads", "summary": "…",
      "repo": "https://github.com/owner/block-ads",
      "commit": "<40 lowercase hex>", "version": "1.2.0", "manifestSha256": "<64 lowercase hex>",
      "type": "process", "scope": "both", "permissions": ["pipeline:xray.config"],
      "icon": "filter", "license": "MIT", "author": "owner",
      "verified": { "by": "LucX", "at": "2026-10-10", "notes": "…" }
    }
  ]
}
```

`repo` is the bare repository link (no branch or tag: the commit pins the version). Unknown fields are ignored so a
newer tool can add to the format. Limits: 500 plugins, 1 MiB, names up to 60 and summaries up to 300 characters.
Screenshots are not part of schema 1: the panel's own content policy would not let the page load images from other sites.
