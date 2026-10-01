# 0g-hack-box

Provisioning and benchmarking for the hacker zone machines: shared computers
where attendees build a 0G project with Claude Code or OpenCode inside a
30 minute slot.

Current phase: the event boxes are fanless Ryzen 7 5825U / 16 GB / 512 GB
machines. The N100 results below stay as the baseline. Session reset,
agent setup and lockdown now exist: an attendee account on a disposable
home, a root-owned 30 minute timer that wipes the box after each slot,
Claude Code and OpenCode configured for one key per box, and a locked-down
desktop and network. See `docs/`.

Nothing in this repo is secret. Keys, tokens and funds never go here.

## Fresh box

Install Linux Mint Cinnamon 22 from the standard ISO (there is no minimal
one), set the hostname to `hackboxN`, then as the normal user:

    curl -fsSL https://raw.githubusercontent.com/0gfoundation/0g-hack-box/main/bootstrap.sh | bash

This clones the repo to `/opt/hack-box` and runs `setup/NN-*.sh` in order.
BIOS settings, the USB installer and running from a branch are in
`docs/INSTALL.md`.

| script | does |
|---|---|
| `05-tailscale.sh` | joins the tailnet, prints a login URL on first run. `TS_TAG=tag:hackbox` joins as a tagged kiosk |
| `10-debloat.sh` | purges LibreOffice, Thunderbird, media apps, ibus and the like |
| `15-upgrade.sh` | `apt full-upgrade`, non-interactive. The only place upgrades happen |
| `20-services.sh` | disables cups, ModemManager, avahi, bluetooth (`KEEP_BLUETOOTH=1` to keep), apport, apt daily timers. Masks suspend. Hides the update, report, welcome and bluetooth tray apps |
| `30-tools.sh` | chromium, Node 22, git, gh. Claude Code from Anthropic's apt repo (held) and a pinned OpenCode binary, both system-wide |
| `40-ssh.sh` | sshd, key-only, keys from `keys/*.pub`, passwordless sudo for the admin user. The attendee can never ssh in |
| `50-desktop.sh` | dark theme. Screensaver, lock, display sleep and notification popups off |
| `60-hacker.sh` | attendee account `hacker` (no sudo, locked password), autologin, home skeleton |
| `65-ai.sh` | agent configs and keys directory. `hackbox agents set` picks what the welcome screen offers |
| `70-session.sh` | disposable loop-mounted home, root timer, reset, work archive, welcome/countdown overlay, `hackbox` CLI |
| `80-lockdown.sh` | attendee firewall, resource caps, desktop locks, Chromium policy. Check with `hackbox lock status` |
| `90-verify.sh` | runs the non-destructive checks in `tests/` and prints a banner. Never aborts the install |

`setup/optional/` holds things we test separately, like zram.

Rerunning bootstrap pulls the latest and reruns everything. Scripts are
idempotent. Reboot after the first run: autologin into the attendee
session starts at the next boot.

## Running the event

- `docs/INSTALL.md`: BIOS checklist, USB or manual install, bootstrap, replacing a box.
- `docs/SECRETS.md`: one key per box, which keys, how to set, rotate and revoke them.
- `docs/EVENT-RUNBOOK.md`: for the desk. Morning checklist, commands, troubleshooting.
- `docs/ARCHITECTURE.md`: how it works, and what it does and does not defend.

Staff run `hackbox` over ssh as the admin user:

| command | does |
|---|---|
| `hackbox status [--json]` | state, time left, agents, load, memory, home disk, last reset |
| `hackbox start [minutes]` | start a session from the welcome screen |
| `hackbox extend <minutes>` | add time to the running session |
| `hackbox end` | time-up screen with the pickup code, then the wipe |
| `hackbox reset` | wipe now |
| `hackbox archive list \| get <code>` | a finished session's `~/project`, kept 24 hours |
| `hackbox agents set <agent>... \| show` | `claude`, `claude-0g`, `opencode`: which agents the box offers |
| `hackbox secret set <name> \| list \| rm <name>` | agent keys, never printed |
| `hackbox lock status` | one line per lockdown control, OK or MISSING |

## Benchmarks

`bench/monitor.sh <csv> [interval]` samples load, CPU %, memory and swap
until killed. Run it in the background, apply a load, stop it, commit the
CSV under `results/<hardware>/` with a line in `results/<hardware>/notes.md`
saying what the load was.
