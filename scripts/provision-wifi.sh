#!/usr/bin/env bash
#
# provision-wifi.sh — get a SoundTouch 10 back onto your WiFi after a reset.
#
# It prompts for SSID/password and POSTs them to the speaker's own setup API
# (/addWirelessProfile). The speaker then encrypts the passphrase with its
# per-device key and writes NetworkProfiles.xml ITSELF — which is why this works
# and hand-building NetworkProfiles.xml does not: Bose's passphrase encryption
# (encrypted="true") is keyed to the device and not reproducible off-box.
#
# WHEN TO USE: a factory reset dropped the speaker off WiFi. On power-up it
# creates its own setup-mode WiFi access point.
#   1. Join the speaker's setup WiFi from this machine (it serves DHCP; the
#      speaker is reachable at 192.0.2.1 — the default HOST below).
#   2. Run this script and answer the prompts.
#   3. The speaker leaves AP mode and joins your network within ~30s; then run
#      scripts/deploy.sh to (re)deploy preset-server.
#
# It can also target an already-online speaker (HOST=192.168.4.30) to add or
# update a network, though that path is less commonly needed.
#
# Usage:
#   scripts/provision-wifi.sh [HOST]
#   HOST=192.0.2.1 SECURITY=wpa_or_wpa2 scripts/provision-wifi.sh
#
# Env / args:
#   HOST      speaker setup-mode address   (default 192.0.2.1; positional arg wins)
#   PORT      SoundTouch API port          (default 8090)
#   SECURITY  security type                (default wpa_or_wpa2; prompt can override)
#
set -euo pipefail

HOST="${1:-${HOST:-192.0.2.1}}"
PORT="${PORT:-8090}"
SECURITY="${SECURITY:-wpa_or_wpa2}"
URL="http://${HOST}:${PORT}/addWirelessProfile"

# xml_attr_escape escapes the five XML predefined entities so an SSID/password
# containing & < > " ' can't break out of the attribute or corrupt the document.
# Note the sed replacement uses \& to emit a literal ampersand (a bare & means
# "the whole match" in sed) — the classic gotcha called out in SESSION-HANDOFF.md.
xml_attr_escape() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' \
                         -e 's/"/\&quot;/g' -e "s/'/\&apos;/g"
}

# build_body assembles the AddWirelessProfile XML from escaped fields. Kept as a
# function so the test can exercise escaping without performing a POST.
build_body() {
  local ssid="$1" pass="$2" sec="$3"
  printf '<AddWirelessProfile><profile ssid="%s" password="%s" securityType="%s" /></AddWirelessProfile>' \
    "$(xml_attr_escape "$ssid")" "$(xml_attr_escape "$pass")" "$(xml_attr_escape "$sec")"
}

# post_profile POSTs the body to the speaker, retrying once on a transport
# failure (the setup endpoint empirically races its own readiness on the first
# POST). It captures both the HTTP status and the response body — note NO `-f`,
# so a 4xx/5xx body is preserved rather than discarded — and reports three
# distinct outcomes so a caller never mistakes a rejection for success:
#   return 0  → 2xx, accepted              (body in $LAST_BODY)
#   return 2  → reached speaker, non-2xx   (it rejected the request; body kept)
#   return 1  → transport/connectivity failure (could not reach the endpoint)
LAST_BODY=""
LAST_CODE=""
post_profile() {
  local body="$1" attempt resp rc
  for attempt in 1 2; do
    # -w appends the HTTP status on its own trailing line.
    resp=$(curl -sS --max-time 12 -H 'Content-Type: text/xml' --data "$body" \
                -w '\n%{http_code}' "$URL" 2>&1); rc=$?
    if [ "$rc" -eq 0 ]; then
      LAST_CODE=$(printf '%s' "$resp" | tail -n1)
      LAST_BODY=$(printf '%s' "$resp" | sed '$d')
      case "$LAST_CODE" in
        2*) return 0 ;;   # accepted
        *)  return 2 ;;   # reached the speaker, but it rejected the request
      esac
    fi
    LAST_BODY="$resp"     # rc != 0: transport failure (refused, timeout, DNS)
    [ "$attempt" -eq 1 ] && { echo "  first attempt failed (endpoint may still be coming up); retrying in 2s..." >&2; sleep 2; }
  done
  return 1
}

# Guard against sourcing (the test sources this file to call the helpers).
[ "${PROVISION_WIFI_LIB:-}" = "1" ] && return 0

main() {
  echo "Provision WiFi on SoundTouch at ${HOST}:${PORT}"
  printf 'WiFi SSID: '
  IFS= read -r SSID
  [ -n "$SSID" ] || { echo "ERROR: SSID is required." >&2; exit 1; }

  printf 'WiFi password: '
  IFS= read -rs PASS; echo
  [ -n "$PASS" ] || { echo "ERROR: password is required." >&2; exit 1; }

  printf 'Security type [%s]: ' "$SECURITY"
  IFS= read -r sec_in
  [ -n "$sec_in" ] && SECURITY="$sec_in"

  echo "Pushing profile for SSID \"$SSID\" to $URL ..."
  rc=0
  post_profile "$(build_body "$SSID" "$PASS" "$SECURITY")" || rc=$?
  case "$rc" in
    0)
      echo "✓ WiFi profile accepted. The speaker will leave setup mode and join \"$SSID\" within ~30s."
      echo "  Next: reconnect this machine to your normal WiFi, then run scripts/deploy.sh"
      ;;
    2)
      # Reached the speaker but it rejected the request — an app-level problem,
      # not connectivity. Surface the body so the cause is actionable.
      echo "ERROR: the speaker rejected the request (HTTP ${LAST_CODE})." >&2
      [ -n "$LAST_BODY" ] && echo "       response: ${LAST_BODY}" >&2
      echo "       Check the SSID, password, and security type (sent: ${SECURITY})." >&2
      exit 1
      ;;
    *)
      # Never reached the endpoint — a genuine connectivity problem.
      echo "ERROR: could not reach ${URL} (connectivity)." >&2
      [ -n "$LAST_BODY" ] && echo "       detail: ${LAST_BODY}" >&2
      echo "       Confirm you are joined to the speaker's setup WiFi and HOST is correct." >&2
      exit 1
      ;;
  esac
}

main "$@"
