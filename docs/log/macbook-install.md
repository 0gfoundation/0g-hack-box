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

`usb/headless.conf`, mode 600: `HB_HOSTNAME='hackbox1'`, `ADMIN_USER='hackadmin'`, generated
24 character admin password, Wi-Fi on the iPhone hotspot with `WIFI_COUNTRY='SG'`, the Mac's
`~/.ssh/id_ed25519.pub` in `SSH_KEY_FILES`, no `TS_AUTHKEY` (so `LAN_SSH=auto` keeps tcp/22
open on the LAN).

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
