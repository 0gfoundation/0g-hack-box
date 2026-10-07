# Keys and secrets

What keys a box needs, where to get them, how to put them on, and what an attendee can see.
Nothing in this repo is secret; keys only ever live in `/etc/hackbox/secrets/` on a box.

## The short version

1. Create **one key per box** with a spend cap enforced by the provider.
2. Put it on the box: `ssh -t hackadmin@hackbox1 hackbox secret set 0g-router-key`.
3. Choose the agents: `hackbox agents set claude opencode` (or another combination).
4. Check: `hackbox agents show` says `ready`, then run the smoke test below.
5. After the event: revoke every key at the provider.

**The attendee can read the key their agent uses.** The agent runs as the attendee, so
anything the agent can read, the attendee can read. The control is the server-side limit on
that key (spend cap, model list, expiry), not secrecy. Treat every key on a box as visible to
whoever sits there.

## Which keys exist

| Secret name | Used by | When | Created at |
|---|---|---|---|
| `0g-router-key` | `opencode`, `claude-0g`, and the attendee's own code (`ZG_ROUTER_API_KEY`) | whenever `opencode` or `claude-0g` is offered | 0G Compute Router, pc.0g.ai |
| `anthropic-api-key` | `claude` | `CLAUDE_AUTH=apikey` (the default) | Claude Console, organiser account |
| `claude-gateway-token` | `claude` | `CLAUDE_AUTH=gateway` | your LLM gateway (for example LiteLLM) |
| `claude-oauth-token` | `claude` | `CLAUDE_AUTH=subscription` | `claude setup-token` on a subscriber's machine |

The three agent ids:

| Agent id | Welcome button, and the note under it | Model backend | Key |
|---|---|---|---|
| `claude` | Start with Claude Code, "Anthropic" | Anthropic, Sonnet by default, Sonnet and Haiku allowed | per `CLAUDE_AUTH` |
| `claude-0g` | Start with Claude Code, "0G Compute (glm-5.3)" | 0G Router, Anthropic-compatible path, `glm-5.3` | `0g-router-key` |
| `opencode` | Start with OpenCode, "0G Compute" | 0G Router, OpenAI-compatible path, `glm-5.3` | `0g-router-key` |

`claude` and `claude-0g` cannot be offered at the same time (one box-wide Claude Code
policy). The default is `claude opencode`.

## Why one key per box

- A key read off one box can be revoked without touching the other two.
- Each key's cap bounds what one box, or one person who copied its key, can spend.
- Usage per box shows up separately at the provider.

## Creating the keys

### 0G Compute Router key (pc.0g.ai)

1. At pc.0g.ai connect the organisers' wallet and deposit 0G to the Payment Layer
   (Dashboard > Deposit). Old deposits on compute-marketplace.0g.ai do not back Router calls.
2. Dashboard > API Keys: create one `sk-` key per box, named after the box (`hackbox1`), with:
   - `allowed_models`: `glm-5.3`, `kimi-k2.7-code`, `0gm-1.0-35b-a3b` (these are `OG_MODEL`,
     `OG_FALLBACK_MODEL` and `OG_SMALL_MODEL` in `files/conf/20-ai.conf`; OpenCode uses the
     small one for titles and `claude-0g` for Claude Code's background calls, so allow all
     three);
   - `credit_limit` with `reset_period` `daily`;
   - an `expiration` shortly after the event.
3. The key is shown once. Put it on the box straight away (below).

The same can be scripted with a management (`mk-`) key; keep that key off the boxes:

    curl -s https://router-api.0g.ai/v1/api-keys \
      -H "Authorization: Bearer $ZG_MGMT_KEY" -H "Content-Type: application/json" \
      -d '{"name":"hackbox1","allowed_models":["glm-5.3","kimi-k2.7-code","0gm-1.0-35b-a3b"],
           "credit_limit":"40","reset_period":"daily","expiration":"2026-10-31T00:00:00Z"}'

The response carries the secret once (`key`) and a `key_id` for revoking.

