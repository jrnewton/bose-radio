#!/usr/bin/env bash
#
# deploy_test.sh — static safety checks for deploy.sh and the redirect config.
# Does NOT touch the device; safe to run anywhere. Run: bash scripts/deploy_test.sh
#
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY="$DIR/deploy.sh"
REDIRECT="$DIR/SoundTouchSdkPrivateCfg.xml.redirect"
fails=0

pass() { printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fails=$((fails + 1)); }

check_present()  { if grep -qE -- "$2" "$DEPLOY"; then pass "$1"; else fail "$1"; fi; }
# code_absent checks only executable lines (strips comments + comment bodies),
# so a comment that *names* a forbidden command to warn against it doesn't trip.
code_absent()    { if sed 's/#.*//' "$DEPLOY" | grep -qE -- "$2"; then fail "$1"; else pass "$1"; fi; }

echo "== deploy.sh syntax =="
if bash -n "$DEPLOY"; then pass "bash -n parses"; else fail "bash -n parse error"; fi

echo "== safety invariants (gotchas from SESSION-HANDOFF.md) =="
# Must never pkill/pgrep -f preset-server: the SSH command line matches itself.
code_absent  "no 'pkill -f' self-kill"            'pkill +-f|pgrep +-f'
# Stray-listener kill must be by PID derived from netstat.
check_present "kills stray :8000 by netstat PID"  'netstat .*:8000'
# scp must use legacy protocol (device has no sftp).
check_present "scp uses -O (no sftp on device)"   'scp -O'
# Legacy host key algorithm required for SSH.
check_present "ssh allows legacy ssh-rsa host key" 'HostKeyAlgorithms=\+ssh-rsa'
# Cross-compile flags for the speaker.
check_present "cross-compiles GOARCH=arm GOARM=7"  'GOARCH=arm GOARM=7'
# Binary integrity check after copy.
check_present "verifies binary md5 on device"      'md5'
# Boot persistence + SSH persistence.
check_present "registers init via update-rc.d"     'update-rc.d preset-server'
check_present "persists SSH (remote_services)"      'touch /mnt/nv/remote_services'
# Health gate.
check_present "verifies /healthz after start"      'healthz'
# Stock-config backup guarded so a re-run never snapshots a redirected file.
check_present "backs up stock config once (guard)" '\[ ! -f \$CFG_BACKUP \]'
check_present "skips backup if already redirected"  "grep -q '127.0.0.1:8000' \\\$CFG_DEST"
# Never silently drops config fields on a differing firmware.
check_present "guards against dropping config fields" 'refusing to overwrite'
# Rootfs is always restored to ro even on a mid-window failure.
check_present "restores rootfs ro via trap on EXIT"  "trap 'mount -o remount,ro /"
# Non-TTY stdin must not abort a successful deploy at the reboot prompt.
check_present "reboot prompt tolerates non-TTY stdin" '\[ -t 0 \]'

echo "== rootfs remount is balanced (rw paired with ro) =="
rw=$(grep -c 'remount,rw /' "$DEPLOY")
ro=$(grep -c 'remount,ro /' "$DEPLOY")
if [ "$rw" -eq "$ro" ] && [ "$rw" -gt 0 ]; then pass "remount rw ($rw) == ro ($ro)"; else fail "unbalanced remount: rw=$rw ro=$ro"; fi

echo "== deploy.sh agrees with preset-server.init (cross-file) =="
INIT="$DIR/preset-server.init"
if [ -f "$INIT" ]; then pass "preset-server.init present"; else fail "preset-server.init missing"; fi
# The init script execs the same path deploy.sh pushes the binary to.
if grep -qF 'DAEMON="/mnt/nv/preset-server"' "$INIT"; then pass "init DAEMON = /mnt/nv/preset-server"; else fail "init DAEMON path unexpected"; fi
if grep -qF 'BIN_DEST="/mnt/nv/preset-server"' "$DEPLOY"; then pass "deploy BIN_DEST matches init DAEMON"; else fail "deploy BIN_DEST != init DAEMON"; fi
# deploy.sh installs the init to the path it later invokes.
if grep -qF 'INIT_DEST="/etc/init.d/preset-server"' "$DEPLOY"; then pass "deploy INIT_DEST = /etc/init.d/preset-server"; else fail "deploy INIT_DEST unexpected"; fi
# Both sides health-check the same port (:8000).
if grep -q 'localhost:8000/healthz' "$INIT"; then pass "init polls :8000/healthz"; else fail "init healthz port != 8000"; fi
if grep -q 'localhost:8000/healthz' "$DEPLOY"; then pass "deploy verifies :8000/healthz"; else fail "deploy healthz port != 8000"; fi

echo "== redirect config artifact =="
[ -f "$REDIRECT" ] && pass "redirect config exists" || fail "redirect config missing"
for url in \
  '<margeServerUrl>http://127.0.0.1:8000</margeServerUrl>' \
  '<statsServerUrl>http://127.0.0.1:8000</statsServerUrl>' \
  '<bmxRegistryUrl>http://127.0.0.1:8000/bmx/registry/v1/services</bmxRegistryUrl>'; do
  if grep -qF -- "$url" "$REDIRECT"; then pass "redirect has $url"; else fail "redirect missing $url"; fi
done
# The redirect artifact must carry no secrets / stock cloud hosts.
if grep -qE 'bose\.com|bosecm\.com|bose\.io' "$REDIRECT"; then
  # swUpdateUrl legitimately stays on bose.com; only flag the redirected hosts.
  if grep -E 'margeServerUrl|statsServerUrl|bmxRegistryUrl' "$REDIRECT" | grep -q 'bose'; then
    fail "a redirected URL still points at a Bose host"
  else pass "only swUpdateUrl references bose (expected)"; fi
else pass "no Bose cloud hosts in redirected URLs"; fi

echo
if [ "$fails" -eq 0 ]; then
  printf '\033[32mAll deploy checks passed.\033[0m\n'; exit 0
else
  printf '\033[31m%d check(s) failed.\033[0m\n' "$fails"; exit 1
fi
