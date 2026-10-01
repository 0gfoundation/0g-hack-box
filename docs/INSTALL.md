# Installing a hack box

From a boxed mini PC to a box that shows the welcome screen and answers `hackbox status`
over Tailscale. About 20 minutes of machine time per box, most of it unattended.

Target hardware: Ryzen 7 5825U, 16 GB, 512 GB NVMe, fanless. Target OS: Linux Mint 22.3
Cinnamon, UEFI. Every time below was measured in the VM lab (QEMU, 4 vCPUs, 8 GB, Mint 22.3),
not on the real boxes yet.

Before you start you need:

- a Linux Mint 22.3 Cinnamon ISO (sha256
  `a081ab202cfda17f6924128dbd2de8b63518ac0531bcfe3f1a1b88097c459bd4`), or the zero-touch
  installer ISO built from it (route A below);
- a USB stick of 4 GB or more;
- a keyboard, mouse and screen for the first 15 minutes;
- wired or Wi-Fi internet without a captive portal;
- a Tailscale login that may add machines to the organisers' tailnet;
- your ssh public key in `keys/*.pub` in this repo (`40-ssh.sh` installs every key there).

## 1. BIOS / UEFI checklist

None of this can be scripted. The menu names differ per vendor; look for the setting, not
the exact wording.

Before the install:

- [ ] Boot mode UEFI only (CSM / Legacy off). The automated installer entries exist only in
      the UEFI boot menu.
- [ ] Secure Boot: leave it on if the firmware ships the Microsoft UEFI CA (Mint's shim is
      signed). The installer stick was not tested with Secure Boot on; if it will not boot,
      switch Secure Boot off for the install.
- [ ] USB boot allowed and first, for the install only.
- [ ] Restore on AC power loss: **Power On**. A boot always builds a fresh attendee home, so
      a power cut is safe.
- [ ] Wake-on-LAN on, if you want to power boxes on remotely.

After the install (step 4):

- [ ] Boot order: internal NVMe first. USB, network (PXE) and optical boot disabled.
- [ ] Boot menu hotkey disabled, if the setup offers that.
- [ ] Supervisor (setup) password set. Same on all boxes, kept by the organisers.
- [ ] Cable lock or tie-down, tamper sticker over the case screws.
- [ ] Label with the hostname and the help desk contact.

Physical attacks (opening the case, pulling the disk, resetting the BIOS) are out of scope
for the software. This checklist is the whole defence against them.

## 2. Install Mint

Pick one route. Both leave an admin account that runs the bootstrap. Route A names it
`hackadmin`; use the same name on route B so the docs and loops match.

### Route A: zero-touch USB stick (recommended)

The builder lives in the lab directory next to this repo (`vm/usb/make-usb-iso.sh`, with
`vm/build-initrd.sh` and `vm/seed/`), not in this repo yet. It remasters the stock ISO:
same Mint, plus a preseed and new boot entries.

Build it on any Linux machine with `xorriso`, `cpio`, `gzip` and `python3`:

    bash vm/usb/make-usb-iso.sh linuxmint-22.3-cinnamon-64bit.iso hackbox-mint223-auto.iso

It prints the output path and writes `hackbox-mint223-auto.iso.sha256` next to it (about
3.0 GB, a few seconds).

Write it to a stick. Check the device name twice, `dd` overwrites whatever it is given:

    lsblk                                  # find the stick, for example /dev/sdX
    sudo dd if=hackbox-mint223-auto.iso of=/dev/sdX bs=4M status=progress conv=fsync

balenaEtcher works too (choose the ISO, choose the stick, Flash).

Install:

1. Plug the stick into the box and boot from it (boot menu key, or USB first in the BIOS).
2. The GRUB menu shows, at the top:

   | Entry | Hostname |
   |---|---|
   | `Automated install (ERASES DISK)` | `hackbox-` plus the last 4 hex digits of the first network card's MAC |
   | `Automated install as hackbox1 (ERASES DISK)` | `hackbox1` |
   | `Automated install as hackbox2 (ERASES DISK)` | `hackbox2` |
   | `Automated install as hackbox3 (ERASES DISK)` | `hackbox3` |

   The first entry starts by itself after 10 seconds. Use the arrow keys to pick the
   `as hackboxN` entry for this box and press Enter.
