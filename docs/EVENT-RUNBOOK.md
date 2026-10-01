# Event runbook

For the person at the desk. Every staff command runs over ssh from a laptop on the tailnet:
`ssh hackadmin@hackbox1 hackbox <command>`. The boxes are `hackbox1`, `hackbox2`,
`hackbox3`. Box setup is in `INSTALL.md`, keys in `SECRETS.md`, internals in
`ARCHITECTURE.md`.

## Morning checklist (each box)

1. Screen shows the welcome screen with the right hostname ("0G HACK-BOX · hackbox1").
   Keyboard and mouse work. Wired network if you have it.
2. `hackbox status`: `state idle`, home mounted, no `WARNING` line, the agents you expect.
3. `hackbox lock status`: every line `OK` (exit code 0).
4. `hackbox agents show`: every offered agent `ready`.
5. `hackbox secret list`: only the keys this box needs are present.
6. One smoke test per offered agent (`SECRETS.md`, "Smoke test each agent").
7. `hackbox archive list`: nothing left from yesterday you still need to hand out.

All three at once:

    for h in hackbox1 hackbox2 hackbox3; do echo "== $h"; ssh -o ConnectTimeout=5 hackadmin@$h 'hackbox status; hackbox lock status >/dev/null && echo "lock OK" || echo "lock MISSING, run: hackbox lock status"'; done

## What the attendee sees

| When | On screen |
|---|---|
| Idle | Full-screen welcome: "Build something on 0G.", three rules (30 minutes, the machine is wiped, no personal passwords or keys), one green button per agent ("Start with Claude Code", "Start with OpenCode") with a note under each ("Anthropic", "0G Compute"). |
| Start pressed | The clock starts. A maximised terminal opens in `~/project` running the chosen agent. When the agent exits, the terminal says how to run it again and leaves a normal shell. |
| Active | A small countdown pill (mm:ss) with "End now" at the top centre. Clicking the time moves it to the bottom edge. The shell prompt also shows the time left, like `[12:34]`. |
| 5:00 left | Pill turns amber, a large toast: "5 minutes left. Commit and push your work now." (12 s, click to dismiss). |
| 1:00 left | Pill turns red, toast: "1 minute left. Everything on this machine is wiped at 00:00." |
| End now | Confirmation dialog, "Keep going" or "End now". |
| Time is up | Full screen: "Time is up. Thanks for hacking!", "Your ~/project was saved. Your pickup code:", a 6 character code, "Show this code at the help desk within 24 hours to get a copy.", and "This machine is wiped in N s." (20 s). If nothing changed in `~/project`: "There was nothing new in ~/project to save." |
| Resetting | "Resetting this machine for the next person…", about a second, then the welcome screen again. |

From the end of a slot to the next welcome screen took about 21.6 s in the lab: 20 s of
"Time is up", under 1.2 s of wipe, about 1 s for the desktop.

In the lab the 1 minute toast was missing once in five tries (the red pill still showed).
Not reproduced since.

## Staff commands

`hackbox` with no arguments lists them:

    usage: hackbox <command> [args]

    commands:
      agents     set <agent>... | show  (which agents the welcome screen offers)
      archive    project archives: list | get <code> [file|-] | prune (get streams when piped)
      end        end the session gracefully (time-up screen, then wipe)
      extend     add minutes to the running session <minutes>
      lock       status  (one line per lockdown control, OK or MISSING)
      reset      wipe the attendee session now, no time-up screen
      secret     set <name> | list | rm <name>  (agent keys, never printed)
      start      start the attendee's session now [minutes]
      status     session state, time left, agents, load, memory, home disk, last reset [--json]

Output below is from the lab VM (`hackbox-vm1`, 8 GB) or follows the command's own format
strings; times and sizes will differ.

### `hackbox status`

    host        hackbox-vm1
    state       idle
    session     30 minutes
    agents      claude-0g opencode
    load        0.58
    memory      1042 / 7930 MB
    home        119 / 39630 MB
    last reset  2026-09-30 21:43:18, took 0.7s

During a session the state line reads `state       active, 29:48 left (timer active)`; on the
time-up screen, `state       ending (time-up screen), code 98RLNN`. A `WARNING` line appears if
the box fell back to a RAM home (see troubleshooting). `hackbox status --json` prints the
same as one line of JSON (`state`, `seconds_left`, `agents`, `archive_code`, `degraded`,
`home_used_mb`, `last_reset_seconds`, `hostname` and more), for scripts.

### `hackbox start [minutes]`

Starts the clock from the welcome screen, as if the attendee pressed a button, but opens a
plain terminal instead of an agent. Default 30 minutes, 1 to 600 allowed.

    $ hackbox start 45
    session started: 45 minutes, ends 14:47:10

Only from `idle`; otherwise `hackbox: state is 'active', a session can only start from idle`.

### `hackbox extend <minutes>`

    $ hackbox extend 10
    extended by 10 minutes: ends 14:42:10, 21:33 left

Only while `active`. The 5 and 1 minute warnings re-arm if the extension moves the end back
above them.

### `hackbox end`

