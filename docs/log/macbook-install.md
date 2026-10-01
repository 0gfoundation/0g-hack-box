# Headless stick built on a MacBook, box hackbox1 (Singapore, 2026-10-01)

Route C (headless stick) built on macOS instead of the jarvis/forge lab, for one fanless
Ryzen 7 5825U mini PC (16 GB, UEFI, Ubuntu preinstalled), on an iPhone hotspot.

## Build machine

- macOS 15 (Darwin 24.5), Apple Silicon, Homebrew.
- `brew install xorriso cpio coreutils gnu-sed`, with the coreutils and gnu-sed `gnubin`
  directories first on PATH for the build (the builder uses `stat -c`, `sed -i` and `sha256sum`).
- `openssl` on PATH is Homebrew OpenSSL 3. macOS's own `/usr/bin/openssl` (LibreSSL 3.3) has no
  `passwd -6`; with it first on PATH the build stops with "ADMIN_PASSWORD_HASH is not a crypt
  hash", so it fails safely, but put Homebrew's openssl first (or set ADMIN_PASSWORD_HASH).

## Source ISO

- `linuxmint-22.3-cinnamon-64bit.iso`, sha256
  `a081ab202cfda17f6924128dbd2de8b63518ac0531bcfe3f1a1b88097c459bd4` (matches the mirror's
  `sha256sum.txt`).
- Mirrors from Singapore, single connection: mirror.freedif.org about 2 MB/s
  (path `/LinuxMint/iso/stable/22.3/`), mirrors.tuna.tsinghua.edu.cn about 1.8 MB/s,
  pub.linuxmint.io 0.6 MB/s, mirrors.layeronline.com and mirrors.kernel.org under 20 KB/s.
  Downloaded with `aria2c` from the first three at once, with the sha256 checked by aria2.

## Fix: the config's plain passwords were copied onto the stick

`usb/README.md` (building outside the lab) says to create the config as `usb/headless.conf`
inside the repo. The builder copies the repo tree to `/hackbox/repo` on the stick with `tar`,
excluding only `.git` and `results`, so that config (plain `ADMIN_PASSWORD`, `WIFI_PASSWORD`,
`TS_AUTHKEY`) went onto the stick world readable and then to `/opt/hack-box` on the box. Any
ISO left in `usb/out/` would have been copied in as well. On jarvis the config lives in
`vm/usb/`, outside the tree, which is why the lab never showed it.

`make-usb-iso.sh` now also excludes `./usb/headless.conf`, `./usb/out`, `*.iso` and
`*.iso.sha256` (the git-ignored files in `usb/`).

## Config (not committed)

`usb/headless.conf`, mode 600: `HB_HOSTNAME='hackbox1'`, `ADMIN_USER='hackadmin'`, a simple
admin password for this test box (asked for by the owner; replace before the event), Wi-Fi on
the iPhone hotspot with `WIFI_COUNTRY='SG'`, the Mac's `~/.ssh/id_ed25519.pub` in
`SSH_KEY_FILES`, and (after the first attempt below) a Tailscale auth key.

## First boot attempt: plain Mint, nothing installed

The F7 menu offered the stick only as "USB" (no "UEFI:" entry). The box came up as
`mint.local` (172.20.10.10 on the hotspot), no ssh, and asked for the Wi-Fi password on a
desktop. The menu it showed (Start Linux Mint, compatibility mode, OEM install, ...) is the
stock isolinux menu: the firmware booted the stick in legacy BIOS (CSM) mode. The builder only
rewrites the UEFI GRUB menu, so legacy boot ran a stock live session (`hostname=mint`, stock
initrd, none of the headless scripts). Nothing was installed or erased.

