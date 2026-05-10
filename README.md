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

1. Format a USB stick as **FAT32** (or FAT). Some sticks need their MBR
   "bootable" flag set — see [SoundCork issue #172](https://github.com/deborahgu/soundcork/issues/172).
2. Create a single empty file named `remote_services` (no extension) in the
   root of the stick. From Linux:
   ```
   touch /mnt/usb/remote_services
   ```
3. Insert the stick into the speaker's USB port.
4. Power-cycle the speaker (unplug, plug back in).
   - On some SoundTouch 10 units, you may need to hold **`4`** + **`Volume −`**
     on the speaker while powering on to force the USB check.
5. After boot, connect:
   ```
   ssh -oHostKeyAlgorithms=+ssh-rsa root@192.168.4.30
   ```
   No password.
6. (Optional, recommended) Make SSH persistent so the USB stick is no longer
   needed:
   ```
   touch /mnt/nv/remote_services
   ```

**Method B: TAP / telnet on port 17000 (legacy, no USB needed)**

Older firmware exposes a debug "TAP" interface on telnet port 17000. Sending
`remote_services on` enables sshd. Port 17000 is open on our device, so this
is worth trying first — *if* this firmware still honors the command (later
firmware revisions removed it). No reboot required on success.

### Step 2 — Run the on-device installer

```
rw && curl -sSL https://raw.githubusercontent.com/gesellix/Bose-SoundTouch/main/scripts/on-device-install/install.sh | sh
```

### Step 3 — Verify AfterTouch UI

Browse to `http://192.168.4.30:8000`. If the speaker doesn't expose the port
externally (older non-BT SoundTouch 10 units may not), use SSH port-forwarding:

```
ssh -L 8000:localhost:8000 root@192.168.4.30
```

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
- Whether the on-device AfterTouch port (8000) will be exposed externally on
  this older (non-BT) SoundTouch 10, or whether we'll need SSH port-forwarding
  permanently.
- Free space on the device — README warns the binaries barely fit.