3. **The whole internal disk is erased.** The installer picks the first non-removable NVMe,
   then SATA, disk of at least 16 GB that is not the stick. To force a disk, press `e` on the
   entry and add `hb_disk=/dev/nvme0n1` to the `linux` line.
4. No questions are asked. The install took 310 to 321 s in the lab. At the end the box
   **powers off** (so a stick left in cannot loop back into the installer).
5. Pull the stick and power on. The box boots to the Mint login screen.

What you get: user `hackadmin`, password `hackadmin`, timezone Asia/Singapore, locale
en_US.UTF-8, US keyboard, no ssh server yet (`40-ssh.sh` adds it). Change the timezone if
the event is elsewhere: `sudo timedatectl set-timezone <Area/City>`.

### Route C: headless stick (no screen, keyboard or mouse)

The same builder with `--headless --config <file>` makes a stick that installs, joins the
network (cable, or the Wi-Fi from the config), reboots by itself into the installed system,
runs every `setup/NN-*.sh` on the first boot from a copy of this repo carried on the stick
(`hackbox-firstboot.service`, progress in `/var/log/hackbox-firstboot.log`), and ends on the
welcome screen. You follow it over ssh with `vm/usb/watch-install.sh <host>`. The steps,
timings (about 12.5 minutes in the lab), the boot-order caveats for a new box and the
troubleshooting list are in `vm/usb/README.md` next to this repo. Tailscale joins by itself
only when the config has an auth key (`TS_AUTHKEY`, passed to `05-tailscale.sh`).

### Route B: stock Mint ISO by hand

Write the stock `linuxmint-22.3-cinnamon-64bit.iso` to a stick the same way, boot it, and
run "Install Linux Mint" from the live desktop:

- Language and keyboard: whatever the organisers type on.
- Multimedia codecs: not needed.
- Installation type: **Erase disk and install Linux Mint**. No LVM, **no disk encryption**
  (the box must boot unattended after a power cut).
- Your name: Hack Admin. Computer name: `hackbox1`, `hackbox2` or `hackbox3`.
  Username: `hackadmin`. A strong password. Do not tick "Log in automatically".
- Timezone: the venue's.

Reboot, remove the stick.

## 3. Bootstrap

Log in at the box as `hackadmin` and open a terminal. Bootstrap runs as this normal user,
not root. Until `40-ssh.sh` runs, `sudo` asks for the admin password once, so stay at the
keyboard for the first minutes.

### Until the pull request is merged

`bootstrap.sh` clones `main`, which does not have these changes yet. Clone the
`session-lockdown` branch into `/opt/hack-box` yourself, then run the same bootstrap. It
finds the checkout, pulls that branch with `--ff-only` and runs every `setup/NN-*.sh`:

    sudo apt-get update && sudo apt-get install -y git curl
    sudo mkdir -p /opt/hack-box && sudo chown "$USER:$USER" /opt/hack-box
    git clone -b session-lockdown <repo URL the branch was pushed to> /opt/hack-box
    TS_TAG=tag:hackbox bash /opt/hack-box/bootstrap.sh

From a copied tree instead (for example a tarball on a USB stick, no git): unpack it to
`/opt/hack-box` owned by `hackadmin`, then run the loop bootstrap would run (bootstrap itself
would try to `git clone` into the non-empty directory and stop):

    for s in /opt/hack-box/setup/[0-9][0-9]-*.sh; do echo "==> $(basename "$s")"; bash "$s"; done

Keep the tree at `/opt/hack-box`: `/usr/local/bin/hackbox` is a symlink into it.