The graceful end, same as the attendee's "End now": time-up screen with the pickup code for
20 s, then the wipe.

    $ hackbox end
    ending: time-up screen for 20s, then the wipe
    follow with: journalctl -t hackbox-reset -f

### `hackbox reset`

Wipes now, no time-up screen. If a session was running, `~/project` is still archived under a
fresh code the attendee never saw; find it by time with `hackbox archive list`.

    $ hackbox reset
    resetting...
    done in 0.7s at 14:12:03, state idle

Use it on an idle box to apply a changed agent selection to the welcome screen.

### `hackbox archive list | get <code>`

    $ hackbox archive list
    CODE     CREATED                 SIZE
    98RLNN   2026-09-30 21:40:02      28K

    $ hackbox archive get 98RLNN
    saved ./hackbox1-98RLNN.tar.gz (28K); unpack with: tar -xzf ./hackbox1-98RLNN.tar.gz

Codes are case-insensitive. The header row only prints in an interactive terminal. `get` streams the tarball when its output is redirected, so from
a laptop: `ssh hackadmin@hackbox1 hackbox archive get 98RLNN > 98RLNN.tar.gz`.

### `hackbox agents set` (switch Claude Code / OpenCode)

One command per box, no reinstall:

    hackbox agents set claude opencode      # Claude Code on Anthropic + OpenCode on 0G (default)
    hackbox agents set claude-0g opencode   # both on 0G Compute
    hackbox agents set opencode             # OpenCode only
    hackbox agents show

The configs are rewritten at once, so an agent launched afterwards uses them. The welcome
buttons change after the next reset (`hackbox reset` on an idle box). An agent that is already
running keeps its settings.

### `hackbox lock status`

One line per lockdown control; exit 1 if any is `MISSING`. Lab output:

    no-sudo            OK       groups: hacker; sudo -l: not allowed
    password-locked    OK       passwd -S: L
    su-sudo-only       OK       /etc/pam.d/su: auth       requisite  pam_wheel.so group=sudo
    cron-deny          OK       hacker in /etc/cron.deny
    at-deny            OK       hacker in /etc/at.deny
    no-linger          OK       /var/lib/systemd/linger/hacker absent
    sshd-deny          OK       sshd -T: denyusers hacker
    admin-home         OK       /home/hackadmin mode 750
    vt-switch-off      OK       logind NAutoVTs=0; xorg Option DontVTSwitch true (applies after reboot / X restart)
    slice-limits       OK       user-2000.slice: CPUQuotaPerSecUSec=2s MemoryHigh=5719982080 MemoryMax=6240075776 MemorySwapMax=2147483648 TasksMax=4096
    nproc-limit        OK       limits.d: hacker hard nproc 4096
    nft-egress         OK       table inet hackbox loaded, uid 2000 jumps to attendee; unit enabled
    nft-inbound        OK       new inbound dropped except lo, tailscale0, ICMP, DHCP; open: tcp dport 22
    nftables-conf-off  OK       nftables.service not enabled
    tailscale-tag      OK       not requested (TS_TAG unset)
    dconf-system-db    OK       profile: user-db:user system-db:local ; compiled db present
    dconf-locks        OK       effective: disable-log-out=true disable-user-switching=true disable-lock-screen=true lock-enabled=false; locks file present
    usb-no-autoopen    OK       automount-open=false; usb storage allow
    chromium-policy    OK       /etc/chromium/policies/managed/hackbox-lockdown.json: sign-in off, sync off, password manager off, DevTools on
    claude-policy      OK       /etc/claude-code/managed-settings.json: bypass disabled, acceptEdits, updates off
    opencode-policy    OK       /etc/opencode/opencode.json: autoupdate off, share disabled, provider 0g only
    claude-held        OK       apt-mark hold: claude-code
    secrets-dir        OK       /etc/hackbox/secrets root:hacker 750
    secret-modes       OK       key files root:hacker 0640
    agents-manifest    OK       /etc/hackbox/agents.json offers: claude-0g opencode

(The lab keeps tcp/22 open for its own ssh; a real box shows no `open:` part, and with `TS_TAG`
set the `tailscale-tag` line names the tag. A 16 GB box has larger slice limits.)

## "An attendee lost their work"

1. Ask for the pickup code from the time-up screen, and which box they used.
2. `ssh hackadmin@hackbox2 hackbox archive get K7M2QX > K7M2QX.tar.gz`
3. Do not know the box? `for h in hackbox1 hackbox2 hackbox3; do ssh hackadmin@$h hackbox archive list | grep -q '^K7M2QX' && echo $h; done`
4. No code (they left early, or staff ran `hackbox reset`)? `hackbox archive list` on their
   box and pick by time.
5. Hand it over on their USB stick, or push it to a repo they own.

What is in it: `~/project` without `node_modules`, build output (`dist`, `build`, `.next`,
`target` and the like), `.env` and other secret-looking files, `.git` objects when `.git` is
over 50 MB, and the biggest files beyond 200 MB in total. Archives are kept 24 hours, then
deleted by an hourly timer. A slot whose `~/project` was unchanged has no archive.

## Troubleshooting

