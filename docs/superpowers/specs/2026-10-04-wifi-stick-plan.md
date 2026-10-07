# Wi-Fi stick: change a box's Wi-Fi by plugging in a USB stick (plan, not built yet)

Status: planned 2026-10-04, to build after the event setup. Open question for the owner:
replace the network (B) or add it and keep the iPhone hotspot as a backup (A, recommended).

## Why

At an event the venue Wi-Fi changes (name, password, a better network) and a kiosk has no
admin console: the attendee desktop may not change networks (polkit), there are no text
consoles, and ssh needs the box to be online already. A cable always works; this is for
when there is no cable.

## How it works

1. Staff, on the build machine:
   `usb/make-wifi-stick.sh "Venue WiFi" 'password' [--username u] [--hidden] [--country SG]`
   writes one file, `hackbox-wifi.conf`, to the top folder of any FAT or exFAT stick:
   `KEY='value'` lines with `WIFI_SSID`, `WIFI_PASSWORD`, optional enterprise keys, `ISSUED_AT`
   and `SIG`, an HMAC-SHA256 over the other lines with `USB_WIFI_KEY` from `.env`.
2. Each box has the same key, root only: `/etc/hackbox/usb-wifi.key` (0600). The fleet stick
   carries it (from `.env`); running boxes get it over ssh.
3. A udev rule on any USB block device with a filesystem starts
   `hackbox-usb-wifi@<dev>.service` (root, oneshot, 30 s timeout):
   - mounts the partition read-only, `nosuid,nodev,noexec`, at `/run/hackbox/usbcfg`, or reads
     from the existing mount when the attendee's desktop already mounted it;
   - reads only `/hackbox-wifi.conf` (regular file, at most 4 KB, no symlink), parses the
     lines strictly (never sources them), checks `SIG` in constant time and that `ISSUED_AT`
     is not older than 30 days; anything else is ignored and logged;
   - writes `/etc/hackbox/wifi.conf` and runs `setup/03-wifi.sh` (the same validation and
     NetworkManager profile as the installer); with option A, as an additional profile
     with a higher priority than the hotspot's;
   - unmounts what it mounted; never writes to the stick.
4. Feedback: a log line in the journal (`hackbox-usb-wifi`), a beep pattern if the box has a
   speaker, and the hub agent reports "Wi-Fi changed to <name>" once the box is online. The
   attendee session is not touched; the attendee's own files on the same stick still mount.

## Option A (recommended): remember more than one network

`03-wifi.sh` gets numbered profiles (`hackbox-wifi`, `hackbox-wifi-2`, ...) with
`connection.autoconnect-priority`; the newest stick wins, the hotspot stays as a fallback.
`hackbox wifi status` lists them; `hackbox wifi forget <name>` removes one.

## Security

- Attendees can plug in sticks, so the signature is required; without the key a file is
  ignored. The key never leaves `.env` and the boxes (root 0600), and is not on the Wi-Fi
  stick, only the signature. Treat the Wi-Fi stick like the Wi-Fi password.
- A stolen Wi-Fi stick can only re-point boxes for up to 30 days (`ISSUED_AT`); rotate
  `USB_WIFI_KEY` after the event.

## Tests

- Unit: signature accepted and rejected (changed byte, missing SIG, old ISSUED_AT), strict
  parsing (quotes, `'\''`, no shell expansion), file size and symlink refused.
- Lab container: a loop-mounted FAT image with a good and a bad file, the service run by hand.
- hackbox1: a real stick with a second network, then back to the hotspot.

## Files to add or change

`usb/make-wifi-stick.sh`, `files/usbwifi/hackbox-usb-wifi` (script), `files/usbwifi/99-hackbox-usb-wifi.rules`,
`files/usbwifi/hackbox-usb-wifi@.service`, `setup/03-wifi.sh` (option A), `setup/70-session.sh`
(install), `usb/headless/late.sh` and `usb/make-usb-iso.sh` (carry the key), `.env.example`
(`USB_WIFI_KEY`), `docs/FLEET.md` (how to use it), `tests/`.
