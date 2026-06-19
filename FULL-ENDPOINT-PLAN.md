# Plan: Implement the marge `/full` endpoint (the last blocker for preset playback)

## Context

We're keeping a Bose SoundTouch 10 (cloud-EOL) alive with our own minimal,
stdlib-only Go service (`preset-server`) that makes the 6 preset buttons play
hardcoded internet-radio streams. Over this session we built and **verified on the
real device**: the marge presets endpoint, the Orion station+token endpoints, the
BMX registry (advertising `LOCAL_INTERNET_RADIO`), `sourceproviders`, `power_on`,
`blacklist`, telemetry, request logging, plus on-device plumbing (SSH unlock,
init-script boot persistence, URL redirects, preset rewrite via
`:8090/storePreset`).

Request logging traced the exact boot handshake: registry `200`, sourceproviders
`200`, then **`GET /streaming/account/1234567/full -> 404`**, and it stops — never
reaching the Orion `/token` → `/station` flow. `now_playing` shows
`source="LOCAL_INTERNET_RADIO"` / `PLAY_STATE` but **no audio** (no stream
connection). `/full` is the account document the speaker reconciles its state
against at boot; without it, playback never resolves.

`/full` is the one risky endpoint: the speaker decodes the XML body into a
**protobuf-backed model with required fields**, and a malformed body — i.e. a
**required element absent** (e.g. a `<source>` missing `<sourceproviderid>`) —
makes it **abort the sync and wipe local presets** (reference GH-269). De-risked:
full state snapshot (`device-backups/BoseApp-Persistence-backup.tgz` + on-device)
and ~5s preset re-push via `set_presets.sh`.

**Outcome:** serve a complete, well-formed `/full` for our radio-only scope (one
`LOCAL_INTERNET_RADIO` source) so the speaker finishes its boot sync and streams
on button press.

> Incorporates architect review: enumerate required fields rather than guess what
> the speaker validates; **lead with populated presets** (treat a wipe as the
> expected outcome of an empty doc, not a tail risk); precise XML→protobuf
> framing; lazy device-identity fetch; current timestamps; tested backup.

## Design decisions (from review)

1. **Populated presets, not empty.** An authoritative `/full` with no `<presets>`
   plausibly means "reconcile to zero" (a wipe instruction), not a no-op — we have
   no positive evidence it's a no-op. So `/full` carries all **6 presets**, each a
   well-formed `LOCAL_INTERNET_RADIO` block. This **reuses the same source block**
   we must get right for `<sources>` anyway, so it adds no new failure mode while
   removing the keep/wipe ambiguity. Consequence: `storePreset` likely becomes
   **recovery-only** (presets then flow `presets.conf → /full → device`); we keep
   it as the proven recovery path. Still go in **expecting a possible wipe** on
   first serve, with `set_presets.sh` ready.
2. **Enumerate required fields; don't reason about what's checked.** Emit *every*
   field the reference emits non-empty — for account / device / attachedProduct
   and every `<source>` (account-level and per-preset). The proxy for "required"
   is "non-`omitempty` in the reference Go struct" (`models.go`); it's a proxy,
   not the device's proto schema, but the reference runs on real hardware, so
   "emit what it emits" is the safe rule. A golden-file test pins this.
3. **Framing:** XML on the wire (`application/vnd.bose.streaming-v1.2+xml`)
   decoded into a protobuf-backed model; failure = required element absent.
4. **Identity fetched lazily**, not hardcoded/startup-fetched (below).
5. **Current timestamps**, not stale 2019 dates (below).

## Schema (reference: `pkg/models/models.go`, `pkg/service/marge/marge.go`)

`AccountFullResponse` → `<account id>` (echo `r.PathValue("account")`):
- `<accountStatus>OK</accountStatus>`, `<mode>global</mode>`,
  `<preferredLanguage>en</preferredLanguage>`
- `<devices><device deviceid="…">`: required `attachedProduct` (`product_code`
  attr + `<productlabel>`, `<serialnumber>`, `<updatedOn>`, empty `<components/>`),
  `createdOn`, `firmwareVersion`, `ipaddress`, `name`, `serialNumber`, `updatedOn`
  — **all non-empty** — plus a populated `<presets>` (see below).
- `<sources><source>`: the `LOCAL_INTERNET_RADIO` block.

`LOCAL_INTERNET_RADIO` source block (`datastore.go:2361`, `mapToFullResponseSource`),
every element present:
```xml
<source id="10003" type="Audio">
  <createdOn>{recent}</createdOn>
  <credential type="token">PRESET-SERVER-ANON</credential>
  <name>LOCAL_INTERNET_RADIO</name>
  <sourceproviderid>11</sourceproviderid>   <!-- REQUIRED, never omit -->
  <sourcename></sourcename>
  <sourceSettings></sourceSettings>
  <updatedOn>{recent}</updatedOn>
  <username></username>
</source>
```

Each `<preset buttonNumber="N">` (`FullResponsePreset`) carries `containerArt`,
`contentItemType`, `createdOn`, `location` (our Orion URL, same as `storePreset`),
`name`, a nested well-formed `<source>` (the block above), `updatedOn`, `username`
— matching our 6 `presets.conf` stations. Content-Type
`application/vnd.bose.streaming-v1.2+xml`; XML header
`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`.

## Implementation

