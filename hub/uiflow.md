# Session hub: UI flows

The screens and pages an attendee and the staff see, step by step, and which hub call sits
behind each step. The API contract is `openapi.yaml`; the design is
`docs/superpowers/specs/2026-10-01-session-hub-design.md`.

Screens on the box are drawn by the overlay. Pages on a phone or laptop are served by the hub.

## Attendee

### 1. Welcome screen (box)

1. The attendee types a first name (optional), optionally an email and a Telegram handle,
   and presses an agent button.
2. The overlay writes the name to `~/.cache/hackbox/attendee-name` and starts the session.
3. Within a second the box agent sees `idle` turn into `active` and calls
   `POST /api/v1/sessions` with the cleaned name, email, Telegram handle, agent, minutes and
   start time. An email or handle that does not look right is dropped (a handle is 5 to 32
   letters, digits or underscores, with or without "@"); the session starts anyway. Only
   staff see them (dashboard); the download page never shows them.
4. The hub answers with `id`, `token`, `code`, `url`, `expires_at`. The agent writes `url`
   to `/run/hackbox/hub-url` and fetches `GET /api/v1/sessions/{id}/qr.png` into
   `/run/hackbox/qr.png`.
5. If the hub is down the session starts anyway; the agent retries in the background.

### 2. During the session (box)

1. The countdown pill shows the time left.
2. Every 3 s the agent sends `POST /api/v1/heartbeat` (state, seconds left, status).
3. When staff extend or end from the dashboard, the next heartbeat reply carries the command.
   The agent runs `hackbox extend <min>` or `hackbox end` and reports
   `POST /api/v1/commands/{id}/result`. The pill shows the new time.

### 3. Ask for more time (box)

1. The attendee presses "Ask for more time".
2. The box writes `/run/hackbox/extend-request`; the next heartbeat carries
   `extend_request_at`.
3. The hub creates a pending request. The heartbeat reply says `extend.status = pending`; the
   overlay shows "Request sent".
4. Staff approve (+5, +10 or +15) or decline on the dashboard.
5. Approved: the reply says `approved <min>` and carries an `extend` command; the pill jumps
   up. Declined: the reply says `declined`; the overlay says so.

### 4. Time is up (box)

1. The full-screen time-up screen shows the QR code, the short link and the 6 character code
   at once (the agent already has them from step 1).
2. The attendee scans the QR with a phone, or notes the code, then presses **Done**.
   Without Done the screen stays for the grace time (180 s with a hub).
3. The agent sends `POST /api/v1/sessions/{id}/end` with the reason and whether `~/project`
   was empty.
4. After the reset archived `~/project`, the agent uploads it with
   `PUT /api/v1/sessions/{id}/archive`. Nothing to save means `empty: true` and no upload.

### 5. Download page (phone or laptop, `/d/{token}`)

The QR and the link open the same page. It shows the 0G hack-box name, "Hi <first name>",
the box and the session date and time.

| State | When | What the attendee sees |
|---|---|---|
| Preparing | no archive yet, not marked empty | "Preparing your files...", a spinner; the page reloads every 3 s |
| Ready | archive stored | a big **Download** button, the size, the last valid day |
| Empty | the box reported nothing to save | "There was nothing new in ~/project to save." |
| Expired | after `expires_at` (7 days) | "This link has expired" (HTTP 410) |
| Not found | unknown token | "We could not find that session", a button to enter a code (HTTP 404) |

1. Usually the page shows Preparing for a few seconds, then Ready by itself.
2. **Download** fetches `/d/{token}/download`: `hackbox-<code>.zip`, one folder
   `<code>-project/` with the project inside. Symlinks are left out.
