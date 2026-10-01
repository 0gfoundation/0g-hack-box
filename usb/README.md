# hack-box USB installer

Two sticks can be built from the stock Linux Mint 22.3 Cinnamon ISO with `make-usb-iso.sh`:

| Stick | Build | What it does |
|---|---|---|
| zero-touch (default) | `make-usb-iso.sh` | installs Mint with no questions and **powers off**; you then need a screen and keyboard to bootstrap (docs/INSTALL.md, route A) |
| **headless** | `make-usb-iso.sh --headless --config <file>` (from jarvis: `vm/lab usb-headless <file>`) | for a box with **no screen, keyboard or mouse**: installs, joins the network (cable or Wi-Fi), becomes reachable over ssh, reboots by itself, provisions the whole box from the repo copy on the stick, and ends on the attendee welcome screen. You follow it over ssh. |

The rest of this file is about the headless stick.

## What happens, stage by stage (measured in the VM lab)

Times are from pressing the power button, measured in the lab VM (4 vCPUs, 8 GB, NVMe, wired
network through QEMU, 2026-09-30). The real box (8 cores, faster disk) should be similar or a
little faster; the two download-heavy steps depend on the internet link.

| Stage | Lab time (4 runs) | From the outside |
|---|---|---|
| Firmware, stick's GRUB menu, 10 s countdown | 0 to about 20 s | stick light flickers |
| Live installer up, network up | 34 to 35 s | `ping hackbox1.local` answers; REPORT_URL gets `installer-network`; 1 short beep |
| ssh into the installer (watch the install) | 50 s | `ssh mint@hackbox1.local` works (only when the network is up during the install) |
| Install to the internal disk | 326 to 336 s (282 s with no network) | stick light busy, then quiet; REPORT_URL `installed`; 1 long beep |
| Reboot into the installed system (stick stays in) | ssh at 327 to 349 s | `ssh hackadmin@hackbox1.local` works; REPORT_URL `ssh-ready`; 2 short beeps |
| First boot provisioning (apt upgrade, tools, lockdown, self-test) | done at 715 to 818 s | one progress line per step; REPORT_URL `done-<script>` per step |
| Reboot to the attendee welcome screen | 750 to 853 s (12.5 to 14.2 min) | 3 short beeps before the reboot; `ssh hackadmin@<ip> hackbox status` says `idle` |

