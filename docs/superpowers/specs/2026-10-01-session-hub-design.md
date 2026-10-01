# Session hub: saved work, download QR, staff dashboard

Status: approved in conversation 2026-10-01, building. First host: jarvis (Mac mini), moved to
a dedicated server after the test; nothing in the boxes names the host except `HUB_URL`.

## What the owner asked for

- Every session's `~/project` is kept centrally, in **private** repos of a new GitHub org
  (`0g-hackbox-sessions`), for the organisers.
- The attendee leaves with a **QR code and a link** (plus the 6 character code) to download
  their work, proxied through our system. Valid 7 days.
- The attendee types their **name on the welcome screen**.
- The time-up screen stays until the attendee presses **Done** (or a timeout).
- The attendee can **ask for more time**; staff approve on the dashboard.
- One **dashboard** for all boxes: live timers, extend, end, extend requests, and the history
  of previous sessions with names.
- Cloudflare Access is added later tonight. Until then the dashboard is reachable on the
  tailnet only; the public tunnel carries only the download pages.

## Components

| Unit | Where | Does |
|---|---|---|
| `hub/` (Go, SQLite, one binary `hackbox-hub`) | jarvis, LaunchAgent `com.udhay.hackbox-hub`, port 8210 | session registry, archive store, download pages, GitHub push queue, dashboard |
| `hackbox-hubagent` (Python 3 stdlib, root) | each box, `hackbox-hubagent.service` | the only thing on a box that talks to the hub; polls it, runs `extend`/`end` |
| overlay changes | each box, attendee session | name field, "Ask for more time", QR on the time-up screen, Done |
| `hackbox-extend-request.service`, `hackbox-done.service` | each box, root oneshots | the attendee's two new requests, allowed by polkit (start only), like the Start button |

Security rules:

- The GitHub token lives only on the hub. Boxes never hold it.
- Boxes pull commands (heartbeat reply); the hub never connects to a box. A hub compromise can
  extend or end sessions, nothing else. The agent accepts only `extend <1..60>` and `end`.
- The attendee uid cannot reach the hub (the attendee nft chain rejects tailnet and private
  ranges). Only the root agent can.
- Everything the attendee supplies (the name) is read by root with `O_NOFOLLOW`, must be a
  regular file owned by the attendee, at most 256 bytes, and is reduced to 40 printable
  characters.
- Download URLs carry a 32 character random token (base32, about 160 bits). The short code
  also works, typed on `/code`, limited to 5 tries per minute per client IP.
- `/`, `/dash/*` and `/api/*` refuse any request that came through Cloudflare (has a
  `Cf-Connecting-IP` header), so a tunnel misconfiguration cannot publish them.

## Box side

Settings: `/etc/hackbox/hub.conf` (root 0600): `HUB_URL`, `HUB_TOKEN`. `hackbox hub set <url>
<token>` writes it and `/etc/hackbox/conf.d/38-hub.conf` (`END_GRACE_SECONDS=180`), enables the
agent. `hackbox hub status` prints the agent's view. Without `hub.conf` the agent stays idle and
the box behaves exactly as before (20 s time-up screen, pickup code only).

Runtime files the agent writes for the overlay (root owned, 0644, in `/run/hackbox`):
`hub-url` (download URL), `qr.png`, `extend-status` (`pending`, `approved <min>`,
`declined`, absent), `attendee` (the cleaned name, for the time-up screen).

Flow:

1. Welcome: the attendee types a name; the overlay writes it to
   `~/.cache/hackbox/attendee-name`, then starts `hackbox-session.service` as today.
2. idle to active (agent sees it within 1 s): read and clean the name, `POST /api/v1/sessions`,
   write `hub-url`, fetch `qr.png`. If the hub is down: retry in the background; the session
   is never delayed.
3. Every 3 s: `POST /api/v1/heartbeat` with the box status; run returned commands through
   `hackbox extend` / `hackbox end`; post each result.
4. "Ask for more time": polkit lets the attendee start `hackbox-extend-request.service`, which
   writes `/run/hackbox/extend-request` (only when active and nothing is pending). The agent
   sends it with the next heartbeat and mirrors the decision into `extend-status`.
5. Time-up: the overlay shows the QR at once (it already has `hub-url`). Done starts
   `hackbox-done.service`, which writes `/run/hackbox/ending-done`; the reset's grace wait ends
   early. Without Done the grace (`END_GRACE_SECONDS`, 180 with a hub) runs out.
6. After the reset archived `~/project` as `<code>.tar.gz`, the agent uploads it
   (`PUT /api/v1/sessions/{id}/archive`), or reports `empty`. The job is kept in
   `/var/lib/hackbox/hub/` and retried with backoff until the hub accepts it, across reboots.

## Hub API

Box calls carry `Authorization: Bearer <box token>`; the hub's config maps tokens to box names.
JSON in and out unless noted. Times are Unix seconds.

