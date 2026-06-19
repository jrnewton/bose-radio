# preset-server — minimal on-device preset emulator

A tiny, **stdlib-only, zero-dependency** Go service that makes the six preset
buttons on a Bose SoundTouch 10 play hardcoded internet-radio streams after
Bose's cloud shutdown. It runs *on the speaker* (a ~6 MB static ARMv7 binary)
and stands in for just enough of Bose's cloud — the "marge" account service and
the "BMX/Orion" stream adapter — to get a button press to a stream, over plain
HTTP, with no UI and no external dependencies.

It is *not* AfterTouch; it deliberately implements only the slice of the cloud
the SoundTouch 10 touches for `LOCAL_INTERNET_RADIO` playback.

## How it actually works

At boot the speaker runs a handshake against its configured cloud URLs (which we
redirect to this service), then resolves a preset on press. The full chain:

```
boot: GET /streaming/sourceproviders        → LOCAL_INTERNET_RADIO is a known source
      GET /streaming/account/{a}/full        → account doc: the source + the 6 presets
      GET /bmx/registry/v1/services          → Orion adapter is a valid source
press: POST .../orion/token                  → anonymous token
       GET  .../orion/station?data=<b64>      → playback JSON with the streamUrl
       speaker opens streamUrl                → audio
```

The endpoints we serve (all under the redirected cloud hosts):

- **`GET /streaming/account/{a}/full`** — the account document; source-of-truth
  for the 6 presets + the `LOCAL_INTERNET_RADIO` source (`full.go`).
- **`GET /streaming/sourceproviders`** — provider catalog incl.
  `LOCAL_INTERNET_RADIO` (id 11).
- **`GET /bmx/registry/v1/services`** (+ `…/servicesAvailability`) — advertises
  the Orion adapter so the source is valid (`bmx.go`).
- **Orion adapter** — `GET .../orion` (descriptor), `POST .../orion/token` (anon
  token), `GET .../orion/station?data=` (the stream resolver).
- **Non-blocking boot calls, stubbed so they don't 404** — `power_on`,
  `blacklist` (405), `streaming_token`, `provider_settings`, `device/{d}/group`.
- **`POST /v1/scmudc/*`** — telemetry sink (`200`, so failures can't stall playback).
- **`GET /media/*`** — placeholder for advertised (cosmetic) BMX icons.

The legacy `GET /streaming/.../presets` endpoint is also served but the speaker
doesn't use it for this flow (it uses `/full`).

## Stations and updating them

Stations come from a small pipe-delimited file (`presets.conf`, see
`presets.conf.example`). The service reads it from the USB stick
(`/media/sda1/presets.conf`) and caches the last good copy to persistent NAND
(`/mnt/nv/presets.conf`), so playback survives the stick being removed.

`presets.conf` is the **single source of truth**: it feeds `/full`, and the
speaker adopts the presets from `/full` at boot (verified — clearing the
speaker's `Presets.xml` and rebooting repopulates all 6 from `/full`).

**To change stations:** edit `presets.conf` on a computer, replug the stick,
**reboot the speaker.** That's the whole workflow. (`set_presets.sh`, which
pushes presets via the `:8090/storePreset` API, is now only a fast recovery tool
if presets ever get cleared — not part of the normal update path.)

## On-device deployment

1. **SSH** enabled via the `remote_services` USB unlock, persisted with
   `touch /mnt/nv/remote_services`.
2. **Binary** at `/mnt/nv/preset-server`; **boot persistence** via an
   `/etc/init.d/preset-server` SysV script (`start-stop-daemon --background`,
   registered with `update-rc.d`). Flags live in the script's `DAEMON_ARGS`.
3. **Redirect** the speaker's cloud URLs to the service in
   `/opt/Bose/etc/SoundTouchSdkPrivateCfg.xml` (remount rootfs `rw` to edit):
   `margeServerUrl`, `statsServerUrl`, and `bmxRegistryUrl` →
   `http://127.0.0.1:8000…`. Plain HTTP, so no certificate work. Reboot to apply.

## Build

```sh
go test ./...

# Cross-compile for the speaker (ARMv7, static, stripped) — ~6 MB
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" \
  -o preset-server-armv7 ./cmd/preset-server
```

## Run (flags)

```
-listen           address to listen on               (default ":8000")
-base-url         absolute URL the speaker uses       (default "http://127.0.0.1:8000")
-usb-config       config path on the USB stick        (default "/media/sda1/presets.conf")
-cache            persistent cache path               (default "/mnt/nv/presets.conf")
-mount-wait       USB mount wait at startup           (default 30s)
-reload-interval  re-read config for changes; 0=off   (default 15s)
-info-url         device-info URL for /full identity  (default "http://localhost:8090/info")
-last-full        persist last served /full for debug (default "/mnt/nv/last-full.xml")
-device-id -firmware -serial -product-code -device-name   /full identity fallbacks
-log-requests     log every request to syslog         (default false; on = debugging)
```

## Recovery

- **Presets cleared:** re-run `set_presets.sh` (≈5 s) or just reboot (`/full`
  repopulates them).
- **Full state:** a `BoseApp-Persistence` tar snapshot is kept (device +
  `device-backups/`); restore it, then **hard power-cycle** (a graceful reboot
  may flush the running app's state over the restore).
- **Undo the redirect:** restore `/mnt/nv/SoundTouchSdkPrivateCfg.xml.orig` over
  `/opt/Bose/etc/SoundTouchSdkPrivateCfg.xml` and reboot.
</content>