3. **Reward** (only when the hub has a `pay` config): under the download card, in the Ready
   and Empty states, a second card shows a floating gift box, "$10" in large type, "A gift for
   your project" and "Keep building on 0G: $10 of model credit for the wallet you use with 0G".
   **Connect wallet and claim** asks the browser wallet for an account (`eth_requestAccounts`)
   and posts it to `/d/{token}/credit`; on a phone without a wallet the attendee opens the
   link inside the wallet app's browser, or unfolds "Paste your address" and types it
   (mixed-case addresses must pass the EIP-55 checksum, so a typo is caught). The hub grants
   the credit through the 0G Pay partner regrant API; on success confetti falls and the card
   turns into "Claimed: $10 of model credit is yours, it is on 0x1234…abcd" with a
   **Spend it on pc.0g.ai** link and "Model credit for 0G Compute, valid until 13 October
   2026" (the link and the expiry are shown only after the claim; `spend_url`,
   `expiry_label` in the config).
   Rules: one credit per session (a second click says where it went), at most 2 credits per
   wallet over all sessions ("…already received 2 credits… Use another wallet"), 10 tries per
   minute per address. If the pool is empty or Pay rejects the row, the page says so and
   points to the staff. After a reload the card shows "Credited" with the wallet.

### 6. Typing the code instead (`/code`)

1. The attendee opens `/code` (also linked from every page footer).
2. They type the 6 character code. Case, spaces and dashes do not matter.
3. A match redirects to `/d/{token}` (step 5). No match shows the form again with
   "No session with that code".
4. More than 5 tries in a minute from one address shows "Too many tries" (HTTP 429). After a
   minute they can try again.
5. Short link: `/c/K7MZQ2` (the code in the path) does the same lookup and redirects straight
   to the download page, so a link can be read out or typed: `<host>/c/K7MZQ2`.

## Staff

### 1. Opening the dashboard

1. Open `http://<hub tailnet address>:8210/` on a laptop or phone that is on the tailnet.
   Through the public hostname `/` answers 404 on purpose.
2. The page polls `/dash/state` every 2 s. The dot top right says "live" or
   "hub unreachable".
3. The chips on top count boxes online, boxes in a session, and boxes asking for time.

### 2. Box cards

One card per box in the config, plus one per enrolled box, sorted by name, even before a box
has ever called in.

- Name, a green or red dot (online means a heartbeat within the last 15 s), the state badge
  (`idle`, `active`, `ending`, `resetting`, or `offline`).
- A big `mm:ss` time left. It counts down every second between polls; amber under 5 minutes,
  red at zero.
- The attendee name, the email and the Telegram handle under it when given, the agent, the
  pickup code and any extra minutes so far.
- A muted line with the programs open on the box, like "Terminal · OpenCode · Chromium". It
  comes from the heartbeat (`activity`), names only, from the box's allow list. Empty when
  the box is idle.
- **+5 / +10 / +15**: `POST /dash/boxes/{box}/extend`. A toast confirms; the box applies it
  on its next heartbeat (within about 3 s).
- **End**: asks "End the session on <box> now?", then `POST /dash/boxes/{box}/end`.
- Buttons are disabled while the box is offline. A command the box does not pick up within
  2 minutes is dropped, so it never hits the next attendee.
- While the box shows the time-up screen (`ending`), the extend and End buttons give way to
  **Finish (end the time-up screen)**. It asks for confirmation, then
  `POST /dash/boxes/{box}/finish` queues a `done` command, which works like the attendee
  pressing Done: the box resets for the next person. In any other state the hub answers 409.
- An offline box that is enrolled or has called in before shows **Release name** (and its MAC
  when enrolled). See "A dead box" below.
