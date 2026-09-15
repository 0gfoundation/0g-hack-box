# 0g-hack-box

Provisioning and benchmarking for the hacker zone machines: shared computers
where attendees build a 0G project with Claude Code or OpenCode inside a
30 minute slot.

Current phase: work out whether an N100 / 16 GB / 512 GB box copes with a
few Claude Code sessions, a local dev server and a browser at once, and if
not, what to order instead. Session reset and credits isolation come later.

Nothing in this repo is secret. Keys, tokens and funds never go here.

## Fresh box

Install Linux Mint Cinnamon 22 from the standard ISO (there is no minimal
one), set the hostname to `hackboxN`, then as the normal user:

    curl -fsSL https://raw.githubusercontent.com/0gfoundation/0g-hack-box/main/bootstrap.sh | bash

This clones the repo to `/opt/hack-box` and runs `setup/NN-*.sh` in order:

| script | does |
|---|---|
| `05-tailscale.sh` | joins the tailnet, prints a login URL on first run |
| `10-debloat.sh` | purges LibreOffice, Thunderbird, media apps, ibus and the like |
| `15-upgrade.sh` | `apt full-upgrade`, non-interactive. The only place upgrades happen |
| `20-services.sh` | disables cups, ModemManager, avahi, bluetooth (`KEEP_BLUETOOTH=1` to keep), apport, apt daily timers. Masks suspend. Hides the update, report, welcome and bluetooth tray apps |
| `30-tools.sh` | chromium, Node 22, Claude Code, OpenCode |
| `40-ssh.sh` | sshd, key-only, keys from `keys/*.pub`, passwordless sudo for the admin user |
| `50-desktop.sh` | dark theme. Screensaver, lock, display sleep and notification popups off |

`setup/optional/` holds things we test separately, like zram.

Rerunning bootstrap pulls the latest and reruns everything. Scripts are
idempotent.

## Benchmarks

`bench/monitor.sh <csv> [interval]` samples load, CPU %, memory and swap
until killed. Run it in the background, apply a load, stop it, commit the
CSV under `results/<hardware>/` with a line in `results/<hardware>/notes.md`
saying what the load was.
