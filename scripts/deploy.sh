#!/usr/bin/env bash
#
# deploy.sh — full (re)deploy of preset-server to a Bose SoundTouch 10.
#
# Run this FROM the dev machine; it drives the speaker over SSH. It rebuilds the
# binary, pushes every on-device artifact, installs boot persistence, redirects
# the speaker's (dead) cloud URLs to the local service, and starts it. It is
# idempotent: safe to re-run, and safe to run against a freshly factory-reset
# speaker (it rebuilds everything from scratch).
#
# PRECONDITION: passwordless root SSH is already enabled on the device. If a
# factory reset disabled SSH, re-enable it first (USB `remote_services` unlock)
# — that bootstrap is out of scope here.
#
# Usage:
#   scripts/deploy.sh [DEVICE_IP]
#   DEVICE_IP=192.168.4.30 REBOOT=yes scripts/deploy.sh
#
# Env:
#   DEVICE_IP   speaker IP            (default 192.168.4.30; positional arg wins)
#   REBOOT      prompt | yes | no     (default prompt) — reboot after deploy to
#               apply the cloud-URL redirect (only read by the Bose app at boot)
#
set -euo pipefail

# --- configuration -----------------------------------------------------------
DEVICE_IP="${1:-${DEVICE_IP:-192.168.4.30}}"
REBOOT="${REBOOT:-prompt}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# On-device destinations.
BIN_DEST="/mnt/nv/preset-server"
INIT_DEST="/etc/init.d/preset-server"
SETPRESETS_DEST="/mnt/nv/set_presets.sh"
PRESETS_DEST="/mnt/nv/presets.conf"
CFG_DEST="/opt/Bose/etc/SoundTouchSdkPrivateCfg.xml"
CFG_BACKUP="/mnt/nv/SoundTouchSdkPrivateCfg.xml.orig"
STAGE="/mnt/nv/.deploy-stage"   # writable staging dir for rootfs-bound files

# Local source artifacts.
BUILD_OUT="$REPO_ROOT/preset-server-armv7"
INIT_SRC="$REPO_ROOT/scripts/preset-server.init"
SETPRESETS_SRC="$REPO_ROOT/scripts/set_presets.sh"
PRESETS_SRC="$REPO_ROOT/presets.conf"
CFG_SRC="$REPO_ROOT/scripts/SoundTouchSdkPrivateCfg.xml.redirect"

# SSH/SCP options: the device offers only the legacy ssh-rsa host key, has no
# persistent known_hosts worth checking, and has NO sftp subsystem (so scp must
# use the legacy -O protocol). These mirror SESSION-HANDOFF.md.
SSH_OPTS=(-o HostKeyAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no \
          -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8)
SSH=(ssh "${SSH_OPTS[@]}" "root@${DEVICE_IP}")
SCP=(scp -O "${SSH_OPTS[@]}")