### After the merge

    curl -fsSL https://raw.githubusercontent.com/0gfoundation/0g-hack-box/main/bootstrap.sh | TS_TAG=tag:hackbox bash

### Wi-Fi

`03-wifi.sh` runs first. Without `/etc/hackbox/wifi.conf` it does nothing (wired boxes). To
put a box on Wi-Fi, run `hackbox wifi set` after bootstrap (prompts for the network name, an
optional username for WPA2-Enterprise, and the password without echo), or create the conf
before bootstrap; `hackbox wifi status` shows the connection, `hackbox wifi forget` removes it.
The profile is system-wide and survives the attendee wipe; the attendee can neither read the
password nor change the connection. A cable, when plugged in, keeps the default route.

### Tailscale login and `TS_TAG`

`05-tailscale.sh` runs first and prints a login URL. Open it (on the box's browser or your
phone) and approve the machine; bootstrap carries on once the box has joined.

`TS_TAG=tag:hackbox` joins the box as a tagged device with `--accept-routes=false`. Before
using it, merge `files/lockdown/tailscale-acl.hujson` into the tailnet policy (admin console,
Access controls) and define `group:organisers` there: staff may reach the tag on tcp:22 and
the tag may reach nothing. The person approving the login must be allowed to own the tag.
Leave `TS_TAG` unset to join untagged, as before.

After bootstrap, make the tag permanent so reruns and `hackbox lock status` know about it:

    echo 'TS_TAG=tag:hackbox' | sudo tee -a /etc/hackbox/conf.d/39-lockdown-local.conf

A box that already joined untagged is not re-tagged automatically (it would re-authenticate
and can drop your ssh session). `05-tailscale.sh` prints the command; `TS_RETAG=1` runs it.

### What each step does and how long it took

| Step | Does | Lab time |
|---|---|---|
| `03-wifi.sh` | Wi-Fi profile from `/etc/hackbox/wifi.conf` (nothing without it) | 0 s |
| `05-tailscale.sh` | installs Tailscale, joins the tailnet | not measured (waits for your login) |
| `10-debloat.sh` | removes unused apps | 11 to 12 s |
| `15-upgrade.sh` | `apt full-upgrade` | 207 to 271 s |
| `20-services.sh` | disables services and tray nags | 1 to 2 s |
| `30-tools.sh` | chromium, Node 22, git, gh, Claude Code, OpenCode, system-wide | 82 to 113 s |
| `40-ssh.sh` | sshd key-only, passwordless sudo for the admin, `DenyUsers hacker` | 0 to 1 s |
| `50-desktop.sh` | dark theme, no lock or sleep, for the admin | 0 to 1 s |
| `60-hacker.sh` | attendee account `hacker`, autologin, skeleton | 0 s |
| `65-ai.sh` | agent configs, secrets directory, launcher | 0 s |
| `70-session.sh` | disposable home, timer, reset, overlay, `hackbox` CLI | 1 s |
| `80-lockdown.sh` | firewall, resource caps, desktop locks, browser policy | 1 s |
| `90-verify.sh` | non-destructive self-test and a banner | 11 to 18 s |
| **Total** | without the Tailscale login | **339 to 422 s** |

Most of the variation is the Ubuntu mirrors during `15-upgrade.sh` and `30-tools.sh`.

### The verify banner

The last thing bootstrap prints is one of:

    #  hack-box verification: ALL NON-DESTRUCTIVE CHECKS PASSED
    #  # summary: 66 passed, 0 failed, 12 skipped

    #  hack-box verification: 1 CHECK(S) FAILED
    #  The install is NOT aborted. Review the FAIL lines above
    #  and re-run:  bash /opt/hack-box/tests/run.sh <group>

A failed check never aborts the install. The lab result before the first reboot was 66 pass,
0 fail, 12 skip. The skips are expected at that point: the session checks that would restart
the display (3), no archive exists yet (3), no attendee desktop before the reboot (3), the
opt-in live model call (1), and two private ranges the lab network cannot route (2; on a real
LAN these may run instead).

