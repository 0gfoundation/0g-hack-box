# Fleet install: one stick, the hub numbers the boxes

How the hack boxes for an event are built and run from one MacBook: one `.env` with every
secret, one USB stick for every box, a session hub on jarvis with a staff dashboard, and
download QR codes for attendees. Tested on 2026-10-01 with hackbox1 (fanless Ryzen 7 5825U,
16 GB, iPhone hotspot); the log of that install is `docs/log/macbook-install.md` and the
design is `docs/superpowers/specs/2026-10-01-session-hub-design.md`.

## What you end up with

- Every box installs itself from the same stick, asks the hub for the next free name
  (`hackbox2`, `hackbox3`, ...) and appears on the dashboard. A box that is reinstalled
  gets its old name back.
- Attendees type their name and email, pick an agent (Claude Code or OpenCode, both already
  pointed at the 0G agent skills), and work for 30 minutes.
- They can ask for more time; staff approve it on the dashboard.
- At the end they get a QR code and a 6 character code to download their project as a zip
  for 7 days. The time-up screen waits for their Done (or staff Finish).
- Staff see every box live (state, time left, attendee, which apps are open), extend, end,
  set the agent keys for all boxes or one, and browse the session history.

## Once: the build machine (macOS)

    brew install xorriso cpio coreutils gnu-sed go
    export PATH="$(brew --prefix)/opt/coreutils/libexec/gnubin:$(brew --prefix)/opt/gnu-sed/libexec/gnubin:$PATH"
    git clone -b session-lockdown https://github.com/0gfoundation/0g-hack-box ~/0g-hack-box

Download `linuxmint-22.3-cinnamon-64bit.iso` from a Linux Mint mirror and check it against
the mirror's `sha256sum.txt` (a081ab20...c459bd4). From Singapore, mirror.freedif.org
(`/LinuxMint/iso/stable/22.3/`) was the fastest.

A dedicated ssh key without a passphrase, so scripts (and you) can log in without an agent:

    ssh-keygen -t ed25519 -N '' -f ~/.ssh/hackbox_ed25519

and in `~/.ssh/config` (your tailnet's MagicDNS suffix from `tailscale status --json`):

    Host hackbox1 hackbox2 hackbox3 hackbox4
        HostName %h.<tailnet>.ts.net
        User hackadmin
        IdentityFile ~/.ssh/hackbox_ed25519
        IdentitiesOnly yes
        UserKnownHostsFile ~/.ssh/known_hosts_hackbox
        StrictHostKeyChecking accept-new

## Once: the hub on jarvis

    hub/deploy/deploy-to-jarvis.sh            # build, install, LaunchAgent com.udhay.hackbox-hub
    hub/deploy/deploy-to-jarvis.sh --tunnel   # public route for the download pages only

The first run creates `/Users/jarvis/hackbox-hub/config.json` (mode 600) with one token per
box and a fleet `enroll_token`, and prints them once. After `--tunnel`, reload the shared
tunnel yourself: `ssh -t jarvis "sudo kill -HUP \$(pgrep -f 'cloudflared tunnel.*run')"`.
Then `https://hackbox.udhaykumarbala.dev/healthz` answers `ok`; only `/d/...`, `/code` and
`/healthz` are public. The dashboard is `http://<jarvis tailnet IP>:8210/`, tailnet only.
Details: `hub/DEPLOYMENT.md`.

## Once: the tailnet

1. Paste `files/lockdown/tailscale-policy.hujson` at
   https://login.tailscale.com/admin/acls/file (replace the default policy) and Save. Boxes
   (`tag:hackbox`) may then reach only the hub's port on jarvis; Tailscale refuses to save
   the policy if its built-in tests fail.
2. Settings, Keys, Generate auth key: Reusable, Pre-approved, Tags `tag:hackbox`. Note when
   it expires; the stick stops joining the tailnet after that.

## Once: `.env`

    cp .env.example .env && chmod 600 .env

Fill in (every key is explained in `.env.example`):

| Key | What |
|---|---|
| `ADMIN_PASSWORD` | the `hackadmin` password (a passphrase; only its hash goes on the stick) |
| `WIFI_SSID`, `WIFI_PASSWORD`, `WIFI_COUNTRY` | the network the boxes use; copy the SSID byte for byte (an iPhone hotspot name has a curly `’`) |
| `SSH_KEY_FILES` | `~/.ssh/hackbox_ed25519.pub` plus your own key |
| `TS_AUTHKEY`, `TS_TAG` | the tagged key from above, `tag:hackbox` |
| `HUB_URL` | `http://<jarvis tailnet IP>:8210` |
| `HUB_ENROLL_TOKEN` | the hub's `enroll_token` |

`.env` is git ignored and never copied onto the stick.

## Build and write the stick

    usb/make-usb-iso.sh --headless --config .env --fleet --repo ~/0g-hack-box \
        ~/isos/linuxmint-22.3-cinnamon-64bit.iso ~/hackbox-fleet.iso

It prints a one-line summary (`fleet: name from the hub`) and the sha256. Then plug the stick
in and find it; the number changes between plug-ins, so look every time:

    diskutil list external physical
    diskutil info /dev/diskN | egrep 'Media Name|Protocol|Disk Size|Device Location|Removable'

You want the stick's name and size, `Protocol: USB`, `Device Location: External`. Then (this
erases it):

    diskutil unmountDisk /dev/diskN
    sudo dd if=$HOME/hackbox-fleet.iso of=/dev/rdiskN bs=4m && sync && diskutil eject /dev/diskN