- The dashboard refuses any address that acts as a box (403, "The dashboard is not available
  from a hack box."), so a box on the tailnet cannot open it.

### 3. Extend requests

1. A box whose attendee asked for time gets an amber outline and a highlighted block,
   "Asking for more time", with how long ago.
2. **Approve +5 / +10 / +15** calls `POST /dash/requests/{id}` with `approve: true`; this
   queues an extend. **Decline** sends `approve: false`.
3. The block disappears on the next poll. The attendee's screen shows the answer within a few
   seconds.
4. A request for a session that has already moved on cannot be approved (the toast says so).

### 4. History

Below the cards, the last 200 sessions, newest first:

| Column | Shows |
|---|---|
| Started | date and time |
| Box, Name, Agent, Code | as registered; the email and Telegram handle show under the name when given |
| Duration | real time used once ended ("running" before), then planned minutes and extensions |
| End reason | as the box reported it |
| Size | the stored tarball |
| GitHub | `disabled`, `queued`, `pushed` (links to the private repo), `failed` (hover for the error; retried every 5 minutes) |
| Files | **zip**: `/dash/sessions/{id}/download`, the same zip the attendee gets, also after the attendee link expired; `empty` or `-` otherwise |

### 5. Agent keys and the agent choice

The "Agent keys" panel sits between the box cards and the history. One row for the default
(all boxes), then one row per box (an override).

1. Each row has a password field for the **Anthropic API key** and the **0G router key**, and
   checkboxes for the offered agents (`claude`, `claude-0g`, `opencode`).
2. The fields are write-only. They never show a value, only a placeholder: "set, updated
   hh:mm", "not set", or on a box row "inherited from default".
3. Type a key and/or change the agent ticks, then **Save**: `POST /dash/config` with the scope
   and only what changed. Empty fields are left as they are. A key must be 8 to 512 printable
   characters without spaces; at least one agent must stay ticked. Errors show as a toast.
4. **Clear** next to a key, or **Clear** / **Use default** under the agents, asks for
   confirmation and removes that value at that scope. On a box row the default applies again.
5. Each box card shows a small badge: "keys up to date" (the box reported the version the hub
   wants) or "keys pending, applies at next idle". No badge when nothing is set anywhere.

What the box does with it:

1. Every heartbeat reply carries `config_version`, the hash of that box's effective config.
2. When it differs from what the box last applied and the box is **idle**, the agent fetches
   `GET /api/v1/config`, writes each secret with `hackbox secret set <name>`, runs
   `hackbox agents set <agents...>` when given, then `hackbox reset` so the welcome screen
   shows the new choice. Never during a session; an active box picks it up after the reset.
3. The next heartbeat reports `applied_config_version`; the badge turns to "keys up to date".
4. A name the hub does not have is left alone on the box.

Per-box keys with spending caps are the safer choice: an attendee can read the key on their
box.

### 6. One stick for many boxes

1. Staff build one USB stick with `HUB_URL` and the fleet `HUB_ENROLL_TOKEN`, no hostname.
2. Each box installs as `hackbox-<last 4 of its MAC>`. On first boot, right after it joins
   the tailnet, it calls `POST /api/v1/enroll` with its MAC.
3. The hub answers with a name and the box's own token: the lowest free `hackboxN`, or the
   same name as before if this MAC enrolled already (a reinstall keeps its number).
4. The box renames itself (hostname and tailnet name), writes `hub.conf` with its own token
   and finishes provisioning under the new name.
5. A new card appears on the dashboard, and a new row in "Agent keys".

### 7. A dead box

1. A box broke, or was swapped for another machine. Its card shows offline.
2. Wait until it has been silent for 5 minutes, then press **Release name** on the card and
   confirm.
3. `POST /dash/boxes/{box}/release` removes the MAC binding, the enrolled token and the box's
   heartbeat record. If the box is still sending heartbeats the hub answers 409 and nothing
   changes.
4. The card disappears (a box from `config.json` stays, as never seen). The next new box that
   enrolls gets the freed name. Agent key overrides for that name stay and apply to the new box.

### 8. API test page

`/dash/api_test.html` (tailnet only) exercises every box and dashboard call against the hub
with a bearer token field, plus enrollment. Useful to fake a box before the real agent runs.
A successful box call from your laptop locks your laptop out of the dashboard for 24 hours
(the box address guard); run box calls from the hub host (localhost is exempt).