| Symptom | Likely cause | Do this |
|---|---|---|
| Terminal says "This station has no key for claude yet. Please ask a staff member." | key missing for that agent | `hackbox secret list`, then `ssh -t ... hackbox secret set <name>`. The attendee reruns the command the terminal shows (`/usr/local/bin/hackbox-agent claude`). Their clock was running: `hackbox extend 5`. |
| "This station is not fully set up for claude yet (CLAUDE_GATEWAY_URL)" | route needs a setting | set it in `/etc/hackbox/conf.d/29-ai-local.conf`, then `hackbox agents set ...` |
| "This station does not offer 'opencode' right now" | that agent is not selected | `hackbox agents set ...` including it |
| Welcome buttons show the wrong agents | selection changed after the desktop started | `hackbox reset` on the idle box |
| Agent is slow | 0G models are slower than Sonnet (estimated 1.5x to 3x); first OpenCode start in a fresh home downloads its provider package (about 110 MB) | in OpenCode, switch model to Kimi K2.7 Code; or switch the box: `hackbox agents set claude opencode`, and the attendee runs `hackbox-agent claude` in their terminal |
| Agent shows 429, 402 `insufficient_balance`, or stops answering | rate limit, key cap reached, Payment Layer empty, Console workspace limit | check the key at pc.0g.ai or in the Console; raise the cap or top up; or switch provider as above |
| Stuck on "Time is up" for more than 30 s | reset stalled | the guard retries after 30 s by itself; otherwise `hackbox reset`, and read `journalctl -t hackbox-reset -n 50` |
| `hackbox status` shows `WARNING tmpfs home since ...` | the home image could not be built (usually a full disk), so the box runs on a 4 GB RAM home | `ssh ... df -h /var/lib/hackbox`, `journalctl -t hackbox-reset -n 50`; free space (old archives), then `hackbox reset` on an idle box; the warning clears on the next good reset |
| `home NOT MOUNTED` in `hackbox status` | reset or boot failed half way | `hackbox reset`; if it persists, reboot |
| npm, git or the browser cannot reach anything | venue network down, captive portal, Wi-Fi dropped | if you can ssh in, the box is online: test `ssh ... "sudo -iu hacker curl -sI https://registry.npmjs.org"`. Private addresses (192.168.x.x, 10.x) are refused for the attendee on purpose, so captive portal pages will not load: use a network without one, or allow the portal's IP with `NFT_ALLOW4` in `39-lockdown-local.conf` and rerun `setup/80-lockdown.sh`. The attendee cannot change network settings; if ssh does not work either, plug in a cable or reboot. |
| Attendee locked the screen | Cinnamon's lock | press Enter at the lock screen: it unlocks with no password for the attendee. Ctrl+Alt+L is unbound. |
| Countdown pill or welcome screen vanished | the overlay was killed | it comes back within about 10 s (the clock keeps running regardless). If not: `sudo systemctl restart hackbox-guard` (safe any time), `journalctl -t hackbox-guard -n 20` |
| Someone is using the desktop with no countdown | the idle welcome screen was bypassed (Super key menu, Alt+F2) | `hackbox start` to put them on the clock, or `hackbox reset`. Hardening status: docs/log/redteam.md in the project notes. |
| Start button says "Could not start the session" | timer unit failed | `hackbox start` from ssh, `journalctl -t hackbox-session -n 20` |
| Box needs a reboot | anything odd that a reset did not fix | `hackbox end` first if someone is working (a boot builds a fresh home but does not archive), then `ssh ... sudo systemctl reboot`. The welcome screen was back 9.7 s after the reboot command in the lab. |
| Box unreachable over ssh | Tailscale or the network is down; there are no text consoles on the box | check it is powered and cabled, then power-cycle. Replace: `INSTALL.md`, "Replace a dead box on the day". |

Logs: `journalctl -t hackbox-reset`, `-t hackbox-session`, `-t hackbox-guard`,
`-t hackbox-archive`, `-t hackbox-ai-seed`.

## Switching every box at once

From a staff laptop. Idle boxes are reset so the welcome screen shows the new choice; busy
boxes keep the running session and pick it up at the next reset:

    for h in hackbox1 hackbox2 hackbox3; do
      echo "== $h"
      ssh -o ConnectTimeout=5 hackadmin@$h 'hackbox agents set claude-0g opencode >/dev/null &&
        if hackbox status --json | grep -q "\"state\":\"idle\""; then hackbox reset;
        else echo "in a session: welcome buttons change after it ends"; fi'
    done

Change `claude-0g opencode` to `claude opencode` to switch back.

## End of day

1. Hand out any archives people asked for. The rest delete themselves 24 hours after they
   were made. To clear them now (there is no CLI command for this):

       for h in hackbox1 hackbox2 hackbox3; do ssh hackadmin@$h "sudo sh -c 'rm -f /var/lib/hackbox/archive/*.tar.gz'"; done

   (The `sh -c` matters: the admin cannot list the archive directory, so the glob must expand
   as root.)
2. Revoke the per-box keys at the providers and remove them from the boxes (`SECRETS.md`,
   "End of the event").
3. `hackbox reset` each box, or power them off: `ssh ... sudo systemctl poweroff`.