Most of the spread is `15-upgrade.sh` (169 to 257 s) and `30-tools.sh` (179 to 204 s), which
download from the Ubuntu and Mint mirrors. A box that was already installed and is booted from
the stick again reaches ssh in 25 s (the stick's menu hands over to the disk).

Beeps only exist if the board has a PC speaker or buzzer; many fanless mini PCs have none, then
the box is silent (there is no LED to drive on a generic mini PC). A failure plays 5 fast beeps
twice. While the first boot waits for a network it beeps once every 5 minutes.

## Tonight, step by step

You need: the box, its power supply, a USB stick of 4 GB or more (it will be erased), a network
cable to your router if you can (Wi-Fi works too, the cable is simpler), your Mac on the same
network, and ssh access from your Mac with a key the stick authorises.

1. **Fill in the config on jarvis.**

       cd /Users/jarvis/0g/hack-box
       cp vm/usb/headless.conf.example vm/usb/headless.conf
       chmod 600 vm/usb/headless.conf
       nano vm/usb/headless.conf

   Set at least `HB_HOSTNAME` (for example `hackbox1`), `ADMIN_PASSWORD`, and, for Wi-Fi,
   `WIFI_SSID` and `WIFI_PASSWORD` (plus `WIFI_USERNAME` for a username and password network).
   `TS_AUTHKEY` and `TS_TAG` if you want the box on Tailscale by itself. The ssh keys default to
   `keys/*.pub` in the repo plus jarvis's own key; add your Mac's public key file to
   `SSH_KEY_FILES` (or to `keys/`) if you will watch from the Mac directly.
   `vm/usb/headless.conf` is ignored by git; never commit it.

2. **Build the ISO** (on forge, driven from jarvis; about 1 minute plus a 3 GB copy):

       vm/lab sync
       vm/lab usb-headless vm/usb/headless.conf

   It prints a one-line summary (admin, hostname, number of keys, Wi-Fi name, Tailscale yes or
   no), builds `hackbox-mint223-headless.iso` (mode 600) and copies it to
   `/Users/jarvis/0g/hack-box/out/`. The copy of your config on forge is deleted after the build.
   A bad Wi-Fi setting (passphrase shorter than 8 characters, and so on) fails here, not on the box.

3. **Copy the ISO to your Mac** (from the Mac's Terminal):

       scp -i ~/.ssh/id_ed25519_faucet jarvis@100.113.49.48:/Users/jarvis/0g/hack-box/out/hackbox-mint223-headless.iso* ~/
       cd ~ && shasum -a 256 hackbox-mint223-headless.iso && cat hackbox-mint223-headless.iso.sha256

   The two sums must match.

4. **Write the stick on the Mac.** Plug the stick in, then find it. Be sure: `dd` erases
   whatever disk you name, including your Mac's own disk.

       diskutil list external physical

   Only external disks are listed. Pick the one whose size matches the stick (say `/dev/disk4`)
   and check it once more:

       diskutil info /dev/disk4 | egrep 'Media Name|Disk Size|Protocol|Removable|Device Location|Internal'

   You want `Protocol: USB`, `Device Location: External`, `Internal: No`, and the size of the
   stick. Then:

       diskutil unmountDisk /dev/disk4
       sudo dd if=$HOME/hackbox-mint223-headless.iso of=/dev/rdisk4 bs=4m
       sync
       diskutil eject /dev/disk4

   `rdisk` (raw) is several times faster than `disk`. `dd` prints nothing until it finishes
   (a few minutes for 3 GB); press Ctrl+T to see progress. If macOS says "The disk you inserted
   was not readable by this computer" afterwards, click Eject: that is normal for a Linux stick.

5. **Boot the box.** Plug in the network cable (if any) and the stick, then power. Press the
   power button. Walk away. Nothing needs a key press.

6. **Watch from the Mac** (or from jarvis):

       /path/to/vm/usb/watch-install.sh hackbox1.local

   (copy the script to the Mac first: `scp -i ~/.ssh/id_ed25519_faucet jarvis@100.113.49.48:/Users/jarvis/0g/hack-box/vm/usb/watch-install.sh ~/`)
   It waits until the box answers ssh, prints the installer's progress lines while it
   installs (as `mint`), reconnects after the reboot (as `hackadmin`), prints each provisioning
   step as it starts and ends, and stops with `VERDICT: SUCCESS` or `VERDICT: FAILED` plus the
   last lines of the detail log. Use the IP address instead of `hackbox1.local` if mDNS does not
   resolve on your network (find it in the router's client list, or from REPORT_URL).

7. **After SUCCESS**, the box reboots to the welcome screen. Then:

       ssh hackadmin@<ip> hackbox status
       ssh hackadmin@<ip> hackbox lock status
       ssh hackadmin@<ip> 'bash /opt/hack-box/tests/run.sh'

   Put the agent keys on (docs/SECRETS.md). mDNS (`hackbox1.local`) stops answering after this
   final reboot on purpose (the repo turns avahi off for the event), so use the IP or the
   Tailscale name from now on. You can leave the stick in: the box boots its own disk. Take it
   out when convenient and keep it safe: **the stick holds the Wi-Fi password, the Tailscale key
   and the admin password hash.** Re-flash or wipe it after the event.

## What the box looks like afterwards

- Hostname from the config, admin account with your password and your keys, passwordless sudo
  for the admin, sshd key-only (`40-ssh.sh`), `hackbox` CLI, attendee account `hacker` with
  autologin to the welcome screen, everything `bootstrap.sh` would have done.
- Tailscale: joined with `TS_AUTHKEY` (and `TS_TAG`) when given; otherwise skipped (it would wait
  for someone to open a login link). Join later over ssh:
  `bash /opt/hack-box/setup/05-tailscale.sh`.
- ssh from the LAN: with `LAN_SSH=auto`, when the box did not join Tailscale, the first boot
  writes `NFT_INBOUND_TCP="22"  # hackbox-firstboot` to `/etc/hackbox/conf.d/39-lockdown-local.conf`
  so the firewall keeps tcp/22 open (otherwise you would be locked out at step 80). `hackbox lock
  status` shows it on the `nft-inbound` line. After you have joined Tailscale, delete that line and
  rerun `setup/80-lockdown.sh` over Tailscale to close it.
- Wi-Fi: one system profile `hackbox-wifi` (see `setup/03-wifi.sh`, `hackbox wifi status`).
  A cable always wins when both are up.
- Logs: `/var/log/hackbox-firstboot.log` (progress), `/var/log/hackbox-firstboot.detail.log`
  (everything), `/var/log/installer/hackbox-install.log` and `hackbox-live.journal` (installer),
  `/var/log/installer/hackbox-headless-late.log` (late step).
- The first-boot unit `hackbox-firstboot.service` disabled itself and left
  `/var/lib/hackbox-firstboot.done`. To run it again: `sudo rm /var/lib/hackbox-firstboot.done &&
  sudo systemctl start hackbox-firstboot`.

## How the stick avoids reinstalling

1. The late step writes `EFI/hackbox/installed` on the new disk's EFI partition and puts the
   new "Ubuntu" boot entry first in the UEFI boot order with `efibootmgr`. With a firmware that
   respects the boot order, the stick is simply not booted again.
2. If the firmware boots the stick anyway (USB first in the BIOS, or a boot menu choice), the
   default menu entry searches all disks for `EFI/hackbox/installed`. When found it prints
   "hack-box is already installed ... booting it, not reinstalling" and chainloads the disk's
   bootloader. Nothing is erased.

To reinstall a box on purpose: over ssh, `sudo rm /boot/efi/EFI/hackbox/installed`, then
reboot with the stick in and USB first (or use the second menu entry, "Force reinstall", which
needs a keyboard).

## Will a brand-new box boot the stick by itself?

Honest answer: probably, but it depends on what the box ships with, and we cannot see its
firmware settings without a screen.

What is typical for this class of machine (fanless Ryzen 5825U mini PCs from Topton, Bosgame,
CWWK and similar, AMI Aptio V firmware):

- **Empty disk (barebone, or a blank SSD):** the firmware finds nothing bootable on the internal
  disk and tries the next device; a UEFI USB stick is in the default list, so it boots the stick.
  This is the most likely case for a no-OS order. It is also what the lab VM does.
- **Windows preinstalled:** the "Windows Boot Manager" entry is Boot Option #1 and the firmware
  boots it; a stick plugged in is ignored. Windows then sits at its first-run setup screen.
  Some AMI builds put a USB device first when one is present, but most do not.
- **Boot menu key:** usually **F7** on these boards (Del or Esc for setup; some models use F11
  or F12). Pressing it blind is not practical: the menu lists the disk and the stick in an order
  you cannot see, and you would have to know how many times to press Down. Do not rely on it.

Signs you can see without a screen:

| What you see | Meaning |
|---|---|
| within 1 minute: `ping hackbox1.local` answers, or the router's client list shows `hackbox1` (or `mint` for a moment), or REPORT_URL gets `installer-network`; the stick's light is busy | the stick booted. Run `watch-install.sh`. |
| a `DESKTOP-XXXXXXX` or `WINDOWS-...` device in the router's client list (only with a cable; Windows setup does not join Wi-Fi by itself) | Windows booted from the internal disk; the stick was ignored. |
| nothing on the network after 3 minutes, even with a cable | either Windows setup without network, or the firmware is sitting in a menu or an error. |

If the stick did not boot, power off (hold the power button 5 s) and wait for a screen: any TV
or monitor on one of the two HDMI ports works (the box does not care what it is), plus any USB
keyboard. Press F7 at power on, pick the UEFI entry for the stick, and it continues unattended
from there. While in the BIOS (Del), set Boot Option #1 to the stick for the install; the late
step puts the internal disk first again by itself.

Not tested at all: Secure Boot on (the stick uses Mint's signed shim and GRUB, unchanged, so it
should boot; the guard's chainload of the installed shim under Secure Boot is untested), and any
real AMI firmware.

## Troubleshooting

- **The watcher waits forever.** Check the network first: is the box in the router's client list?
  With Wi-Fi only, check the SSID and password in the config (the watcher cannot help before the
  box has a network). Try the IP instead of `.local` (some networks block mDNS). Make sure the
  Mac's key is authorised (it is only if it is in `SSH_KEY_FILES` or `keys/`).
- **`ssh mint@...` never works but the box installs anyway.** The installer's ssh server comes
  from the internet; on a slow link it can take a few minutes, or fail. Nothing depends on it:
  wait for `ssh hackadmin@...` after the reboot (about 6 minutes).
- **VERDICT: FAILED.** The watcher prints the last lines of the detail log. Common causes: a
  mirror timeout during `15-upgrade.sh` or `30-tools.sh` (just rerun: `ssh hackadmin@<ip> sudo
  systemctl start hackbox-firstboot`), a Tailscale key that is expired or not allowed to use
  `TS_TAG` (fix the key in `/etc/hackbox/ts-authkey`, root only, then rerun). The unit stays
  enabled after a failure, so a reboot also retries.
- **The first boot says `WAITING for the network`.** No cable and the Wi-Fi did not connect.
  Plug in a cable: it continues by itself, no reboot needed. Then fix the Wi-Fi over ssh with
  `hackbox wifi set` and check `hackbox wifi status`.
- **Wi-Fi does not associate** (untested on real radios): `hackbox wifi status` shows the device
  and state; `journalctl -u NetworkManager -u wpa_supplicant` shows why. Enterprise networks with
  a private CA fail the certificate check by design; see `WIFI_EAP_NO_CA_CHECK` and its risk.
  Hidden networks need `WIFI_HIDDEN=1`. A WPA3-only network needs `WIFI_KEY_MGMT=sae` if the scan
  at install time did not see it.
- **The box reinstalls itself in a loop.** Should not happen (two independent guards, both proven
  in the lab). If it does, pull the stick; the disk boots on its own.
- **The box is on the network as Windows.** See the section above; you need a screen once.
- **`mint.local` answers instead of `hackbox1.local`, or a menu says "LEGACY BIOS boot".** The
  firmware booted the stick in legacy (CSM) mode: the boot menu entry was just the stick's name
  or "USB", without "UEFI:". The automated entries exist only in the UEFI menu, so nothing is
  installed. Power off, pick the "UEFI:" entry, or set the boot mode to UEFI (CSM off) in setup.
- **Wi-Fi from an iPhone hotspot never connects.** iOS names the hotspot after the phone and
  writes the apostrophe as `’` (U+2019), not `'`. Copy the SSID exactly, for example from the
  Mac's `ipconfig getsummary en0` while it is joined to the hotspot.
- **Nothing at all, no lights.** Power supply, or the box is set to stay off after power loss;
  press the power button.

## Files

| File | Where it runs | Does |
|---|---|---|
| `make-usb-iso.sh` | forge (xorriso) | builds the ISO; `--headless --config` for this stick |
| `headless.conf.example` | you | annotated config; copy to `headless.conf` (git ignored) |
| `../lab usb-headless` | jarvis | pushes repo, config and keys to forge, builds, deletes the config there, fetches the ISO |
| `headless/early-live.sh` | stick, initramfs | installs `hackbox-live.service` into the live session |
| `headless/live.sh` | stick, live session | hostname, Wi-Fi, network, ssh for `mint`, progress log, reports |
| `headless/late.sh` | stick, end of install | hostname, password hash, keys, repo to `/opt/hack-box`, Wi-Fi profile, sshd, marker, boot order, first-boot unit |
| `headless/hackbox-firstboot` + `.service` | installed box, first boot | network, ssh, provisioning, reboot |
| `headless/hb-common.sh`, `hostname.sh` | both | log lines, reports, beeps; hostname pattern |
| `watch-install.sh` | your Mac | follows the install, prints the verdict |
| `test-headless.sh` | forge | the lab proof (throwaway VM, stick attached throughout) |
| `test-usb-iso.sh` | forge | the lab proof of the default (power-off) stick |

## Building outside the jarvis lab (for example on a Mac)

The builder needs `xorriso`, `cpio`, `gzip`, `python3` and GNU `sed`, `stat` and
`sha256sum`. On macOS: `brew install xorriso cpio coreutils gnu-sed` and put the GNU tools
first on PATH (`export PATH="$(brew --prefix)/opt/coreutils/libexec/gnubin:$(brew --prefix)/opt/gnu-sed/libexec/gnubin:$PATH"`),
or run the build in an `ubuntu:24.04` container with those packages. Then:

    cp usb/headless.conf.example usb/headless.conf   # fill in, chmod 600
    usb/make-usb-iso.sh --headless --config usb/headless.conf --repo "$PWD" \
        /path/to/linuxmint-22.3-cinnamon-64bit.iso usb/out/hackbox-mint223-headless.iso

The stock ISO comes from any Linux Mint mirror (sha256 in the mirror's `sha256sum.txt`).
`watch-install.sh` follows the install over ssh from the same machine.

### A fleet from one `.env` (what the MacBook build uses)

All secrets for every box live in one file at the repo root, `.env` (git ignored; the
committed template is `.env.example`): admin password, Wi-Fi, the Tailscale key, the hub URL
and one hub token per box. Build one stick per box with `--host`:

    cp .env.example .env && chmod 600 .env          # fill in
    ssh-keygen -t ed25519 -N '' -f ~/.ssh/hackbox_ed25519   # once: a key scripts can use
    usb/make-usb-iso.sh --headless --config .env --host hackbox2 --repo "$PWD" \
        /path/to/linuxmint-22.3-cinnamon-64bit.iso ~/hackbox2.iso

With `HUB_URL` and `HUB_TOKEN_<host>` the box joins the session hub by itself on its first
boot (dashboard, download QR, agent keys from the dashboard). `.env` and `usb/headless.conf`
are never copied onto the stick. For `ssh hackbox2` without prompts, add to `~/.ssh/config`:

    Host hackbox1 hackbox2 hackbox3 hackbox4
        HostName %h.<tailnet>.ts.net
        User hackadmin
        IdentityFile ~/.ssh/hackbox_ed25519
        IdentitiesOnly yes
        UserKnownHostsFile ~/.ssh/known_hosts_hackbox
        StrictHostKeyChecking accept-new

What we learned on real hardware (docs/log/macbook-install.md): set the firmware boot mode
to Pure UEFI (or pick a "UEFI:" entry in the F7 menu; "UEFI: USB, Partition 2" works), keep
an iPhone on its Personal Hotspot screen for the whole install, and expect 1.5 to 2 GB of
downloads and about 30 minutes of provisioning on a phone hotspot.
