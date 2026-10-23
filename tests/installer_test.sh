#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Tests for installer/install.sh and uninstall.sh in a fake root with a stand-in systemctl.
# The real program is built and installed; the network is never reached (INDEX_URL points nowhere).
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
fails=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ — $2}"; fails=$((fails + 1)); }

case $(uname -m) in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) echo "unsupported host" >&2; exit 2 ;; esac
dist=$work/dist
mkdir -p "$dist"
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=test" -o "$dist/lucx-plugins-sync-linux-$arch" ./cmd/lucx-plugins-sync
(cd "$dist" && sha256sum "lucx-plugins-sync-linux-$arch" | cut -d' ' -f1 > "lucx-plugins-sync-linux-$arch.sha256")

# The signed list and the host binary the installer takes the plugin system service from, served locally.
ctl=$work/buildsctl
go build -o "$ctl" ./cmd/buildsctl
key=$work/key
pub=$("$ctl" keygen -out "$key")
srv=$work/srv
mkdir -p "$srv/list" "$srv/dl/host-1"
printf '#!/bin/sh\necho host-1\n' > "$srv/dl/host-1/lucx-plugin-host-linux-$arch"
chmod 755 "$srv/dl/host-1/lucx-plugin-host-linux-$arch"
echo "another architecture" > "$srv/dl/host-1/lucx-plugin-host-linux-other"
mine=$(sha256sum "$srv/dl/host-1/lucx-plugin-host-linux-$arch" | cut -d' ' -f1)
theirs=$(sha256sum "$srv/dl/host-1/lucx-plugin-host-linux-other" | cut -d' ' -f1)
if [[ $arch == amd64 ]]; then amd=$mine; arm=$theirs; else amd=$theirs; arm=$mine; fi
"$ctl" host-set -index "$srv/list/host.json" -version host-1 -amd64-sha256 "$amd" -arm64-sha256 "$arm"
"$ctl" host-sign -key "$key" -index "$srv/list/host.json"
cat > "$work/serve.py" << 'PY'
import http.server, sys, os
os.chdir(sys.argv[1])
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a): pass
s = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PY
python3 "$work/serve.py" "$srv" > "$work/serve.port" &
serve_pid=$!
trap 'kill $serve_pid 2> /dev/null || true; rm -rf "$work"' EXIT
for _ in $(seq 1 50); do [[ -s $work/serve.port ]] && break; sleep 0.1; done
port=$(cat "$work/serve.port")
export LUCX_HOST_UPDATE_BASE=http://127.0.0.1:$port/list
export LUCX_HOST_UPDATE_DOWNLOAD_BASE=http://127.0.0.1:$port/dl
export LUCX_HOST_UPDATE_PUBKEYS=$pub

new_root() {
  local r=$1
  mkdir -p "$r/usr/local/x-ui" "$r/etc/lucx-plugins" "$r/bin" "$r/var/lib/x-ui-plugins" "$r/etc/x-ui"
  printf '#!/bin/sh\necho mine\n' > "$r/usr/local/x-ui/x-ui"
  chmod 755 "$r/usr/local/x-ui/x-ui"
  echo "plugin data" > "$r/var/lib/x-ui-plugins/data"
  cat > "$r/bin/systemctl" << 'STUB'
#!/bin/sh
echo "systemctl $*" >> "$(dirname "$0")/../systemctl.log"
if [ "$1" = cat ]; then printf '[Service]\nExecStart=/usr/local/x-ui/x-ui\n'; fi
exit 0
STUB
  chmod 755 "$r/bin/systemctl"
  cat > "$r/etc/lucx-plugins/config" << CONF
PANEL_BINARY=$r/usr/local/x-ui/x-ui
STATE_DIR=$r/var/lib/lucx-plugins
INDEX_URL=http://127.0.0.1:1/builds.json
CONF
}

install_in() { # install_in <root> [args...]
  local r=$1
  shift
  LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl LUCX_PLUGINS_SYNC_URL=file://$dist bash installer/install.sh "$@"
}
uninstall_in() {
  local r=$1
  shift
  LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl bash installer/uninstall.sh "$@"
}