Fix: `make-usb-iso.sh` now replaces `/isolinux/live.cfg` with a menu that says the stick was
booted in legacy mode and how to boot it in UEFI mode, with no timeout (a plain live session
is still there, not default). Tested in QEMU on the Mac (`qemu-system-x86_64`, TCG, stick as a
read-only USB disk): SeaBIOS shows the new menu; OVMF (UEFI) shows the hack-box GRUB menu,
starts "Headless install" after 10 s, and reaches the Mint kernel splash by itself (GRUB's
"Press any key to continue" after the installed-marker search times out after 10 s).

## Second finding: the hotspot SSID

iOS writes the apostrophe in "Udhaykumar’s iPhone" as U+2019. The config had an ASCII `'`, so
even a UEFI boot would not have joined the hotspot. The config now has the exact bytes
(`e2 80 99`). README troubleshooting lists both findings.

## Tailscale

A Tailscale auth key (not tagged, `TS_TAG` empty) is in the config, so `05-tailscale.sh` joins
the box as `hackbox1` on the first boot and `LAN_SSH=auto` closes tcp/22 on the LAN after the
lockdown; reach it over Tailscale. The attendee uid cannot reach the tailnet (the nft attendee
chain rejects `tailscale0` and 100.64.0.0/10). Before the event: tag the box (`tag:hackbox`,
`files/lockdown/tailscale-acl.hujson`) and set a strong admin password (this test box uses a
simple one).

## Third attempt: installed, then waited 55 minutes for the network

With Pure UEFI the F7 menu listed `UEFI: USB` and `UEFI: USB, Partition 2` (plus the legacy
`USB`). `UEFI: USB, Partition 2` booted the hack-box GRUB menu and the headless install ran
by itself. The installed system came up as `hackbox1`, but the first boot logged
`WAITING for the network` every 5 minutes from 15:24 for 55 minutes: the hotspot profile
(`hackbox-wifi`, exact SSID) was written (`END wifi rc=0`) but never connected. After a
restart with the iPhone on its Personal Hotspot screen, the same profile connected in 26 s
(`NETWORK up ips=172.20.10.10`), so the profile was right and the hotspot was not available
during the first boot (an iPhone switches it off when nothing is connected for a while).
Lesson: keep the phone on the hotspot screen for the whole install, or use a cable.

Tailscale joined at once (`05-tailscale.sh rc=0`, 68 s) as `hackbox1`, 100.64.41.33, direct
path. ssh from the Mac failed with "Permission denied (publickey)" although the server
accepted the key: the Mac's `id_ed25519` has a passphrase and was not in the agent.
`ssh-add --apple-use-keychain ~/.ssh/id_ed25519` fixed it.

`15-upgrade.sh` over the hotspot downloads at about 0.3 MB/s (910 MB after 7 minutes), far
slower than the lab; plan 1.5 to 2 GB of mobile data and 30 minutes or more for provisioning
on a phone hotspot. `watch-install.sh` loses ssh now and then while the link is saturated
and reconnects by itself.

## Result

Provisioning finished at 16:47 (`HACKBOX-FIRSTBOOT-DONE rc=0`): `15-upgrade.sh` 1109 s over
the hotspot, `30-tools.sh` 94 s, every step rc=0, self-test 66 passed, 0 failed, 12 skipped.
After the reboot:

- `hackbox status`: idle, 30 minute sessions, agents `claude opencode`, home 39.6 GB image.
- `hackbox lock status`: all 25 controls OK, exit 0 (`nft-inbound`: LAN ssh closed because the
  box joined Tailscale).
- `hackbox agents show`: claude and opencode offered, both waiting for keys
  (`anthropic-api-key`, `0g-router-key`; set with `hackbox secret set`).
- `tests/run.sh`: 72 passed, 0 failed, 10 skipped (destructive groups, no archive yet, two
  private ranges with no route on the hotspot, live agent calls).

Then the branch was synced to `/opt/hack-box` (rsync, without the local config), 
`setup/70-session.sh` rerun, and `hackbox hub set http://<jarvis>:8210 <token>`: the box shows on
the hub dashboard as online and idle, and the welcome screen shows the name field.