## 4. Reboot and confirm

1. Route A only: change the admin password now, the installer default is public:
   `passwd`.
2. `sudo systemctl reboot`. Autologin as the attendee and the tmpfs caps take effect at
   this boot. The welcome screen appears by itself (9.7 s from the reboot command in the
   lab).
3. Do the "after the install" part of the BIOS checklist.
4. From a staff laptop on the tailnet (the box drops new inbound connections from the LAN;
   there are no text consoles to fall back on):

       ssh hackadmin@hackbox1 hackbox status
       ssh hackadmin@hackbox1 hackbox lock status

   `status` should say `state       idle` and show a mounted home. `lock status` prints one
   line per control and exits 0 only if every line says `OK`. Sample output of both is in
   `EVENT-RUNBOOK.md`.
5. Put the keys on and pick the agents: `SECRETS.md`. Then `hackbox agents show` should say
   `ready` for every offered agent.
6. Optional, on an idle box before the event:

       ssh hackadmin@hackbox1 'bash /opt/hack-box/tests/run.sh'              # starts and resets a session
       ssh hackadmin@hackbox1 'bash /opt/hack-box/tests/run.sh --live agents' # one real prompt per agent

   `--destructive` also exists; it fills the 40 GB home image and stresses the box, so run it
   on one box the day before at most.

If you installed over ssh from the LAN instead of at the keyboard, `80-lockdown.sh` kept
tcp/22 open inbound so it would not lock you out, and said so. `hackbox lock status` then shows
`open: tcp dport 22` on the `nft-inbound` line. Rerun `bash /opt/hack-box/setup/80-lockdown.sh`
over Tailscale to close it.

## 5. Rerunning setup

Every script is idempotent and safe on a box that is mid-session. Rerunning the whole
bootstrap also runs `apt full-upgrade`, so do not do that during the event. To reapply one
area, run its script, for example `bash /opt/hack-box/setup/80-lockdown.sh`.

Per-box settings go in extra files that the repo never overwrites:

| File | For |
|---|---|
| `/etc/hackbox/conf.d/90-local.conf` | session: `SESSION_MINUTES`, `WARN_MINUTES`, `HOME_IMG_SIZE`, `ARCHIVE_HOURS` |
| `/etc/hackbox/conf.d/29-ai-local.conf` | agents: `CLAUDE_AUTH`, models, `OG_ROUTER_URL`; then `hackbox agents set ...` |
| `/etc/hackbox/conf.d/39-lockdown-local.conf` | lockdown: `TS_TAG`, `NFT_ALLOW4`, `USB_STORAGE`; then rerun `80-lockdown.sh` |

## 6. Replace a dead box on the day

About 15 minutes of machine time, from the lab numbers.

1. Keep a written installer stick at the desk, plus a spare box that already has the BIOS
   settings from step 1.
2. In the Tailscale admin console, remove the dead machine, so the new one can take the same
   name (`hackbox2`, say).
3. Boot the stick, pick `Automated install as hackbox2 (ERASES DISK)`, wait for the power
   off (about 5 minutes), pull the stick, power on.
4. Log in as `hackadmin`, bootstrap as in step 3 with `TS_TAG`, approve the Tailscale login.
5. `passwd`, `sudo systemctl reboot`, then from a laptop `hackbox status` and
   `hackbox lock status`.
6. Keys: if the dead box is in your hands, reuse its keys (`SECRETS.md`). If it left the
   building, revoke its keys and create new ones.
7. Agents: a fresh box offers the default (`claude opencode`). Set what the other boxes run:
   `hackbox agents set ...`, then `hackbox reset` so the welcome screen shows it. Copy any
   `29-ai-local.conf` the other boxes have.
8. Finish the BIOS lock-down (USB boot off, supervisor password) when the queue allows.

If a box is alive but unreachable (Tailscale down) there is no local console to log in on:
power-cycle it first. A boot builds a fresh home and restarts every service.