# 1. a normal install
r=$work/a
new_root "$r"
if install_in "$r" --no-restart --restart-at 05:30 > "$work/a.out" 2>&1; then ok "install runs"; else bad "install runs" "$(tail -5 "$work/a.out")"; fi
[[ -x $r/usr/local/lucx-plugins/lucx-plugins-sync ]] && ok "the program is installed" || bad "the program is installed"
grep -q "^ExecStartPre=-+$r/usr/local/lucx-plugins/lucx-plugins-sync -config $r/etc/lucx-plugins/config at-start$" "$r/etc/systemd/system/x-ui.service.d/30-lucx-plugins.conf" && ok "the start hook is a drop-in that cannot stop the panel" || bad "the start hook is a drop-in that cannot stop the panel"
grep -q "OnCalendar=\*-\*-\* 05:30:00" "$r/etc/systemd/system/lucx-plugins-apply.timer" && ok "the restart hour is honoured" || bad "the restart hour is honoured"
grep -q "enable --now lucx-plugins-sync.timer lucx-plugins-apply.timer" "$r/systemctl.log" && grep -q "daemon-reload" "$r/systemctl.log" && ok "the timers are enabled" || bad "the timers are enabled"
grep -q "INDEX_URL=http://127.0.0.1:1/builds.json" "$r/etc/lucx-plugins/config" && ok "an existing configuration is kept" || bad "an existing configuration is kept"
[[ $(cat "$r/usr/local/x-ui/x-ui") == $'#!/bin/sh\necho mine' ]] && ok "an unknown panel binary is left alone" || bad "an unknown panel binary is left alone"
grep -q "restart x-ui" "$r/systemctl.log" && bad "--no-restart must not restart the panel" || ok "--no-restart does not restart the panel"

# 1b. the plugin system service: binary, unit, environment file, start
[[ -x $r/usr/local/lucx-plugins/lucx-plugin-host && $("$r/usr/local/lucx-plugins/lucx-plugin-host" -v) == host-1 ]] && ok "the service binary comes from the signed list" || bad "the service binary comes from the signed list"
unit=$r/etc/systemd/system/lucx-plugins-host.service
[[ -f $unit ]] && ok "the service unit is written" || bad "the service unit is written"
grep -q "^Restart=always$" "$unit" && grep -q "^KillMode=process$" "$unit" && ok "the unit restarts the service and leaves plugin processes alone" || bad "the unit restarts the service and leaves plugin processes alone"
grep -q "^ExecStartPre=-+$r/usr/local/lucx-plugins/lucx-plugins-sync -host-exe $r/usr/local/lucx-plugins/lucx-plugin-host -host-state $r/var/lib/lucx-plugin-host host-guard$" "$unit" && ok "a failing update is rolled back by a guard before each start" || bad "a failing update is rolled back by a guard before each start"
grep -q "^ExecStart=$r/usr/local/lucx-plugins/lucx-plugin-host -state-dir $r/var/lib/lucx-plugin-host$" "$unit" && grep -q "^ReadWritePaths=.*$r/usr/local/lucx-plugins.*/var/lib/x-ui-plugins" "$unit" && ok "the unit names the paths it writes" || bad "the unit names the paths it writes"
grep -q "^RuntimeDirectory=lucx-panel$" "$r/etc/systemd/system/x-ui.service.d/30-lucx-plugins.conf" && ok "the panel gets a runtime directory for its socket" || bad "the panel gets a runtime directory for its socket"
grep -q "enable lucx-plugins-host.service" "$r/systemctl.log" && grep -q "restart lucx-plugins-host.service" "$r/systemctl.log" && ok "the service is enabled and (re)started" || bad "the service is enabled and (re)started"
host_unit_name=$(bash -c 'source installer/common.sh; echo "$HOST_UNIT_NAME"')
# shellcheck disable=SC2053 # a glob on purpose: the service treats every lucx-plugin-*.service as a plugin's
[[ $host_unit_name != lucx-plugin-*.service ]] && ok "the service's own unit is not mistaken for a plugin's" || bad "the service's own unit is not mistaken for a plugin's" "$host_unit_name"
grep -q "^#LUCX_HOST_GATEWAY=127.0.0.1:19480$" "$r/etc/lucx-plugins/host.env" && ok "the gateway defaults are documented in the environment file" || bad "the gateway defaults are documented in the environment file"
if command -v systemd-analyze > /dev/null 2>&1; then
  if systemd-analyze verify "$unit" > "$work/verify.out" 2>&1; then ok "systemd accepts the service unit"; else bad "systemd accepts the service unit" "$(head -5 "$work/verify.out")"; fi