**1. `internal/preset/full.go` (new)**
- Structs for the subset of `AccountFullResponse` we emit; **no `omitempty` on any
  required field** (notably `sourceproviderid`, and every envelope field we list).
- `deviceIdentity` (deviceid, name, serial, firmware, ip, product_code, product
  label) resolved by `identity()` — **lazy fetch** of `http://localhost:8090/info`
  guarded by `sync.Once`, parsed for the real values, **falling back to flags** if
  the fetch fails. Safe because the speaker only asks for `/full` once its own
  `:8090` is up, so no boot race.
- `handleAccountFull(w, r)`: `account := r.PathValue("account")`; build the doc
  (identity + 6 populated `LOCAL_INTERNET_RADIO` presets from the served config +
  one account-level source); current timestamps; `xml.Marshal` + header; compute
  ETag over the body; honor `If-None-Match` → `304`. **Built per-request** (cold
  path) — no startup precompute, so no cache to guard against lazy identity.
- On serve, **persist the exact body** to `/mnt/nv/last-full.xml` and log
  `method path -> status (len, etag)` for post-mortem diffing.
- Follow patterns in `internal/preset/marge.go` / `bmx.go`; reuse `makeETag`.

**2. Route — `internal/preset/server.go` `Handler()`**
- `mux.HandleFunc("GET /streaming/account/{account}/full", s.handleAccountFull)`.

**3. Identity flags — `cmd/preset-server/main.go`** (fallbacks only): `-device-id`,
`-firmware`, `-serial`, `-product-code`, `-device-name`, defaulted to this device's
known values (provisioned from a one-time `:8090/info` read so they're
known-correct). **No `-account-id`** — echo the path.

**4. Tests — `internal/preset/full_test.go`**
- `GET /streaming/account/1234567/full` → 200, xml content-type, valid XML;
  `<account id>` echoes the path; `accountStatus=OK`; exactly one account-level
  `<source>` with `sourceproviderid=11`/`name=LOCAL_INTERNET_RADIO`; 6 `<preset>`
  each with a nested well-formed source.
- **Golden-file / required-field test:** assert presence + non-emptiness of *every*
  enumerated required field (envelope + each source) so a refactor can't silently
  drop one and reintroduce the wipe.
- Conditional GET with the ETag → `304`. Keep all existing suites green.

**Critical files:** `internal/preset/full.go` (new), `internal/preset/full_test.go`
(new), `internal/preset/server.go` (route), `cmd/preset-server/main.go` (identity
flags). Reference patterns: `internal/preset/marge.go`, `internal/preset/bmx.go`.

## Deploy & verify (end-to-end)

1. `gofmt`, `go vet ./...`, `go test ./...`; cross-compile
   `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w"`.
2. **Confirm v1.2 minor:** capture our device's actual `/full` request headers
   (tcpdump) and verify `Accept: …streaming-v1.2+xml` — adjust if firmware
   27.0.6.46330.5043500 expects a different minor.
3. **Backup + tested restore:** re-snapshot `/mnt/nv/BoseApp-Persistence`; verify
   archive integrity (`tar tzf` lists expected files) and **rehearse the restore
   procedure** (extract to a temp dir, diff) so the backstop is known-good, not a
   guess.
4. Deploy: `/etc/init.d/preset-server stop` → kill any `:8000` listener **by PID
   from `netstat -ltnp`** (not `pgrep -f preset-server` — it self-matches the ssh
   shell) → `scp -O` binary → `start` → verify md5.
5. Reboot; wait for `:8090`; read `logread | grep preset-server:` — confirm
   **`/streaming/account/…/full -> 200`** then **`POST …/orion/token`** then
   **`GET …/orion/station`**.
6. **Expect a possible preset wipe here.** Check `:8090/presets` shows all 6
   `LOCAL_INTERNET_RADIO` presets; if wiped, re-run `set_presets.sh` (~5s) and, if
   it recurs, diff `/mnt/nv/last-full.xml` against the reference's known-good to
   find the absent/wrong required element.
7. **Audio confirmation:** press button 2 (WGBH, HTTP) via `POST :8090/key
   PRESET_2`; verify `:8090/now_playing` is `PLAY_STATE` AND `netstat -tn | grep
   ESTABLISHED` shows a connection to `wgbh-live.streamguys1.com`; then **user
   listens**. Test HTTP streams first; the HTTPS/non-443 ones (WCUW :5495,
   WMBR :8002) are a separate device-player question and tested last.

## Risks & fallbacks

- **Preset wipe (now expected, not tail):** `storePreset` re-push (~5s) +
  tested tgz restore for anything beyond presets.
- **Timestamp reconciliation direction:** if `/full` is ignored as "older than
  local," presets stall again — current timestamps should make `/full` win;
  confirm the direction empirically.
- **More endpoints after `/full`:** if the log shows new `404`s before `/token`,
  handle incrementally — but the device already carrying a local
  `LOCAL_INTERNET_RADIO` source in `Sources.xml` suggests `/full` is the last gate.
- **HTTPS/non-443 streams:** device-player capability, independent of this change;
  isolated by testing HTTP first.

## Out of scope (radio only)

No Spotify/Amazon/account-bound sources, no TuneIn adapter, no UI, no
`providerSettings` (none needed for `LOCAL_INTERNET_RADIO`).
</content>