| Call | Body | Reply |
|---|---|---|
| `POST /api/v1/sessions` | `{"name","agent","minutes","started_at","local_code"}` | `{"id","token","code","url","expires_at"}` |
| `GET /api/v1/sessions/{id}/qr.png` | | PNG of `url` |
| `POST /api/v1/heartbeat` | `{"session_id","state","seconds_left","extend_request_at","status":{...hackbox status --json}}` | `{"commands":[{"id","op":"extend"\|"end","minutes"}],"extend":{"status":"none"\|"pending"\|"approved"\|"declined","minutes"}}` |
| `POST /api/v1/commands/{id}/result` | `{"ok","message"}` | `{}` |
| `POST /api/v1/sessions/{id}/end` | `{"ended_at","reason","empty"}` | `{}` |
| `PUT /api/v1/sessions/{id}/archive` | raw `.tar.gz`, at most 250 MB | `{}` |

Public (through the tunnel):

| Path | Does |
|---|---|
| `GET /d/{token}` | page: "Preparing your files" (refreshes every 3 s), download button, "nothing was saved", or expired |
| `GET /d/{token}/download` | the project as a `.zip` (converted from the tarball, symlinks left out) |
| `GET /code`, `POST /code` | type the 6 character code, redirects to `/d/{token}` |

Dashboard (tailnet only tonight):

| Path | Does |
|---|---|
| `GET /` | the dashboard page, refreshes from `/dash/state` every 2 s |
| `GET /dash/state` | boxes (online, state, time left, attendee, pending request), last 200 sessions |
| `POST /dash/boxes/{box}/extend` `{"minutes"}` | queue `extend` |
| `POST /dash/boxes/{box}/end` | queue `end` |
| `POST /dash/requests/{id}` `{"approve","minutes"}` | decide an extend request (approve queues `extend`) |
| `GET /dash/sessions/{id}/download` | staff download of any session |

## Hub data (SQLite)

`sessions(id, box, name, agent, minutes, started_at, ended_at, end_reason, extended_minutes,
token, code, expires_at, archive_path, archive_bytes, empty, github_repo, github_status,
github_error)`, `boxes(name, last_seen, state, seconds_left, status_json, session_id)`,
`requests(id, session_id, box, asked_at, status, minutes, decided_at)`, `commands(id, box,
op, minutes, created_at, picked_at, ok, message)`.

GitHub: after an archive is stored, a worker creates `0g-hackbox-sessions/<yyyymmdd-hhmm>-<box>-<name-slug>-<code>`
(private), unpacks the tarball into a temporary directory and pushes one commit with the git
CLI. `github_status`: `disabled` (no token), `queued`, `pushed`, `failed` (retried every 5
minutes). The download never waits for GitHub.

## Testing

- Hub: Go tests with `httptest` for every API call, the token and code paths, the rate limit,
  the tunnel guard, tar.gz to zip, and the GitHub worker against a fake.
- Agent: a test run on the Mac against a local hub with a fake `/run/hackbox` (paths are
  overridable by environment), covering start, heartbeat, extend request, end, upload, retry.
- Box: on hackbox1 after provisioning, one real session end to end, then `tests/run.sh`.

## Not in this round

Cloudflare Access (tonight, by the owner), per-attendee GitHub access, email delivery,
encrypting archives at rest.

## Round 2 (approved 2026-10-01): agent keys from the dashboard, one-file build

### Agent keys and agent choice from the dashboard

- Dashboard panel "Agent keys": `anthropic-api-key` and `0g-router-key`, and the offered agents
  (`claude`, `claude-0g`, `opencode`), for scope `*` (all boxes, the default) or one box (an
  override). Write-only: the page shows "set, updated <time>" and never a value. Clearing a
  value at box scope falls back to the default.
- Hub: `config` rows `(scope, name, value, updated_at)`; the effective config of a box is the
  box row, else the `*` row, per name. `config_version` = a hash of the effective config.
- Heartbeat reply gains `"config_version"`. `GET /api/v1/config` (box auth) returns
  `{"version","secrets":{"anthropic-api-key":...,"0g-router-key":...},"agents":[...]}` with
  only the names that are set.
- Box agent: when the version differs from the last applied one and the box is **idle**, it
  fetches the config, writes each secret with `hackbox secret set <name>` (value on stdin),
  runs `hackbox agents set <agents...>` when given, then `hackbox reset` so the welcome screen
  shows it. Never during a session. A name the hub does not have is left alone on the box.
- Per-box keys with spending caps are recommended (an attendee can read the key on their box).

### One `.env` for the whole build

- `.env` at the repo root (git-ignored), `.env.example` committed. It is a shell file with the
  `headless.conf` keys plus `HUB_URL` and `HUB_TOKEN_<hostname>` per box.
- `make-usb-iso.sh --config .env --host hackbox2` builds the stick for one box (the hostname
  overrides `HB_HOSTNAME`). With `HUB_URL` and that box's token, the stick carries
  `hub.conf`; the first boot runs `hackbox hub set` after the session engine is installed.
- Agent keys are not put on the stick; they come from the dashboard.
- ssh: a dedicated, passphrase-less key `~/.ssh/hackbox_ed25519` on the build machine (in
  `SSH_KEY_FILES`) and `~/.ssh/config` entries for `hackbox1..4` (user `hackadmin`, the
  Tailscale names), so non-interactive ssh always works.
