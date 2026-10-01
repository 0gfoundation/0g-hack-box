# AI agents on a hack-box

Owner: AI+LOCKDOWN. Installed by `setup/30-tools.sh` (binaries) and `setup/65-ai.sh` (config).

## Put keys on a box

Run as the admin user, on the box (over ssh is fine). Values are read from stdin or a
hidden prompt and never printed.

    hackbox secret set anthropic-api-key      # CLAUDE_AUTH=apikey (default)
    hackbox secret set 0g-router-key          # opencode and claude-0g
    hackbox secret set claude-gateway-token   # CLAUDE_AUTH=gateway
    hackbox secret set claude-oauth-token     # CLAUDE_AUTH=subscription
    printf '%s' "$KEY" | hackbox secret set 0g-router-key    # from a pipe
    hackbox secret list
    hackbox secret rm claude-oauth-token

Use one key per box, created with a server-side cap: a Console workspace spend limit for
Anthropic keys, a per-key budget on the gateway, `credit_limit` + `reset_period` +
`allowed_models` on 0G Router keys. The attendee can read any key the agent uses (both run
as the attendee), so treat every key on a box as visible to whoever sits there.

## Choose the agents

    hackbox agents set claude opencode      # default: Claude Code on Anthropic + OpenCode on 0G
    hackbox agents set claude-0g opencode   # both harnesses on 0G Compute
    hackbox agents set claude-0g            # Claude Code on 0G only
    hackbox agents set opencode             # OpenCode only
    hackbox agents set default              # back to AGENTS_DEFAULT
    hackbox agents show                     # what is offered, and ready / missing secret

`claude` and `claude-0g` cannot be offered together (one box-wide Claude Code policy).
Changes apply at the next reset (`hackbox reset` to apply now).

## Settings and the lab stand-in

Settings live in `/etc/hackbox/conf.d/20-ai.conf` (from the repo, refreshed on every
bootstrap). Put box or lab overrides in `/etc/hackbox/conf.d/29-ai-local.conf`, then run
`hackbox agents set ...` again. Example for pointing everything at a stand-in gateway:

    ANTHROPIC_BASE_URL_OVERRIDE=http://10.0.2.2:4000   # claude (apikey / subscription)
    OG_ROUTER_URL=http://10.0.2.2:4000                 # opencode (/v1) and claude-0g (/v1/messages)
    CLAUDE_AUTH=gateway                                # or keep apikey
    CLAUDE_GATEWAY_URL=http://10.0.2.2:4000

Other keys: `CLAUDE_MODELS`, `CLAUDE_DEFAULT_MODEL`, `CLAUDE_MAX_EFFORT`, `OG_MODEL`,
`OG_SMALL_MODEL`, `OG_FALLBACK_MODEL`, `OG_CONTEXT_TOKENS`, `OPENCODE_VERSION`.
Note: the attendee firewall rejects private addresses for uid 2000, so a stand-in on a
private address also needs `NFT_ALLOW4="10.0.2.2"` in `39-lockdown-local.conf`.

## Claude Code auth routes (`CLAUDE_AUTH`)

- `apikey` (default): organiser Console key, read by the managed `apiKeyHelper`.
- `gateway`: `ANTHROPIC_BASE_URL=CLAUDE_GATEWAY_URL`; the per-box gateway token is also
  delivered by `apiKeyHelper` (sent as `Authorization: Bearer` and `x-api-key`), so it
  never sits in the world-readable managed settings file.
- `subscription`: `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, exported by
  `hackbox-agent` at launch. **Anthropic's Consumer Terms do not allow sharing a
  subscription account with other people, and the Claude Code legal page limits
  subscription OAuth to the purchaser's own ordinary use. Using one subscription on public
  kiosks is outside those terms. This route exists because the organisers asked for it;
  whether to use it is their decision.**

## Smoke test on a box

    sudo -iu hacker bash -lc 'cd ~/project && hackbox-agent claude -p "say ok"'
    sudo -iu hacker bash -lc 'cd ~/project && opencode run "say ok"'

The admin account cannot read the key files (group `hacker` only), so test as `hacker`.