fi

# 2. installing again changes nothing
before=$(cd "$r" && find etc usr/local/lucx-plugins -type f -print0 | sort -z | xargs -0 sha256sum)
install_in "$r" --no-restart --restart-at 05:30 > /dev/null 2>&1
after=$(cd "$r" && find etc usr/local/lucx-plugins -type f -print0 | sort -z | xargs -0 sha256sum)
[[ $before == "$after" ]] && ok "a second install is idempotent" || bad "a second install is idempotent"

# 2b. a gateway of the operator's choice is written, and only a loopback one
install_in "$r" --no-restart --gateway 127.0.0.1:19999 > /dev/null 2>&1
grep -q "^LUCX_HOST_GATEWAY=127.0.0.1:19999$" "$r/etc/lucx-plugins/host.env" && ok "--gateway is written to the environment file" || bad "--gateway is written to the environment file"
install_in "$r" --no-restart > /dev/null 2>&1
grep -q "^LUCX_HOST_GATEWAY=127.0.0.1:19999$" "$r/etc/lucx-plugins/host.env" && ok "a later install keeps the operator's gateway" || bad "a later install keeps the operator's gateway"
for g in 0.0.0.0:19480 example.com:19480 192.168.1.5:19480 127.0.0.1 "127.0.0.1:1;rm"; do
  if install_in "$r" --no-restart --gateway "$g" > /dev/null 2>&1; then bad "gateway '$g' is refused"; else ok "gateway '$g' is refused"; fi
done

# 3. uninstall when the panel is not our build: parts removed, nothing restarted, data kept
mkdir -p "$r/var/lib/lucx-plugin-host" && echo "registry" > "$r/var/lib/lucx-plugin-host/plugins.json"
uninstall_in "$r" > "$work/u1.out" 2>&1 || bad "uninstall runs" "$(tail -3 "$work/u1.out")"
grep -q "disable --now lucx-plugins-host.service" "$r/systemctl.log" && grep -q "stop lucx-plugin-\*.service" "$r/systemctl.log" && ok "uninstall stops the service and the plugins it started" || bad "uninstall stops the service and the plugins it started"
[[ ! -e $r/etc/systemd/system/lucx-plugins-host.service && ! -e $r/etc/lucx-plugins ]] && ok "uninstall removes the service unit and its environment" || bad "uninstall removes the service unit and its environment"
[[ -f $r/var/lib/lucx-plugin-host/plugins.json ]] && ok "the service's registry stays without --purge" || bad "the service's registry stays without --purge"
[[ ! -e $r/usr/local/lucx-plugins && ! -e $r/etc/systemd/system/x-ui.service.d/30-lucx-plugins.conf && ! -e $r/etc/systemd/system/lucx-plugins-apply.timer && ! -e $r/etc/lucx-plugins ]] && ok "uninstall removes everything it installed" || bad "uninstall removes everything it installed"
[[ -f $r/var/lib/x-ui-plugins/data ]] && ok "plugin data stays without --purge" || bad "plugin data stays without --purge"
grep -q "restart x-ui" "$r/systemctl.log" && bad "nothing was restored, so nothing should restart" || ok "no restart when there was nothing to restore"