# --- helpers -----------------------------------------------------------------
say()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32m✓ %s\033[0m\n' "$*"; }
die()  { printf '\033[31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

ssh_dev() { "${SSH[@]}" "$@"; }
push()    { "${SCP[@]}" "$1" "root@${DEVICE_IP}:$2"; }

# --- 0. preflight ------------------------------------------------------------
say "Preflight: toolchain + connectivity to ${DEVICE_IP}"
command -v go >/dev/null 2>&1 || die "go toolchain not found on PATH"
for f in "$INIT_SRC" "$SETPRESETS_SRC" "$PRESETS_SRC" "$CFG_SRC"; do
  [ -f "$f" ] || die "missing source artifact: $f"
done
ssh_dev true >/dev/null 2>&1 || die "cannot SSH to root@${DEVICE_IP} (is SSH enabled / on the same WiFi?)"
ok "device reachable, sources present"

# --- 1. build ----------------------------------------------------------------
say "Building ARMv7 binary (static, stripped)"
( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
    go build -ldflags="-s -w" -o "$BUILD_OUT" ./cmd/preset-server )
LOCAL_MD5="$(md5sum "$BUILD_OUT" | awk '{print $1}')"
ok "built $BUILD_OUT ($(du -h "$BUILD_OUT" | cut -f1), md5 ${LOCAL_MD5})"

# --- 2. stop any running instance (safely) -----------------------------------
say "Stopping any running preset-server"
# Prefer the installed init script (stops by PID file via start-stop-daemon).
if ssh_dev "test -x $INIT_DEST"; then
  ssh_dev "$INIT_DEST stop" || true
fi
# Fallback: kill whatever still holds :8000, BY PID from netstat. Never use
# `pkill -f preset-server`: this very SSH command line contains the string
# "preset-server", so a -f match would kill our own remote shell.
ssh_dev '
  pid=$(netstat -ltnp 2>/dev/null | awk "/:8000 /{print \$NF}" | cut -d/ -f1 | head -n1)
  if [ -n "$pid" ]; then echo "killing stray :8000 listener pid $pid"; kill "$pid" 2>/dev/null || true; fi
' || true
ok "stopped"

# --- 3. push artifacts -------------------------------------------------------
# /mnt/nv is always writable. Files bound for the read-only rootfs (init script,
# config XML) are staged here first, then copied into place inside one brief
# rootfs-rw window in step 4.
say "Pushing artifacts (scp -O)"
ssh_dev "mkdir -p $STAGE"
push "$BUILD_OUT"      "$BIN_DEST"
push "$PRESETS_SRC"    "$PRESETS_DEST"
push "$SETPRESETS_SRC" "$SETPRESETS_DEST"
push "$INIT_SRC"       "$STAGE/preset-server.init"
push "$CFG_SRC"        "$STAGE/SoundTouchSdkPrivateCfg.xml"
ssh_dev "chmod +x $BIN_DEST $SETPRESETS_DEST"
ok "binary + presets.conf + set_presets.sh + staged rootfs files pushed"

# Verify the binary survived the copy intact.
REMOTE_MD5="$(ssh_dev "md5sum $BIN_DEST | awk '{print \$1}'")"
[ "$REMOTE_MD5" = "$LOCAL_MD5" ] || die "binary md5 mismatch (local $LOCAL_MD5 != device $REMOTE_MD5)"
ok "binary md5 verified on device"

# --- 4. install init script + config redirect (rootfs rw window) -------------
# The init script (/etc/init.d) and the SDK config (/opt/Bose/etc) live on the
# read-only ubifs rootfs. Remount rw, do all rootfs writes, remount ro. We back
# up the *current* config as the stock .orig only if no backup exists yet, so a
# re-run never overwrites a good stock backup with an already-redirected file.
say "Installing init script + cloud-URL redirect (rootfs rw)"
ssh_dev "
  set -e
  # Guard: never silently drop config fields. If the live config has a top-level
  # element our redirect file lacks, abort instead of overwriting — this catches
  # a firmware whose stock config differs from the captured snapshot. Runs before
  # the rw remount, so a mismatch leaves the device untouched.
  for t in \$(grep -oE '<[a-zA-Z][a-zA-Z0-9]*>' $CFG_DEST | sort -u || true); do
    grep -qF \"\$t\" $STAGE/SoundTouchSdkPrivateCfg.xml || {
      echo \"ABORT: live config has \$t not in the redirect file; refusing to overwrite (would drop it).\" >&2
      exit 3
    }
  done
  # Always restore the rootfs to read-only on the way out, even if a cp fails
  # mid-window — otherwise set -e would exit with the rootfs left writable.
  trap 'mount -o remount,ro / 2>/dev/null || true' EXIT
  mount -o remount,rw /
  # boot persistence
  cp $STAGE/preset-server.init $INIT_DEST
  chmod +x $INIT_DEST
  # Back up the stock config once — but only when the current file is genuinely
  # stock (not already redirected), so we never snapshot a redirected file as the
  # '.orig' that recovery restores from.
  if [ ! -f $CFG_BACKUP ]; then
    if grep -q '127.0.0.1:8000' $CFG_DEST; then
      echo 'note: current config already redirected; not snapshotting it as stock (.orig).'
    else
      cp $CFG_DEST $CFG_BACKUP
    fi
  fi
  cp $STAGE/SoundTouchSdkPrivateCfg.xml $CFG_DEST
"
ssh_dev "update-rc.d preset-server defaults >/dev/null 2>&1 || true"
ssh_dev "rm -rf $STAGE"
ok "init installed + rc.d symlinks + cloud URLs redirected to 127.0.0.1:8000"

# --- 5. persistence of SSH + start -------------------------------------------
say "Ensuring SSH persistence + starting service"
ssh_dev "touch /mnt/nv/remote_services"   # keep passwordless SSH after reboot
# The init script's `start` polls /healthz itself and exits non-zero on failure.
ssh_dev "$INIT_DEST start"
ok "service started"

# --- 6. verify ---------------------------------------------------------------
say "Verifying health"
if ssh_dev "curl -fsS --max-time 4 http://localhost:8000/healthz" >/dev/null 2>&1; then
  ok "http://localhost:8000/healthz responding"
else
  die "service started but /healthz not responding — inspect: ${SSH[*]} 'logread | grep preset-server | tail'"
fi

# --- 7. reboot (to apply the cloud-URL redirect) -----------------------------
# The Bose app only reads SoundTouchSdkPrivateCfg.xml at boot, so the redirect
# (and thus preset playback) doesn't fully take effect until the speaker reboots.
do_reboot() { say "Rebooting speaker"; ssh_dev "reboot" || true; ok "reboot issued — give it ~60s"; }

case "$REBOOT" in
  yes) do_reboot ;;
  no)  say "Skipping reboot"; printf '    Reboot the speaker to apply the cloud-URL redirect.\n' ;;
  *)   if [ -t 0 ]; then
         printf '\n'
         # `|| ans=n`: a closed/EOF stdin must not abort the script under set -e
         # after a fully successful deploy.
         read -r -p "Reboot the speaker now to apply the redirect? [y/N] " ans || ans=n
         case "$ans" in [yY]*) do_reboot ;; *) printf '    Skipped. Reboot later to apply the redirect.\n' ;; esac
       else
         say "Skipping reboot (no TTY to prompt; set REBOOT=yes to force)"
         printf '    Reboot the speaker to apply the cloud-URL redirect.\n'
       fi ;;
esac

say "Deploy complete."
