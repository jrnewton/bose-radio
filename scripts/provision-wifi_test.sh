#!/usr/bin/env bash
#
# provision-wifi_test.sh — unit/integration tests for provision-wifi.sh.
# No device contact: escaping is tested directly, and the POST is exercised
# against a throwaway local HTTP server that captures the request body.
# Run: bash scripts/provision-wifi_test.sh
#
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/provision-wifi.sh"
fails=0
pass() { printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fails=$((fails + 1)); }

echo "== syntax =="
if bash -n "$SCRIPT"; then pass "bash -n parses"; else fail "parse error"; fi

# Source the helpers (the lib guard makes the script return before main()).
# No HOST/PORT in the env, so URL resolves to the real device-facing default.
PROVISION_WIFI_LIB=1 . "$SCRIPT"

echo "== default endpoint pins the real SoundTouch API port + path =="
# Guards against the default PORT (8090) or the /addWirelessProfile path silently
# changing — the live-POST test below overrides URL to a throwaway port, so it
# would not otherwise catch a wrong default shipping.
case "$URL" in
  http://192.0.2.1:8090/addWirelessProfile) pass "default URL = $URL" ;;
  *) fail "default URL wrong: $URL (expected http://192.0.2.1:8090/addWirelessProfile)" ;;
esac

echo "== xml_attr_escape escapes all five entities =="
got="$(xml_attr_escape 'a&b<c>d"e'\''f')"
want='a&amp;b&lt;c&gt;d&quot;e&apos;f'
[ "$got" = "$want" ] && pass "escapes & < > \" '" || fail "escape mismatch: got [$got] want [$want]"

echo "== build_body produces well-formed, escaped XML =="
body="$(build_body 'My&Net' 'p<a>ss"x' 'wpa_or_wpa2')"
case "$body" in
  *'ssid="My&amp;Net"'*)        pass "ssid escaped in body" ;; *) fail "ssid not escaped: $body" ;;
esac
case "$body" in
  *'password="p&lt;a&gt;ss&quot;x"'*) pass "password escaped in body" ;; *) fail "password not escaped: $body" ;;
esac
case "$body" in
  *'<AddWirelessProfile><profile '*'securityType="wpa_or_wpa2" /></AddWirelessProfile>') pass "envelope shape correct" ;;
  *) fail "envelope shape wrong: $body" ;;
esac

echo "== post_profile actually POSTs the body (local capture server) =="
if ! command -v python3 >/dev/null 2>&1; then
  fail "python3 unavailable — skipping live-POST test"
else
  CAP="$(mktemp)"
  PORTFILE="$(mktemp)"
  python3 - "$CAP" "$PORTFILE" <<'PY' &
import http.server, socketserver, sys
cap, portfile = sys.argv[1], sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        open(cap, 'wb').write(self.path.encode() + b'\n' + self.rfile.read(n))
        self.send_response(200); self.end_headers(); self.wfile.write(b'OK')
    def log_message(self, *a): pass
srv = socketserver.TCPServer(('127.0.0.1', 0), H)
open(portfile, 'w').write(str(srv.server_address[1]))
srv.handle_request()  # serve exactly one request, then exit
PY
  SRV_PID=$!
  # Wait for the server to publish its port.
  for _ in $(seq 1 50); do [ -s "$PORTFILE" ] && break; sleep 0.1; done
  TESTPORT="$(cat "$PORTFILE")"

  if [ -n "$TESTPORT" ]; then
    URL="http://127.0.0.1:${TESTPORT}/addWirelessProfile"   # override the lib's URL
    if post_profile "$(build_body 'HomeNet' 'secret&pw' 'wpa_or_wpa2')"; then
      pass "post_profile returned success on HTTP 200"
    else
      fail "post_profile failed against local server"
    fi
    wait "$SRV_PID" 2>/dev/null
    captured="$(cat "$CAP")"
    case "$captured" in
      */addWirelessProfile*) pass "POSTed to /addWirelessProfile path" ;;
      *) fail "wrong path captured: $captured" ;;
    esac
    case "$captured" in
      *'ssid="HomeNet"'*'password="secret&amp;pw"'*) pass "server received escaped credentials" ;;
      *) fail "body not received as expected: $captured" ;;
    esac
  else
    kill "$SRV_PID" 2>/dev/null
    fail "local server never published a port"
  fi
  rm -f "$CAP" "$PORTFILE"

  echo "== post_profile reports an HTTP rejection as code 2 (not success) =="
  PORTFILE2="$(mktemp)"
  python3 - "$PORTFILE2" <<'PY' &
import http.server, socketserver, sys
portfile = sys.argv[1]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', 0)))
        body = b'<errors><error>invalid securityType</error></errors>'
        self.send_response(400); self.send_header('Content-Length', str(len(body)))
        self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
srv = socketserver.TCPServer(('127.0.0.1', 0), H)
open(portfile, 'w').write(str(srv.server_address[1]))
srv.handle_request()
PY
  SRV2_PID=$!
  for _ in $(seq 1 50); do [ -s "$PORTFILE2" ] && break; sleep 0.1; done
  TESTPORT2="$(cat "$PORTFILE2")"
  if [ -n "$TESTPORT2" ]; then
    URL="http://127.0.0.1:${TESTPORT2}/addWirelessProfile"
    rc=0; post_profile "$(build_body 'HomeNet' 'pw' 'bogus')" || rc=$?
    wait "$SRV2_PID" 2>/dev/null
    [ "$rc" = "2" ] && pass "rejection returns code 2 (got $rc)" || fail "expected code 2 on HTTP 400, got $rc"
    [ "$LAST_CODE" = "400" ] && pass "LAST_CODE captured (400)" || fail "LAST_CODE = '$LAST_CODE', want 400"
    case "$LAST_BODY" in *'invalid securityType'*) pass "rejection body surfaced in LAST_BODY" ;; *) fail "LAST_BODY missing server error: '$LAST_BODY'" ;; esac
  else
    kill "$SRV2_PID" 2>/dev/null; fail "rejection-test server never published a port"
  fi
  rm -f "$PORTFILE2"
fi

echo
if [ "$fails" -eq 0 ]; then
  printf '\033[32mAll provision-wifi checks passed.\033[0m\n'; exit 0
else
  printf '\033[31m%d check(s) failed.\033[0m\n' "$fails"; exit 1
fi
