# Bose SoundTouch 10 — Keep-Alive (`preset-server`)

Keep a **Bose SoundTouch 10** playing internet radio after Bose's official cloud
shutdown — with a tiny, **stdlib-only, zero-dependency** Go service that runs
*on the speaker itself* and stands in for just enough of Bose's dead cloud to get
the six preset buttons to play.

When Bose retired the SoundTouch cloud, the speaker's preset buttons stopped
working: a press resolves the preset against Bose's "marge" account service and
"BMX/Orion" stream adapter, both of which are gone. This project replaces only
that slice — a ~6 MB static ARMv7 binary serving the handful of endpoints the
firmware touches for `LOCAL_INTERNET_RADIO` playback — so each button plays a
hardcoded internet-radio stream. No cloud, no UI, no external dependencies.

It is **not** [AfterTouch](https://github.com/gesellix/Bose-SoundTouch) (the
broader community project); it deliberately implements only the minimal path the
ST 10 needs. The reference repo above was invaluable for reverse-engineering the
protocol, and the official Bose API docs are in `SoundTouch-Web-API.pdf`.

> **Note on identifiers.** All device-specific values in this repo (device ID,
> serial, account UUID, MAC, name) are **placeholders** — e.g. `AABBCCDDEEFF`,
> `EXAMPLESERIAL0000`, account `1234567`, `ExampleSpeaker`. Override them at
> runtime with the flags below; the speaker's real values are fetched live from
> its own `/info` endpoint on the first request.

## How it works

At boot the speaker runs a handshake against its (redirected) cloud URLs, then
resolves a preset on press:

```
boot:  GET /streaming/sourceproviders         → LOCAL_INTERNET_RADIO is a known source
       GET /streaming/account/{a}/full         → account doc: the source + the 6 presets
       GET /bmx/registry/v1/services           → Orion adapter is a valid source
press: POST .../orion/token                    → anonymous token
       GET  .../orion/station?data=<b64>        → playback JSON with the streamUrl
       speaker opens streamUrl                  → audio
```

`presets.conf` is the single source of truth for the six stations; the service
feeds it into `/full`, and the speaker adopts those presets at boot. See
**[`SERVICE.md`](SERVICE.md)** for the full architecture (endpoints, the
BMX/Orion adapter, the boot stubs, the on-device layout).

## Build

```sh
go test ./...

# Cross-compile for the speaker (ARMv7, static, stripped) — ~6 MB
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" \
  -o preset-server-armv7 ./cmd/preset-server
```

## Deploy

Prerequisite: passwordless root SSH enabled on the speaker (USB `remote_services`
unlock — see `SERVICE.md`). Then, from this repo:

```sh
scripts/deploy.sh [DEVICE_IP]      # default 192.168.4.30; or set DEVICE_IP / REBOOT=yes|no
```

`deploy.sh` rebuilds the binary, pushes every on-device artifact, installs boot
persistence, redirects the speaker's dead cloud URLs to the local service, starts
it, verifies `/healthz`, and offers to reboot. It is idempotent and safe to re-run
(including against a freshly factory-reset speaker, once SSH is back).

## Changing stations

1. Edit `presets.conf` (format: `Name | stream-url`; see `presets.conf.example`).
2. Replug the USB stick into the speaker, **or** re-run `scripts/deploy.sh` to
   push it to the on-device cache.
3. Reboot the speaker — `/full` repopulates all six presets from the new config.

## Recovery scripts

- **`scripts/deploy.sh`** — full (re)deploy after a reset or code change.
- **`scripts/provision-wifi.sh`** — speaker kicked off WiFi? Join its setup-mode
  AP and push your SSID/password via the firmware's own `/addWirelessProfile`
  API (the speaker encrypts and persists it itself).
- **`scripts/set_presets.sh`** — fast preset repair (pushes presets via
  `:8090/storePreset`) if presets get cleared without a reboot.
- **`scripts/preset-server.init`** — SysV init script installed on the device for
  boot persistence.

Each script has a self-contained `*_test.sh` (where applicable) that runs without
touching the device.

## Repo layout

```
cmd/preset-server/      entry point + flags
internal/preset/        the service: marge /full, BMX/Orion, boot stubs, config
scripts/                deploy, wifi-provision, init script, preset recovery (+ tests)
device-backups/         stock config + original presets (secret-bearing captures are gitignored)
SERVICE.md              architecture + on-device deployment detail
presets.conf(.example)  the six stations
```
