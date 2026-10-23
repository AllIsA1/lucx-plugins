# Writing a LucX-UI plugin

A plugin is a GitHub repository with a `lucx-plugin.json` at its root and, for plugins that run code, the
compiled binaries. The plugin system downloads the repository at one commit, shows the operator what the plugin asks
for (on the panel's Plugins page), and only installs it after they confirm exactly those permissions. A plugin never
modifies panel files; removing it restores the panel to its previous state.

**Where things run.** The plugin system is a service of its own (`lucx-plugins-host`) next to the panel. It installs, starts and
confines plugins and keeps their permissions; the panel only shows the Plugins page, forwards requests to the service and
hands it panel events. So: a plugin keeps running when the panel restarts and when the service restarts or updates itself; the
address in `$LUCX_PANEL_URL` is the service's loopback gateway (default `127.0.0.1:19480`), which forwards the calls the plugin
has permission for to the panel; and this document says "the panel" where the operator sees the panel and the Plugins page.

`examples/hello-plugin` is a complete, working plugin (requests, panel API, events, a page that calls its own backend); `examples/block-ads-plugin` shows a pipeline plugin; `examples/ui-hello` is a page-only plugin with no process. Copy one to start. It is MIT-licensed (see its `LICENSE`), so you may use and relicense it freely; your plugin's own license is your choice.

## Layout

```
lucx-plugin.json          the manifest (required)
bin/amd64/<name>          executable for x86-64   (process plugins)
bin/arm64/<name>          executable for ARM64
ui/…                      pages shown inside the panel (see *Interface pages*)
```

The panel does not compile anything. Build the binaries yourself (`make build` in the example) and commit
`bin/`. Binaries must be static Linux executables (`CGO_ENABLED=0`).

## Manifest

```json
{
  "id": "hello",
  "name": "Hello Plugin",
  "version": "1.0.0",
  "apiVersion": "1",
  "type": "process",
  "run": { "command": "bin/${arch}/hello", "args": [] },
  "permissions": ["api:clients:read"]
}
```

| Field | Rules |
|---|---|
| `id` | 3–40 characters, `a-z 0-9 -`, starts and ends with a letter or digit. Names the systemd unit and the data directory. |
| `version` | semver, `1.2.3` |
| `apiVersion` | `"1"` |
| `minPanelVersion` | optional, `3.10.2` — older panels refuse to install |
| `type` | `process` (runs code), `hook` (runs code, no pages), `ui` (pages only) |
| `scope` | `master` (default), `node`, `both`. A plugin installed on a panel runs there when its scope is `master` or `both`; one a master panel pushes to a node runs on the node when its scope is `node` or `both`. A `master` plugin is never pushed to a node |
| `run.command` | relative path inside the repository. `${arch}` becomes `amd64` or `arm64`; `${socket}` and `${data}` are the socket and data paths |
| `hooks.install`, `hooks.uninstall` | optional endpoints under `/_lucx/` the panel calls (see Hooks) |
| `permissions` | see below |

## Permissions

The operator sees every permission before installing. Dangerous ones are marked in red.

| Permission | Grants | Dangerous |
|---|---|---|
| `api:<resource>:read` | read routes of the panel API for `clients`, `inbounds`, `hosts`, `nodes`, `server`, `xray`, `settings` | `nodes`, `settings` |
| `api:<resource>:write` | change routes for the same resources. Write does not include read | always |
| `events:<name>` | receive a panel event, e.g. `events:client.created` (see *Events*) | no |
| `pipeline:xray.config` | patch the generated Xray configuration (see *Pipelines*) | always |
| `network` | reach the internet. Without it the plugin can only talk to the panel on this machine | always |
| `ui:menu`, `ui:replace:<route>` | sidebar entries, replacing a built-in page (see *Interface pages*) | `replace` |

Routes that hand over the database, rotate credentials, update or reconfigure the panel are not reachable by any
permission.

## How a plugin runs

The plugin system service starts the executable as its own unprivileged user. How well it is confined depends on the host; the
Plugins page shows the level.

| Host | Runner | Confinement |
|---|---|---|
| Linux with systemd (default) | transient systemd service | **strong**: own dynamic user, read-only system, private `/tmp`, no capabilities, memory/CPU/task caps, **no network** except the plugin system's gateway on this machine unless the plugin holds `network`. Keeps running when the panel or the service restarts; the service picks it up again. |
| Docker image (`XUI_IN_DOCKER=true`), or `XUI_PLUGINS_RUNNER=process` on a host without systemd | child process of the plugin system service (it must run as root) | **partial**: own unprivileged user per plugin, no supplementary groups, `no_new_privs`, no capabilities, limits on memory (data segment), processes and files. Outgoing connections are **not** filtered, so the `network` permission is not enforced, and files readable by every user stay readable. The plugin stops when the service stops and is started again with it. |

In every case:

* it should write only to its **state directory** (`$STATE_DIRECTORY`, `/var/lib/x-ui-plugins/<id>`; in Docker
  `/etc/x-ui/plugins-data/<id>`, on the volume); its code is read-only;
* if it crashes repeatedly the plugin system switches it off and shows why.

Declare only the permissions you need: on the process runner the panel cannot enforce the ones you leave out, so an honest
manifest is what the admin relies on. `XUI_PLUGINS_RUNNER` takes `systemd`, `systemd-user` (development without root) or `process`; the process runner keeps
sockets and tokens in `/run/x-ui-plugins` (`XUI_PLUGINS_RUNTIME_DIR` moves them).

Docker: the image keeps plugin code and data under the database volume, so they survive a container recreate. Compose users
should add `init: true` to the service so orphaned grandchildren of a plugin are reaped.

### The contract

| What | How |
|---|---|
| Listen | on the unix socket in `$LUCX_SOCKET`; never on a TCP port |
| Health | answer `GET /_lucx/health` with 200 when ready |
| Stop | exit on `SIGTERM` within 10 seconds |
| Data | write only under `$STATE_DIRECTORY` |
| Logs | write to stdout/stderr; they appear in the panel (Plugins → Logs) |
| Identity | `$LUCX_PLUGIN_ID`, `$LUCX_API_VERSION` |

`examples/hello-plugin/lucxplugin.go` implements all of it; copy that file.

## Hooks

Both are plain `POST`s to your socket with the header `X-LucX-Hook: install` or `uninstall`; any 2xx is success.

* `hooks.install` runs **once**, after the plugin first answers its health check (a freshly installed plugin starts disabled, so
  this is the first time it can run). A failure is retried on the next few reconciles (3 attempts in all, then it waits for a
  panel restart) and never disables the plugin. It can run again after a panel restart if it never succeeded, so make it
  idempotent. Updates do not run it again.
* `hooks.uninstall` runs just before the process is stopped, **only if the plugin is running** at that moment, with a 10 second
  limit. Errors and timeouts are logged and the removal goes on, so it cannot be relied on for anything that must happen.

`examples/hello-plugin` implements both.

## Serving requests

An operator (or an API token with admin scope) reaches your plugin through the panel:

```
GET/POST/PUT/DELETE  /panel/api/plugins/x/<id>/<path>   →   your socket, path /<path>
```

The panel authenticates the caller and checks CSRF before forwarding. **Your plugin never sees the cookie, the
`Authorization` header or the CSRF token.** It receives `X-LucX-User` (the panel user), `X-LucX-Plugin-Id` and
`X-LucX-Request-Id`; clients cannot forge them. Paths under `/_lucx/` are reserved and not forwarded. Request
bodies are limited to 10 MiB and responses are sandboxed (`Content-Security-Policy: sandbox`, no `Set-Cookie`,
no CORS headers), so a plugin cannot run script with the panel's privileges.

## Events

List `events:<name>` permissions and the panel POSTs each matching event to your socket at `/_lucx/events`:

```json
{"id": "5f2c…", "type": "client.created", "source": "alice@example.com",
 "time": "2026-10-18T12:00:00Z", "data": {"email": "alice@example.com", "inboundIds": [3]}}
```

Answer 2xx quickly (the panel waits 5 seconds and does not retry). Delivery is best effort, in order per plugin: a
plugin that is stopped or cannot keep up (64 queued events) loses events, and the Plugins page counts delivered,
failed and dropped. Payloads carry identifiers only, never keys, passwords or UUIDs.

| Event | Data |
|---|---|
| `client.created`, `client.updated`, `client.deleted` | `email`, `clientId`, `inboundIds` (created) |
| `client.traffic.exhausted`, `client.expired` | `email` — the client was just switched off |
| `inbound.created`, `inbound.updated`, `inbound.deleted` | `id`, `tag`, `protocol`, `remark`, `port` |
| `xray.crash`, `node.down`, `node.up`, `cpu.high`, `memory.high`, `outbound.down`, `outbound.up`, `login.attempt` | as published by the panel |

Misspelled event names are rejected when the plugin is installed.

## Pipelines

A pipeline lets a plugin change something the panel generates, without touching panel files. The only pipeline is
`xray.config` (permission `pipeline:xray.config`, always marked dangerous): every time the panel builds the Xray
configuration it POSTs the document to `/_lucx/pipeline/xray.config` on each plugin holding the permission, one
after another in plugin id order, each seeing the previous result. The plugin answers a JSON Patch (RFC 6902):

```json
{"patch": [{"op": "add", "path": "/routing/rules/0", "value": {"type": "field", "outboundTag": "blocked"}}]}
```

or `204 No Content` for no change. Only `add`, `remove`, `replace` and `test` are accepted, at most 200 operations, and
the result stays under 4 MiB. Because the change is computed on the fly and never stored, **removing or disabling
the plugin removes its effect**.

The panel keeps what it depends on: a patch is refused if it changes the `api`, `stats` or `metrics` sections,
changes the API inbound, or removes or renames an existing inbound. A plugin that errors, times out (2 seconds),
returns an invalid patch or one that is refused is skipped for that run (the config is generated as if it were not
installed) and the problem shows on the Plugins page. Answers are cached for 30 seconds per identical document, and
the panel calls you on every generation, so **your handler must be idempotent**: a document that already contains
your change must produce no operations. `examples/block-ads-plugin` does exactly this.

## Interface pages

A plugin can add sidebar entries (`ui.menu`, permission `ui:menu`) and stand in for built-in pages (`ui.replaces`,
permission `ui:replace:<route>`). Pages live below `ui/`: an interface plugin (`type: ui`) is served straight from
disk (a path ending in `/` serves its `index.html`, nothing is listed, nothing outside `ui/` is reachable); a process
plugin answers the same URLs itself.

- Replaceable pages: `/`, `/inbounds`, `/clients`, `/groups`, `/nodes`, `/hosts`. Settings, Xray, Plugins, API docs
  and the login cannot be replaced, so a plugin can never hide its own uninstall. One plugin holds a page: installing
  a second one that replaces the same page is refused. In the panel, `?original=1` always shows the built-in page and
  a banner on the replaced page links to it.
- The page that stands in for a route is found by convention, no manifest field: `ui/replace/<route>/index.html`
  (the dashboard `/` uses `ui/replace/index.html`).
- The panel shows a page in an iframe with `sandbox="allow-scripts"` and no `allow-same-origin`: the page has no origin
  of its own, no cookies, no `localStorage`, and the page's CSP forbids any network access (`connect-src 'none'`), inline
  script, `<script type="module">`, forms and frames. Use classic `<script src>` files and relative URLs.
- That frame has no cookie, so the panel gives it a token address, `/panel/plugin-frame/<token>/ui/...` (2 hours, one
  plugin, GET and HEAD below `/ui/` only; it stops working when the plugin is disabled, updated or removed). Relative
  URLs of your scripts and styles resolve below it. Opening `/panel/api/plugins/x/<id>/ui/...` in a tab shows the HTML,
  but its scripts and styles cannot load: that address is not meant to be opened directly.
- Menu and replacements follow the registry: disabling or uninstalling a plugin removes them at once.
  `GET /panel/api/plugins/menu` lists what is active.

### Writing a page

Copy `examples/sdk/lucx-ui.js` (and `lucx-ui.d.ts` for TypeScript) into `ui/` and load it with a classic script. It adds
one global, `lucx`:

```html
<script src="lucx-ui.js"></script>
<script>
  lucx.ready().then(function (ctx) { lucx.applyTheme(); /* ctx.pluginId, ctx.theme, ctx.lang */ });
  lucx.onContext(function () { lucx.applyTheme(); });          // theme or language changed
  lucx.api('/api/hello').then(function (res) { /* res.ok, res.status, res.body */ });
</script>
```

`lucx.applyTheme()` sets `data-theme="dark|light"`, `data-palette` and `lang` on `<html>` so your CSS can follow the
panel. `lucx.api(path, {method, body})` always resolves with `{ok, status, body?, error?}` and has a 35-second limit.
A process plugin serves its own page and must answer under `/api/` for the calls (see how `examples/hello-plugin`
embeds `ui/` and routes `/api/...` to the same handlers); a page-only plugin has no backend, so `lucx.api` is of no use
there. Menu icons are plain words from a fixed list: `api`, `bar-chart`, `bell`, `bug`, `chart`, `cloud`,
`cloud-server`, `code`, `dashboard`, `database`, `experiment`, `file`, `filter`, `folder`, `global`, `heart`, `key`,
`line-chart`, `link`, `lock`, `mail`, `message`, `notification`, `pie-chart`, `rocket`, `safety`, `setting`, `star`,
`tags`, `team`, `thunderbolt`, `tool`, `user`, `warning`; anything else shows a generic icon.

### The bridge (`postMessage`, version 1)

The page talks to the panel and to its own backend only through messages to `window.parent` (target `"*"`: the page's
origin is opaque). Every message is `{ lucx: 1, type, ... }`; the panel answers only the frame it created.

| From the page | Reply from the panel |
|---|---|
| `{type: "ready"}` | `{type: "init", pluginId, theme: {dark, ultra, palette}, lang}`, later `{type: "context", theme, lang}` whenever either changes |
| `{type: "fetch", id, method, path, body?}` | `{type: "fetch-result", id, ok, status, body?, error?}` |

`path` must start with `/api/` and is sent to the plugin's own backend (`/panel/api/plugins/x/<id>/api/...`) with the
admin's session and CSRF token, which the plugin never sees. `method` is GET, POST, PUT or DELETE; the body is JSON up
to 1 MiB; at most 20 calls a second and 8 at once. Anything else (another path, `..`, encoded dots or slashes) answers
`{ok: false, status: 403}`. A page never calls the panel API: panel data comes from the plugin's backend, which uses
its own token and permissions.

## Updating and rolling back

The Plugins page checks a plugin's own repository (where it was installed from, or a branch, tag or commit you type)
and shows what an update changes: the new version and commit, the permissions that are added, removed or kept, and a
change of `type` or `scope`. Nothing changes on disk until you confirm.

- Anything that adds a permission, or changes `type` or `scope`, needs an explicit confirmation; an update that only
  keeps or removes permissions is one click. The API requires the confirmed permissions to equal the new manifest's
  exactly, as at install. An update can only come from the plugin's own repository and its manifest `id` must not change.
- Applying an update stops the process, keeps the old code as the single rollback copy (`<id>.prev`), swaps in the new
  code and updates version, commit, permissions and menu. The enabled state, the install time and the plugin's data
  directory stay. A running plugin has 30 seconds to become healthy (`/_lucx/health`); if it does not, the update is
  undone by itself and the call fails saying the previous version was restored.
- **Roll back** (shown when a rollback copy exists) returns to the version from before the last update and drops the newer
  code. Data is never touched, so a version that migrates its data must cope with an older one reading it.
- Nothing updates by itself. Master panels cannot update or roll back plugins on a node directly: the node row of the
  master's Plugins page flags a plugin on a different commit and offers **Update on node**.

## Plugins on nodes

A panel that manages nodes can install a plugin on them (Plugins page → *Plugins on nodes*). The node owner must first
allow it (Plugins page of the node → *Allow the master panel to manage plugins*; off by default). The master does
not copy code: it tells the node the repository, the exact commit, the SHA-256 of `lucx-plugin.json` and the
permissions the operator approved, and the node downloads that commit itself and refuses on any difference. A
plugin pushed to a node runs there only if its `scope` is `node` or `both`, and the master can only control plugins
it installed, never ones the node owner installed by hand. Events and pipelines on a node concern that node.

The master shows each node as reachable, not read yet, offline, without plugin support, locked (the owner has not
allowed management) or failing, refreshed every 30 seconds. Commands are explicit: nothing installs, updates or
removes a plugin on a node by itself, and a node whose version differs from the master's is flagged, not fixed.

**Update on node** removes the plugin from the node (its data stays), installs the master's pinned commit again and
turns it on if it was on. The master refuses before touching the node when the permissions differ from its own, the
plugin is not on the node, was installed there by hand, or already runs that commit; grown permissions need an explicit
tick. There is no automatic way back: if the new version cannot be installed the node is left without the plugin and
the error says so (install it again once the cause is fixed).

## Calling the panel

The plugin system gives the plugin an API token: the address (the service's loopback gateway, which passes the call on to
the panel) is in `$LUCX_PANEL_URL`, the token in the file `$CREDENTIALS_DIRECTORY/panel-token`. Send it as
`Authorization: Bearer <token>` to `/panel/api/...`. Only routes covered by the plugin's permissions answer; everything else
returns 403 (the gateway forwards a call with the list of routes this plugin may use, and the panel checks the route it
resolved against that list). The token stops working the
moment the plugin is disabled, updated or its permissions change. Answers use the panel envelope
`{"success": true, "msg": "", "obj": …}`. `Panel` in `lucxplugin.go` wraps this.

## Installing

Publish the repository and give the operator its link, `https://github.com/<owner>/<repo>` (optionally
`/tree/<branch|tag|commit>`). In the panel: Plugins → paste the link → Check plugin → review permissions →
Install. The plugin is installed disabled; switch it on when ready. Pin a commit or tag in the link for
reproducible installs.

## Limits

* GitHub repositories only, public ones;
* archive up to 100 MiB, 5000 files, 150 MiB per file;
* one plugin per `id`; update by uninstalling and installing the new version (in-place updates come later).