# 4. uninstall when the program reports a restore: the panel is restarted; --purge removes plugin data
r=$work/b
new_root "$r"
install_in "$r" --no-restart > /dev/null 2>&1
printf '#!/bin/sh\necho "restored the original panel binary"\n' > "$r/usr/local/lucx-plugins/stub" && chmod 755 "$r/usr/local/lucx-plugins/stub" && mv -f "$r/usr/local/lucx-plugins/stub" "$r/usr/local/lucx-plugins/lucx-plugins-sync"
uninstall_in "$r" --purge > /dev/null 2>&1 || bad "uninstall with --purge runs"
grep -q "restart x-ui" "$r/systemctl.log" && ok "the panel is restarted after a restore" || bad "the panel is restarted after a restore"
[[ ! -e $r/var/lib/x-ui-plugins ]] && ok "--purge removes plugin data" || bad "--purge removes plugin data"
[[ ! -e $r/var/lib/lucx-plugin-host ]] && ok "--purge removes the service's state" || bad "--purge removes the service's state"

# 5. a failing restore stops the uninstall before it deletes the program
r=$work/c
new_root "$r"
install_in "$r" --no-restart > /dev/null 2>&1
printf '#!/bin/sh\nexit 1\n' > "$r/usr/local/lucx-plugins/stub" && chmod 755 "$r/usr/local/lucx-plugins/stub" && mv -f "$r/usr/local/lucx-plugins/stub" "$r/usr/local/lucx-plugins/lucx-plugins-sync"
if uninstall_in "$r" > "$work/c.out" 2>&1; then bad "a failed restore must fail the uninstall"; else ok "a failed restore fails the uninstall"; fi
[[ -x $r/usr/local/lucx-plugins/lucx-plugins-sync ]] && ok "the program stays so the restore can be retried" || bad "the program stays so the restore can be retried"

# 6. a program that does not match its checksum is refused and nothing is installed
r=$work/d
new_root "$r"
rm -rf "$r/etc/lucx-plugins"
cp -r "$dist" "$work/dist-bad"
echo 0000000000000000000000000000000000000000000000000000000000000000 > "$work/dist-bad/lucx-plugins-sync-linux-$arch.sha256"
if LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl LUCX_PLUGINS_SYNC_URL=file://$work/dist-bad bash installer/install.sh --no-restart > "$work/d.out" 2>&1; then bad "a checksum mismatch must abort"; else ok "a checksum mismatch aborts"; fi
[[ ! -e $r/usr/local/lucx-plugins && ! -e $r/etc/systemd/system/x-ui.service.d ]] && ok "nothing is installed after an aborted install" || bad "nothing is installed after an aborted install"

# 6b. --no-restart only checks; without it the program is asked to apply (which restarts when a build waits)
stub=$work/dist-stub
mkdir -p "$stub"
cat > "$stub/lucx-plugins-sync-linux-$arch" << 'STUB'
#!/bin/sh
for last; do :; done
echo "$last" >> "$(dirname "$0")/calls.log"
case $last in version) echo stub ;; status) echo "stub status" ;; esac
exit 0
STUB
chmod 755 "$stub/lucx-plugins-sync-linux-$arch"
(cd "$stub" && sha256sum "lucx-plugins-sync-linux-$arch" | cut -d' ' -f1 > "lucx-plugins-sync-linux-$arch.sha256")
for mode in no-restart restart; do
  r=$work/f-$mode
  new_root "$r"
  rm -rf "$r/etc/lucx-plugins"
  args=()
  [[ $mode == no-restart ]] && args=(--no-restart)
  LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl LUCX_PLUGINS_SYNC_URL=file://$stub bash installer/install.sh "${args[@]}" > /dev/null 2>&1 || bad "install ($mode) runs"
  calls=$(cat "$r/usr/local/lucx-plugins/calls.log" 2> /dev/null || true)
  if [[ $mode == no-restart ]]; then
    [[ $calls == *check* && $calls != *apply* ]] && ok "--no-restart asks the program only to check" || bad "--no-restart asks the program only to check" "$calls"
  else
    [[ $calls == *apply* ]] && ok "without --no-restart the program is asked to apply" || bad "without --no-restart the program is asked to apply" "$calls"
  fi
