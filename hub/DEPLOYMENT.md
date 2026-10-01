# hackbox-hub: deployment

The session hub runs on jarvis (the Mac mini) for the first event, as one Go binary under a
LaunchAgent. Nothing on the boxes names the host except `HUB_URL`, so it can move to another
server later. Design: `docs/superpowers/specs/2026-10-01-session-hub-design.md`. API:
`openapi.yaml`. Screens: `uiflow.md`.

## Auth model: network isolation

| Surface | Reachable how | Protection |
|---|---|---|
| Box API `/api/v1/*` | tailnet: `http://<jarvis tailnet address>:8210` | per-box bearer token, from `config.json` or handed out by enrollment |
| Enrollment `/api/v1/enroll` | tailnet | the fleet `enroll_token`; 10 calls per minute per address |
| Dashboard `/`, `/dash/*` | tailnet only | no login of its own. Cloudflare Access comes later. |
| Download pages `/d/*`, `/code`, `/healthz` | public, `https://hackbox.udhaykumarbala.dev`, once the optional tunnel step is done | 32 character random tokens; codes limited to 5 tries per minute per IP |

Two locks keep the dashboard and box API off the internet:

1. The tunnel ingress is path-scoped: only `^/(d/|code|healthz)` goes to the hub, the rest of
   the hostname gets a 404 from cloudflared.
2. The hub itself answers 404 to `/`, `/dash/*` and `/api/*` whenever a request carries
   `Cf-Connecting-IP`, so a tunnel mistake cannot publish them.

A third lock keeps the boxes off the dashboard, which shares the hub's port: an address that
made an authenticated box API call (box token, enrolled token or enroll token) in the last
24 hours gets 403 on `/` and `/dash/*`. The list is in memory, so a hub restart clears it
until each box's next heartbeat (3 s). Loopback is exempt. A staff laptop that uses the box
calls on `api_test.html` locks itself out the same way; restart the hub or wait.

`0.0.0.0:8210` also exposes the dashboard on the home LAN. Accepted for now, same as the
monitor.

## On jarvis

| Item | Path |
|---|---|
| Binary | `/Users/jarvis/hackbox-hub/hackbox-hub` |
| Config | `/Users/jarvis/hackbox-hub/config.json` (mode 600, holds box tokens and the GitHub token) |
| Data | `/Users/jarvis/hackbox-hub/data/`: `hub.db` (SQLite, mode 600, also holds the agent keys set from the dashboard) and `archives/<session id>.tar.gz` |
| LaunchAgent | `~/Library/LaunchAgents/com.udhay.hackbox-hub.plist`, label `com.udhay.hackbox-hub` |
| Logs | `~/Library/Logs/hackbox-hub.{out,err}.log` (everything goes to `err`) |
| Port | 8210 |
| Public hostname | `hackbox.udhaykumarbala.dev` (optional step) |

## Config

```json
{
  "public_base_url": "https://hackbox.udhaykumarbala.dev",
  "boxes": {"<token>": "hackbox1", "<token>": "hackbox2", "<token>": "hackbox3"},
  "github": {"org": "0g-hackbox-sessions", "token": ""},
  "download_days": 7,
  "enroll_token": "<random, 16+ characters>",
  "name_prefix": "hackbox"
}
```

- `public_base_url` is what goes into the QR code and the link. It must be the public
  hostname, or attendees cannot open the link from their phones.
- `boxes` maps a box token to a box name. Tokens must be at least 8 characters. A box with an
  unknown token gets 401.
- `github.token` empty means no pushes; sessions show `disabled`. Add a token later and
  restart: archives stored while it was empty are queued then. The token needs repo creation
  and push rights in the org (a fine-grained token on the org with Administration and
  Contents read and write, or a classic token with `repo`). It never leaves the hub and is
  never logged.
- `enroll_token` is the fleet token on the USB stick (`HUB_ENROLL_TOKEN`). With it, a fresh
  box calls `POST /api/v1/enroll` with its MAC and gets a name and its own box token. Empty
  or absent turns enrollment off. It must be at least 16 characters and must not also be a
  box token. It works only on `/api/v1/enroll`, never as a box token.
- `name_prefix` (default `hackbox`) is the stem of enrolled names: `hackbox1`, `hackbox2`, ...
- The deploy script writes this file only when it is missing, with a random token per box,
  and prints them once. It never overwrites it. Edit it on jarvis, then restart.

## Deploy

From the laptop:

```bash
hub/deploy/deploy-to-jarvis.sh            # build, test, push, (re)load
hub/deploy/deploy-to-jarvis.sh --tunnel   # the same, plus the optional public route
```

What it does:

0. Preflight: ssh works, port 8210 is free or held by our own hub, git is present.
1. Runs `go test ./...` and cross-compiles `darwin/arm64` with `CGO_ENABLED=0`.
2. Copies the binary to `hackbox-hub.new` and swaps it into place.
3. Creates `config.json` from a template only if missing (mode 600), with a random token per box and a random `enroll_token`, printed once.
4. Writes the LaunchAgent (RunAtLoad, KeepAlive on crash or failure, ThrottleInterval 10),
   reloads it in `gui/501` and checks `http://127.0.0.1:8210/healthz` on jarvis.
5. Optional, only with `--tunnel`: see below.

Code-only redeploys (step 0 to 4) never touch cloudflared.

### Step 5 (optional): the public tunnel route

Only for the download pages. It shares the `mac-mini` tunnel with the other apps.