Sizing (estimate, not measured): one 30 minute slot on `glm-5.3` is roughly USD 3 to 6, about
9 to 18 0G; 16 slots a day is 150 to 300 0G per box. The Router only starts pulling from the
Payment Layer after the account's first successful call, so run the smoke test before the
event.

### Anthropic key (Claude Console)

1. In the organisers' Claude Console account, create a workspace for the event and set its
   spend limit.
2. Create one API key per box in that workspace, named after the box.
3. Budget: the allowlist keeps attendees on Sonnet and Haiku (`CLAUDE_MODELS` in
   `20-ai.conf`); there is no per-session dollar cap for interactive use, so the workspace
   limit is the backstop.

### Gateway token

Create one virtual key per box on your gateway, each with its own budget. Set the gateway in
`/etc/hackbox/conf.d/29-ai-local.conf`:

    CLAUDE_AUTH=gateway
    CLAUDE_GATEWAY_URL=https://gateway.example

The token reaches Claude Code through the managed `apiKeyHelper`, which sends it as both
`Authorization: Bearer` and `x-api-key`, so the gateway must accept a bearer token (LiteLLM
does). A gateway on a private address also needs `NFT_ALLOW4="<its IP>"` in
`39-lockdown-local.conf` and a rerun of `setup/80-lockdown.sh`, because the attendee firewall
rejects private ranges.

## Claude Code auth routes (`CLAUDE_AUTH`)

Set in `/etc/hackbox/conf.d/29-ai-local.conf`, then run `hackbox agents set claude ...` again.

| Route | Credential | How it reaches Claude Code |
|---|---|---|
| `apikey` (default) | organiser Console key, `anthropic-api-key` | managed `apiKeyHelper` |
| `gateway` | per-box gateway token, `claude-gateway-token` | managed `apiKeyHelper`, `ANTHROPIC_BASE_URL=CLAUDE_GATEWAY_URL` |
| `subscription` | `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, `claude-oauth-token` | exported by `hackbox-agent` at launch |

On the subscription route only the welcome button (or `hackbox-agent claude`) has the
credential; a plain `claude` typed in another terminal shows a login screen, and `/login` is
hidden.

**Subscription terms.** Anthropic's Consumer Terms, section 2, say you may not share your
account credentials with anyone else or make your account available to anyone else
(https://www.anthropic.com/legal/consumer-terms). The Claude Code legal and compliance page
says subscription OAuth is meant for the purchaser's own ordinary use, and that Pro and Max
usage limits assume ordinary, individual usage
(https://code.claude.com/docs/en/legal-and-compliance). Running one subscription on public
kiosks for many people is outside those terms. The route exists because the organisers asked
for it; whether to use it is the organisers' decision. The same page allows an organiser's own
API key in a machine image for its own authorised users, which is what `apikey` does. Also note
that all boxes on one subscription share its five-hour and weekly limits.

## Putting keys on a box

Run as the admin user, on the box or over ssh. The value is read from a hidden prompt or from
stdin, is never printed, and never appears on a command line.

    ssh -t hackadmin@hackbox1 hackbox secret set 0g-router-key         # hidden prompt
    ssh hackadmin@hackbox1 hackbox secret set 0g-router-key < hackbox1-0g.key   # from a file
    ssh hackadmin@hackbox1 hackbox secret list

    saved 0g-router-key (<n> characters). Takes effect at the next reset.

`hackbox secret list` shows names, presence, age and mode only:

    NAME                   STATE    AGE        OWNER/MODE
    anthropic-api-key      missing  -          -
    claude-gateway-token   missing  -          -
    claude-oauth-token     missing  -          -
    0g-router-key          present  10m        root:hacker 640

Remove a key: `hackbox secret rm <name>`. Remove any key the box does not currently offer: an
attendee can read every file in `/etc/hackbox/secrets/`, used or not.

## Choosing the agents

    hackbox agents set claude opencode      # default: Claude Code on Anthropic + OpenCode on 0G
    hackbox agents set claude-0g opencode   # everything on 0G Compute
    hackbox agents set claude-0g            # Claude Code on 0G only
    hackbox agents set opencode             # OpenCode only
    hackbox agents set default              # back to AGENTS_DEFAULT
    hackbox agents show

`agents set` rewrites the managed configs and the welcome screen's manifest; no reinstall. The
welcome screen reads it when the attendee desktop starts, so the change shows after the next
reset. On an idle box, apply it now with `hackbox reset`.

Sample `hackbox agents show` (from the lab, which points the Router URL at a stand-in):

    agents: claude-0g opencode
    claude auth: apikey   0G router: https://<router url>
      claude     off      missing secret: anthropic-api-key
      claude-0g  offered  ready
      opencode   offered  ready
    takes effect at the next reset (a running agent keeps its settings)

## Smoke test each agent

The admin account cannot read the key files (they are group `hacker`), so test as the
attendee. On an idle box, over ssh:

    sudo -iu hacker bash -lc 'cd ~/project && hackbox-agent claude -p "Reply with exactly: OK" </dev/null'
    sudo -iu hacker bash -lc 'cd ~/project && hackbox-agent claude-0g -p "Reply with exactly: OK" </dev/null'
    sudo -iu hacker bash -lc 'cd ~/project && hackbox-agent opencode run "Reply with exactly: OK" </dev/null'

Only the agents that are offered will run; the others print "This station does not offer
...". Each should print `OK`. A missing key prints "This station has no key for ... yet.
Please ask a staff member." The test suite does the same for every offered agent:
`bash /opt/hack-box/tests/run.sh --live agents`.

## Rotating or revoking mid-event

1. Create the new key at the provider.
2. `ssh -t hackadmin@hackboxN hackbox secret set <name>`.
3. Revoke the old key at the provider (pc.0g.ai API Keys, or
   `curl -X DELETE https://router-api.0g.ai/v1/api-keys/<key_id> -H "Authorization: Bearer $ZG_MGMT_KEY"`;
   for Anthropic, disable the key in the Console).