done

# 6c. the documented one-liner pipes the script into bash (and sudo cannot pass a <(...) file descriptor on)
r=$work/g
new_root "$r"
rm -rf "$r/etc/lucx-plugins"
mkdir -p "$work/raw/installer"
cp installer/*.sh "$work/raw/installer/"
# shellcheck disable=SC2002 # the cat mimics `curl ... | bash`
if (cat "$work/raw/installer/install.sh" | LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl LUCX_PLUGINS_SYNC_URL=file://$dist LUCX_PLUGINS_RAW=file://$work/raw bash -s -- --no-restart > "$work/g.out" 2>&1); then
  [[ -x $r/usr/local/lucx-plugins/lucx-plugins-sync ]] && ok "install works when piped into bash" || bad "install works when piped into bash" "nothing installed"
else
  bad "install works when piped into bash" "$(tail -4 "$work/g.out")"
fi
# shellcheck disable=SC2002
if (cat "$work/raw/installer/uninstall.sh" | LUCX_ROOT=$r LUCX_SYSTEMCTL=$r/bin/systemctl LUCX_PLUGINS_RAW=file://$work/raw bash -s > "$work/g2.out" 2>&1); then
  [[ ! -e $r/usr/local/lucx-plugins ]] && ok "uninstall works when piped into bash" || bad "uninstall works when piped into bash" "files left"
else
  bad "uninstall works when piped into bash" "$(tail -4 "$work/g2.out")"
fi

# 6d. a plugin system service that cannot be installed stops the installer before it changes anything else;
# a leftover of the earlier built-in system is mentioned
r=$work/h
new_root "$r"
rm -rf "$r/etc/lucx-plugins"
foreign=$("$ctl" keygen -out "$work/other.key")
if (export LUCX_HOST_UPDATE_PUBKEYS=$foreign; install_in "$r" --no-restart > "$work/h.out" 2>&1); then bad "an untrusted service list must abort"; else grep -q "could not be installed" "$work/h.out" && ok "an untrusted service list aborts with a clear message" || bad "an untrusted service list aborts with a clear message" "$(tail -3 "$work/h.out")"; fi
[[ ! -e $r/etc/systemd/system/x-ui.service.d && ! -e $r/etc/systemd/system/lucx-plugins-host.service && ! -e $r/usr/local/lucx-plugins/lucx-plugin-host ]] && ok "nothing but the program is left after an aborted install" || bad "nothing but the program is left after an aborted install"
r=$work/i
new_root "$r"
rm -rf "$r/etc/lucx-plugins"
echo '{}' > "$r/etc/x-ui/plugins.json"
install_in "$r" --no-restart > "$work/i.out" 2>&1
grep -q "earlier, built-in plugin system" "$work/i.out" && ok "plugins of the earlier system are mentioned, not touched" || bad "plugins of the earlier system are mentioned, not touched"
[[ -f $r/etc/x-ui/plugins.json ]] && ok "the earlier system's files are left alone" || bad "the earlier system's files are left alone"

# 7. refuses to run without a panel binary or with bad options
r=$work/e
new_root "$r"
rm "$r/usr/local/x-ui/x-ui"
if install_in "$r" --no-restart > "$work/e.out" 2>&1; then bad "a missing panel binary must abort"; else grep -q "does not exist" "$work/e.out" && ok "a missing panel binary aborts with a clear message" || bad "a missing panel binary aborts with a clear message" "$(tail -2 "$work/e.out")"; fi
if install_in "$r" --restart-at 25:99 > /dev/null 2>&1; then bad "a bad --restart-at must abort"; else ok "a bad --restart-at aborts"; fi

((fails == 0)) || { echo "$fails test(s) failed"; exit 1; }
echo "all passed"
