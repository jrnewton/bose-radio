# preset-server — minimal on-device preset emulator

A tiny, stdlib-only Go service that makes the six preset buttons on a Bose
SoundTouch 10 play hardcoded internet-radio streams after Bose's cloud
shutdown. It is *not* AfterTouch: it emulates only the two cloud endpoints the
preset-press flow actually touches, over plain HTTP, with no TLS, no DNS
server, no station resolution, and no UI.

## How it works

When the speaker boots (or re-polls), it fetches its preset list from the Bose
"marge" streaming endpoint; when a button is pressed, it resolves that preset's
`LOCAL_INTERNET_RADIO` ContentItem against a Bose "Orion" BMX adapter URL and
opens the returned `streamUrl`. We serve both:

1. `GET /streaming/account/{a}/device/{d}/presets`
   → XML listing 6 presets. Each preset's `location` points back at us (2) with
   the station's `{name, streamUrl}` base64-encoded in a `?data=` blob. Sends an
   `ETag`; answers `304` to conditional re-polls.
2. `GET /core02/svc-bmx-adapter-orion/prod/orion/station?data=<b64>`
   → decodes the blob and returns playback JSON `{audio:{streamUrl},name,...}`.
   The speaker then opens `streamUrl` directly.
3. `POST /v1/scmudc/*` → `200` (telemetry sink, so failures can't stall playback).

Redirect the speaker to us by pointing `margeServerUrl` at the service
(`http://127.0.0.1:8000` when running on-device), via `SoundTouchSdkPrivateCfg.xml`
or the telnet `sys configuration` command. Because everything is plain HTTP,
there is no certificate work.

## Config

Stations come from a small pipe-delimited file (see `presets.conf.example`).
The service reads it from the USB stick (`/media/sda1/presets.conf`) and copies
the last good copy to a persistent cache (`/mnt/nv/presets.conf`), so:

- Leave the stick in, or remove it after first boot — playback survives either
  way (it falls back to the cache).
- To change stations: edit `presets.conf` on a computer, replug the stick,
  power-cycle the speaker.

The USB mount is asynchronous and races startup, so the service waits up to
30 s for the stick before falling back to the cache.

## Build

```sh
# Local tests
go test ./...

# Cross-compile for the speaker (ARMv7, static, stripped) — ~5.7 MB
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" \
  -o preset-server-armv7 ./cmd/preset-server
```

## Run (flags)

```
-listen           address to listen on            (default ":8000")
-base-url         absolute URL the speaker uses    (default "http://127.0.0.1:8000")
-usb-config       config path on the USB stick     (default "/media/sda1/presets.conf")
-cache            persistent cache path            (default "/mnt/nv/presets.conf")
-mount-wait       USB mount wait at startup        (default 30s)
-reload-interval  re-read config for changes; 0=off (default 15s)
```

## Status / unknowns to verify on the real device (FW 27.0.6)

These are reverse-engineered from the reference repo and not yet confirmed on
the actual speaker:

- Does FW 27.x accept an `http://` (not `https://`) `margeServerUrl`?
- Exact preset XML schema the firmware tolerates (`vnd.bose.streaming-v1.2+xml`).
- Whether `/media/sda1` is the real mount path on this unit.
- Stream playability: WCUW (HTTPS, port 5495) and WMBR (HTTPS, port 8002) are
  the two non-plain-http streams — test those buttons first.
