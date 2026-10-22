#!/usr/bin/env bash
# Copyright (c) 2026 AllIsA1.
# SPDX-License-Identifier: MIT
# Tests for installer/install.sh and uninstall.sh in a fake root with a stand-in systemctl.
# The real program is built and installed; the network is never reached (INDEX_URL points nowhere).
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ — $2}"; fails=$((fails + 1)); }

case $(uname -m) in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) echo "unsupported host" >&2; exit 2 ;; esac
dist=$work/dist
mkdir -p "$dist"
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=test" -o "$dist/lucx-plugins-sync-linux-$arch" ./cmd/lucx-plugins-sync
(cd "$dist" && sha256sum "lucx-plugins-sync-linux-$arch" | cut -d' ' -f1 > "lucx-plugins-sync-linux-$arch.sha256")

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

# 2. installing again changes nothing
before=$(cd "$r" && find etc usr/local/lucx-plugins -type f -print0 | sort -z | xargs -0 sha256sum)
install_in "$r" --no-restart --restart-at 05:30 > /dev/null 2>&1
after=$(cd "$r" && find etc usr/local/lucx-plugins -type f -print0 | sort -z | xargs -0 sha256sum)
[[ $before == "$after" ]] && ok "a second install is idempotent" || bad "a second install is idempotent"

# 3. uninstall when the panel is not our build: parts removed, nothing restarted, data kept
uninstall_in "$r" > "$work/u1.out" 2>&1 || bad "uninstall runs" "$(tail -3 "$work/u1.out")"
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

# 7. refuses to run without a panel binary or with bad options
r=$work/e
new_root "$r"
rm "$r/usr/local/x-ui/x-ui"
if install_in "$r" --no-restart > "$work/e.out" 2>&1; then bad "a missing panel binary must abort"; else grep -q "does not exist" "$work/e.out" && ok "a missing panel binary aborts with a clear message" || bad "a missing panel binary aborts with a clear message" "$(tail -2 "$work/e.out")"; fi
if install_in "$r" --restart-at 25:99 > /dev/null 2>&1; then bad "a bad --restart-at must abort"; else ok "a bad --restart-at aborts"; fi

((fails == 0)) || { echo "$fails test(s) failed"; exit 1; }
echo "all passed"
