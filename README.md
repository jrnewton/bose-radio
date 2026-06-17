# Bose SoundTouch 10 — Keep-Alive Project

Goal: keep my Bose SoundTouch 10 useful after Bose's official cloud shutdown,
using the community [gesellix/Bose-SoundTouch](https://github.com/gesellix/Bose-SoundTouch)
project (AfterTouch). Specifically, install/configure the **"On Device"** service
so the speaker runs AfterTouch locally rather than depending on a server elsewhere
on the network.

Reference repo cloned to `/tmp/Bose-SoundTouch/`.
Official Bose API docs: `./SoundTouch-Web-API.pdf`.

## Device

| Field          | Value                                       |
| -------------- | ------------------------------------------- |
| Model          | SoundTouch 10                               |
| Friendly name  | גָדוֹל (Hebrew, "big/great")                |
| IP             | 192.168.4.30 (on the 192.168.4.0/22 subnet) |
| Device ID      | B0D5CC1918A7                                |
| MAC (SCM)      | B0:D5:CC:19:18:A7                           |
| MAC (SMSC)     | 04:A3:16:43:46:8A                           |
| Module type    | sm2                                         |
| Variant        | rhino                                       |
| Firmware       | 27.0.6.46330.5043500 (build 2022-08-04)     |
| Region/Country | US                                          |

## Network reachability

Open TCP ports observed on the device:

| Port  | Purpose                                            |
| ----- | -------------------------------------------------- |
| 80    | HTTP (likely UPnP / device description)            |
| 8080  | (probing)                                          |
| 8090  | SoundTouch Web API (`/info`, `/now_playing`, etc.) |
| 8091  | (probing)                                          |
| 17000 | Notification websocket                             |
| 22    | **CLOSED** — SSH not enabled yet                   |

## Plan

### Step 1 — Enable SSH on the speaker

There are two known methods. Both make port 22 available with passwordless
root login.

**Method A: USB stick (canonical, requires physical access)**

We're preparing the stick under **Windows**. The end-state is:

- USB drive formatted as **FAT32**.
- A single empty file named `remote_services` (no extension, lowercase) at
  the **root** of the drive.

#### A.1 — Format as FAT32

File Explorer → right-click the drive → **Format…** → File system **FAT32**
→ Quick Format → Start. If the stick is > 32 GB, Windows hides FAT32; use
[Rufus](https://rufus.ie/) instead.

#### A.2 — Create the `remote_services` file

Pick the method that won't accidentally append `.txt`:

PowerShell:
```powershell
New-Item -Path E:\remote_services -ItemType File
```
or cmd:
```cmd
type nul > E:\remote_services
```
or File Explorer (enable **View → File name extensions** first, then New →
Text Document, then rename to `remote_services` deleting the `.txt`).

Verify with `dir E:\` — should show `remote_services`, 0 bytes, no extension.

#### A.3 — Eject

Use **Safely Remove Hardware** so the FS is flushed before unplugging.

#### A.4 — Insert and reboot the speaker

1. Speaker powered on.
2. Plug stick into speaker's USB port.
3. Unplug speaker power, wait ~5 s, plug back in.
4. Wait for full boot.
5. If port 22 doesn't open, retry while holding **`4` + `Volume −`** on the
   speaker during power-on (forces a USB scan on some ST 10 units).

#### A.5 — Connect

```
ssh -oHostKeyAlgorithms=+ssh-rsa root@192.168.4.30
```

No password expected.

#### A.6 — (Optional) Persist SSH so the USB stick isn't needed

Once logged in:
```
touch /mnt/nv/remote_services
```

#### A.7 — Bootable-flag fallback

If A.5 fails to find port 22 and A.4's button-combo retry also fails, the
stick's MBR partition may need the "active/bootable" flag set (see
[SoundCork issue #172](https://github.com/deborahgu/soundcork/issues/172)).
Easiest fix on Windows: re-format with **Rufus** (it sets the flag), or use
`diskpart` (`select disk N` → `select partition 1` → `active` —
**triple-check the disk number**).

**Method B: TAP / telnet on port 17000 (legacy, no USB needed)**

Older firmware exposes a debug "TAP" interface on telnet port 17000. Sending
`remote_services on` enables sshd. Port 17000 is open on our device, so this
is worth trying first — *if* this firmware still honors the command (later
firmware revisions removed it). No reboot required on success.

### Step 2 — Run the on-device installer

Verified against the cloned repo (`scripts/on-device-install/install.sh`,
2026-06-17). The one-liner downloads the ARMv7 binary + init script, installs
to **`/mnt/nv/aftertouch`** (persistent partition — *not* rootfs), symlinks
`/opt/aftertouch`, backs up any prior binary, registers an init script via
`update-rc.d`, and starts the service. Default version is **v0.107.0**; pin a
version with `VERSION=x.y.z` or `sh -s -- --version x.y.z`.

```
rw && curl -sSL https://raw.githubusercontent.com/gesellix/Bose-SoundTouch/main/scripts/on-device-install/install.sh | sh
```

Space check first (binary ~12 MB + one backup; `/mnt/nv` usually has 20–40 MB):

```
rw && df -h /mnt/nv
```

### Step 3 — Verify AfterTouch UI

On-device health check:

```
wget -qO- http://localhost:8000/health      # expect "version":"v0.107.0"
```

The repo's own walkthrough reaches the Admin UI via an SSH tunnel rather than
assuming the port is exposed externally — plan for that on this older non-BT
ST 10:

```
ssh -oHostKeyAlgorithms=+ssh-rsa -L 8000:localhost:8000 root@192.168.4.30
# then browse http://localhost:8000
```

Full runbook: `docs/content/docs/guides/ON-DEVICE-INSTALL-WALKTHROUGH.md` in
the cloned repo.

## Status / Log

- **2026-05-10** — Project started.
  - Located device on the LAN (port-8090 sweep of 192.168.4.0/22).
  - Pulled `/info` from the SoundTouch Web API; recorded device details above.
  - Confirmed port 22 is closed; SSH-enable step is required next.
  - This README created.
- **2026-05-10** — Tried Method B (TAP `remote_services on`).
  - Connected to TAP shell on TCP/17000; banner is `->`.
  - `help` → `Command not found` (consistent with stripped 27.x build).
  - Sent exactly one command, `remote_services on`, in a single session.
  - Response: `Command not found`. Port 22 still refuses connections.
  - **Conclusion:** TAP-based SSH-enable is **not available on this firmware**.
    Matches the repo's `docs/analysis/TELNET-COMMAND-REFERENCE.md`, which
    notes `remote_services on` was removed from the command set in
    FW 7.x and later (we have FW 27.0.6).
  - No persistent state was changed on the device.
- **2026-06-17** — Resumed; re-verified state after a host reboot.
  - The reboot wiped `/tmp`; re-cloned the reference repo to
    `/tmp/Bose-SoundTouch/`.
  - Transient scare: this WSL host had come up on the wrong interface
    (`eth1`, 192.168.1.0/24) with no route to the speaker. After
    reconnecting WiFi, `eth0` returned to `192.168.4.48/22` and the device
    was reachable again at the same IP.
  - Re-confirmed `192.168.4.30` `/info` (deviceID B0D5CC1918A7, FW 27.0.6).
  - Port re-check: **22 closed**, 8090 open, 17000 open — unchanged from
    2026-05-10. No device state altered. Next decision: Path A vs Path B.

## Revised plan options

Given that the TAP path to SSH is dead, two viable paths remain:

### Path A — USB-stick SSH unlock, then on-device installer

Original plan, unchanged from above. Risk: community reports show the USB
unlock has occasionally failed on later firmware revisions (e.g. Wave IV in
`docs/analysis/TELNET-MIGRATION-METHOD.md`). Worth trying, but not
guaranteed.

### Path B — Skip SSH; redirect cloud URLs via TAP `sys configuration` / `envswitch`

The gesellix repo's `SURVIVAL-GUIDE` and `TELNET-MIGRATION-METHOD` confirm a
**non-SSH** migration path that is **verified working on ST 10 FW 27.x**:

- Run AfterTouch (`soundtouch-service`) on a separate Linux box on the LAN.
- Use TAP (port 17000) to send `sys configuration …` / `envswitch …`
  commands that retarget the speaker's Bose cloud URLs to AfterTouch's
  local server.
- `sys reboot` over TAP to apply.

No SSH. No on-device install. The trade-off vs. on-device install is that
the AfterTouch server must always be running somewhere on the LAN.

## Open questions / things to investigate

- Exact layout of the `remote_services` USB stick (file/folder names,
  filesystem format) for this firmware revision.
- **(Answered 2026-06-17)** Port 8000 exposure — the repo's own walkthrough
  reaches the Admin UI over an SSH tunnel, so we plan for port-forwarding
  rather than relying on external exposure. See Step 3.
- **(Answered 2026-06-17)** Free space — the installer targets `/mnt/nv`
  (~20–40 MB free), not rootfs (~4 MB). Binary is ~12 MB + one backup, and
  the installer auto-prunes stale artefacts. Fits comfortably. See Step 2.