`dd` reports 3175546880 bytes. If macOS then says the disk is not readable, click Eject
(never Initialize).

## Install a box (about 30 minutes on a phone hotspot)

1. Network: a cable if there is one. On an iPhone hotspot, turn on Allow Others to Join and
   Maximize Compatibility, and keep the phone on the Personal Hotspot screen for the whole
   install (an iPhone switches the hotspot off when nothing is connected for a while).
2. Plug the stick into the box, power on and tap F7. Pick **`UEFI: USB, Partition 2`**
   (`UEFI: USB` also works).
3. The menu starts "Headless install (ERASES DISK ...)" by itself after 10 s. Leave it.
4. About 6 minutes: installed, the box reboots into Mint (the stick can stay in; it will not
   reinstall). Then provisioning runs by itself: Tailscale, the name from the hub, system
   upgrade (the longest step, about 1.5 to 2 GB of downloads), tools, lockdown, self-test.
5. When it is done the box reboots to the attendee welcome screen and shows on the dashboard
   as `hackboxN`, idle.

Boxes can be installed one after another with the same stick: once a box shows the Mint
desktop (the install part is over), the stick can go to the next box.

Follow one from the Mac (it waits until the box is on the tailnet):

    usb/watch-install.sh hackbox2

## Check a box

    ssh hackbox2 hackbox status          # state idle, agents
    ssh hackbox2 hackbox lock status     # every line OK, exit 0
    ssh hackbox2 hackbox agents show     # offered agents, missing keys
    ssh hackbox2 'bash /opt/hack-box/tests/run.sh'   # 72 pass, 0 fail on hackbox1

## Run the event

- Dashboard: live boxes, `+5 / +10 / +15 / End`, extend requests (Approve or Decline),
  Finish for a box left on the time-up screen, history with names, emails and downloads.
- Agent keys: on the dashboard, Agent keys. Set the Anthropic key and the 0G router key for
  all boxes, or override per box (recommended: one key per box with a spending cap, since
  an attendee can read the key on their own box). Pick the offered agents. Boxes apply it
  between sessions, never during one, and the welcome screen shows only those agents.
- All boxes at once: `for h in hackbox{1..4}; do ssh $h hackbox status; done`.
- 0G skills: `ssh hackboxN hackbox skills update` refreshes the copy in
  `/opt/0g-agent-skills` (fresh homes pick it up at the next reset).
- A dead box: replace it, install it from the same stick. A new machine gets the next free
  number; to give it the dead box's name, press Release name on the dead box's card first.

## Update a running box (no reinstall)

    rsync -az --exclude=.git --exclude=.env --exclude=usb/headless.conf --exclude=usb/out \
        --exclude='*.iso' --exclude=hub/data --exclude=hub/config.json ./ hackbox1:/opt/hack-box/
    ssh hackbox1 'bash /opt/hack-box/setup/70-session.sh && hackbox reset'

(`hackbox reset` only on an idle box; run the matching `setup/NN-*.sh` for other areas.)

## What protects what

- The attendee (and any agent they run) has no sudo, cannot stop or change the timer,
  cannot read the hub token, and cannot reach the tailnet or the LAN (tested on hackbox1).
- A box that is taken over completely reaches only the hub port (tailnet policy), and the
  hub refuses its dashboard to box addresses.
- The Wi-Fi password, the Tailscale key, the enroll token and the admin password hash are on
  the stick: keep it private and re-flash it after the event. The plain admin password and
  `.env` are never on it.

## Troubleshooting

| What you see | Why, and what to do |
|---|---|
| A menu titled "LEGACY BIOS boot" | the firmware booted the stick without UEFI; pick a `UEFI:` entry in F7 |
| `mint.local` on the network, plain Mint desktop | same cause (legacy boot); nothing was installed |
| Box installed but `WAITING for the network` in `/var/log/hackbox-firstboot.log` | the Wi-Fi was not there; turn the hotspot on (or plug a cable) and it continues by itself, or reboot |
| `ssh` says Permission denied though the key is accepted | your key has a passphrase and is not in the agent; use `~/.ssh/hackbox_ed25519` or `ssh-add` |
| Box on the tailnet as `hackbox-xxxx` and not on the dashboard | the hub was unreachable at the first boot; `ssh` in and run `hackbox hub set <url> <token>` with a token from the hub's config |
| Box not on the tailnet at all | the Tailscale key expired or is not tagged; make a new one and rebuild the stick |