1. Backs up `~/.cloudflared/config.yml` to `config.yml.bak-<UTC time>`.
2. If `hackbox.udhaykumarbala.dev` is not there yet, inserts two entries just above the
   `- service: http_status:404` catch-all. Other apps' entries are never moved or removed.

   ```yaml
     - hostname: hackbox.udhaykumarbala.dev
       path: "^/(d/|code|healthz)"
       service: http://localhost:8210
     - hostname: hackbox.udhaykumarbala.dev
       service: http_status:404
   ```

3. Runs `/opt/homebrew/bin/cloudflared tunnel ingress validate`. On failure it restores the
   backup and stops.
4. Routes DNS: `cloudflared tunnel route dns mac-mini hackbox.udhaykumarbala.dev`.
5. Prints, and does not run, the reload. cloudflared runs as a root daemon and re-reads its
   config only on HUP. Run it yourself:

   ```bash
   ssh -t jarvis "sudo kill -HUP \$(pgrep -f 'cloudflared tunnel.*run')"
   ```

Then check from anywhere:

```bash
curl -s https://hackbox.udhaykumarbala.dev/healthz                         # ok
curl -s -o /dev/null -w '%{http_code}\n' https://hackbox.udhaykumarbala.dev/  # 404
curl -s -o /dev/null -w '%{http_code}\n' https://hackbox.udhaykumarbala.dev/dash/state  # 404
```

Never kill or restart the shared cloudflared process. It carries every other app on jarvis.

## Setting the enroll token

Once, on jarvis, then put the same value in the build `.env` as `HUB_ENROLL_TOKEN`:

```bash
TOKEN=$(openssl rand -hex 24)
ssh jarvis "cd /Users/jarvis/hackbox-hub && cp config.json config.json.bak && \
  /usr/bin/python3 -c 'import json,sys; c=json.load(open(\"config.json\")); c[\"enroll_token\"]=sys.argv[1]; json.dump(c, open(\"config.json\",\"w\"), indent=2)' $TOKEN && \
  chmod 600 config.json && launchctl kickstart -k gui/501/com.udhay.hackbox-hub"
echo "HUB_ENROLL_TOKEN=$TOKEN"
```

Or edit `config.json` by hand and restart. The hub refuses to start with a token shorter than
16 characters. To rotate it, set a new value and rebuild the sticks; boxes that already
enrolled keep their own tokens.

How names are given:

- The same MAC always gets the same name back, with a new token. A reinstall keeps its number;
  its old token stops working.
- A new MAC gets the lowest free `hackboxN`. A name is taken when it is bound to a MAC or has
  ever sent a heartbeat. A name that only exists as an unused token in `boxes` is free; it gets
  bound, and its config token keeps working for it too.
- Log lines look like `enroll: aa:bb:cc:dd:ee:ff -> hackbox2`. Tokens are never logged.

A dead box keeps its name until staff press **Release name** on its card (offline for at least
5 minutes). That removes the MAC binding, the enrolled token and its heartbeat record.

## Pointing the boxes at the hub

With the fleet stick this is automatic (see above). For a box with a fixed token (a `--host`
stick, or set by hand), with that box's token from `config.json`:

```bash
ssh hackadmin@hackbox1 sudo hackbox hub set http://<jarvis tailnet address>:8210 <token>
ssh hackadmin@hackbox1 hackbox hub status
```

The dashboard shows the box online within a few seconds.

## Operations

```bash
ssh jarvis 'launchctl list | grep hackbox-hub'
ssh jarvis 'launchctl kickstart -k gui/501/com.udhay.hackbox-hub'    # restart (after a config edit)
ssh jarvis 'tail -f ~/Library/Logs/hackbox-hub.err.log'
ssh jarvis 'curl -s http://127.0.0.1:8210/healthz'
ssh jarvis 'du -sh /Users/jarvis/hackbox-hub/data/archives'
```

- Dashboard: `http://<jarvis tailnet address>:8210/`.
- API test page: `http://<jarvis tailnet address>:8210/dash/api_test.html`.
- Agent keys (Anthropic, 0G router) and the offered agents are set on the dashboard, not in
  `config.json`. They live in the `config` table of `hub.db`; values are never shown on the
  dashboard or written to the log. Treat a copy of `hub.db` as a secret.
- Backup: copy `/Users/jarvis/hackbox-hub/data/` while the service is stopped, or use
  `sqlite3 hub.db ".backup hub-copy.db"` while it runs.
- A GitHub push that failed is retried every 5 minutes. The error shows on hover in the
  history table. Downloads never wait for GitHub.
- Archives are kept after the attendee link expires; staff can still download them from the
  history table. Nothing prunes them yet.

## Running locally

```bash
cd hub
go test ./... && go vet ./...
go build -o hackbox-hub .
cp config.example.json config.json            # box token "devtoken" for hackbox1
./hackbox-hub -listen 127.0.0.1:8210 -data ./data -config ./config.json
open http://127.0.0.1:8210/                   # dashboard
open http://127.0.0.1:8210/dash/api_test.html # fake a box
```

Flags can also come from the environment: `HACKBOX_HUB_LISTEN`, `HACKBOX_HUB_DATA`,
`HACKBOX_HUB_CONFIG`. SIGTERM or Ctrl-C shuts down cleanly.

Cross-compile for a Linux server later:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o hackbox-hub-linux-amd64 .
```

## Rollback

The previous binary is not kept. To roll back, check out the previous commit and run the
deploy script again. `config.json` and `data/` are never touched by a deploy.