When the new key is used:

| Agent | Picks up the new key |
|---|---|
| `claude`, `claude-0g` | at the next launch; a running Claude Code re-runs its key helper within five minutes or after a 401 |
| `opencode` | at the next launch (the key is exported when the agent starts) |
| attendee's own code (`ZG_ROUTER_API_KEY`) | at the next reset (written into the fresh home) |

To cut a box off at once: revoke at the provider, `hackbox secret rm <name>`, `hackbox reset`.
A `claude-oauth-token` has no documented revoke step in Anthropic's docs; community reports
point at the claude.ai Claude Code settings page. Another reason to prefer `apikey`.

## What an attendee can and cannot see

| Can | Cannot |
|---|---|
| read every file in `/etc/hackbox/secrets/` (root:hacker 0640 in a root:hacker 0750 directory) | read the admin's home, the archives in `/var/lib/hackbox/archive`, or other boxes' keys |
| run the key helper `/usr/local/lib/hackbox/ai/key` | change `/etc/claude-code/managed-settings.json` or `/etc/opencode/opencode.json` (root-owned) |
| see `ZG_ROUTER_API_KEY` in their own shell when `opencode` or `claude-0g` is offered | pick models outside the Claude allowlist, use bypass-permissions mode, add MCP servers or plugins, buy extra usage (all set in the managed policy) |
| copy a key off the box on a USB stick | spend more than the key's server-side cap, or use it after it is revoked or expired |

The managed config files are world-readable but hold no secrets. Claude Code's deny rules for
the secrets directory are a speed bump for the agent, not a wall.

When the session ends the home is wiped, including `~/.config/hackbox/env` and any agent
state; the key files in `/etc/hackbox/secrets/` stay for the next attendee.

## End of the event

1. Revoke every per-box key at pc.0g.ai, in the Claude Console and on the gateway.
2. `hackbox secret rm` each name on each box, or wipe the boxes.
3. Check the provider dashboards for spend per key.
