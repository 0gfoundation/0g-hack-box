#!/usr/bin/env bash
# AI agent configuration (SPEC section 4). The binaries come from 30-tools.sh; this installs
# what decides which agents a box offers and how they authenticate, all outside the
# attendee's wiped home:
#   /etc/hackbox/conf.d/20-ai.conf        settings (overrides go in 29-ai-local.conf)
#   /etc/hackbox/secrets/                 keys, root:hacker 0750, key files 0640
#   /usr/local/sbin/hackbox-ai-render     writes the managed configs and the manifest
#   /usr/local/lib/hackbox/ai/key         Claude Code apiKeyHelper
#   /usr/local/bin/hackbox-agent          the manifest's launch command
#   /usr/local/bin/0g-fund                testnet 0G for a wallet, through the session hub
#   /usr/local/bin/0g-mock-api            mock prices / markets / AI marketplace API plus a live
#                                         0G Compute AI relay, hackbox-mock-api.service on :4010
#   /etc/hackbox/ai/starters.json         welcome-screen idea starters (OpenCode first prompts)
#   /etc/hackbox/reset.d/50-ai-seed       seeds agent state into every fresh home
#   /opt/0g-agent-skills                  the 0G agent skills, which every fresh home's
#                                         ~/.claude/CLAUDE.md and opencode AGENTS.md point at
# then renders the configs for the saved selection (first run: AGENTS_DEFAULT).
# Keys are set separately: `hackbox secret set <name>`. Without them the box still works
# and the welcome screen's agent buttons print a "please ask staff" line.
#
# Residual exposure, stated plainly: whatever the agent can read, the attendee can read,
# because both run as the attendee. Key files are group-readable by the attendee and
# nobody else; the real limits are per-box keys with server-side caps.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
A="$HERE/files/ai"

sudo install -d -m 0755 /etc/hackbox /etc/hackbox/conf.d /etc/hackbox/reset.d /etc/hackbox/ai
sudo install -m 0644 "$HERE/files/conf/20-ai.conf" /etc/hackbox/conf.d/20-ai.conf
for f in /etc/hackbox/conf.d/*.conf; do [ -r "$f" ] && . "$f"; done
HACKER_USER=${HACKER_USER:-hacker}
if ! getent group "$HACKER_USER" >/dev/null; then
  echo "group $HACKER_USER does not exist yet: run setup/60-hacker.sh first" >&2
  exit 1
fi

# Secrets: the directory is traversable and listable by the attendee group (SPEC A1) so
# helpers running as the attendee can open the agent-facing key files (root:hacker 0640).
# Anything else in there stays root:root 0600.
sudo install -d -m 0750 -o root -g "$HACKER_USER" /etc/hackbox/secrets
for name in anthropic-api-key claude-gateway-token claude-oauth-token 0g-router-key; do
  f=/etc/hackbox/secrets/$name
  if sudo test -f "$f"; then
    sudo chown "root:$HACKER_USER" "$f"
    sudo chmod 0640 "$f"
  fi
done

# Helpers.
sudo install -d -m 0755 /usr/local/lib/hackbox /usr/local/lib/hackbox/ai /etc/claude-code /etc/opencode
sudo install -m 0755 "$A/hackbox-ai-render" /usr/local/sbin/hackbox-ai-render
sudo install -m 0755 "$A/key" /usr/local/lib/hackbox/ai/key
sudo install -m 0755 "$A/hackbox-agent" /usr/local/bin/hackbox-agent
sudo install -m 0755 "$A/0g-fund" /usr/local/bin/0g-fund
sudo install -m 0755 "$A/0g-mock-api" /usr/local/bin/0g-mock-api
sudo install -m 0644 "$A/starters.json" /etc/hackbox/ai/starters.json
sudo install -m 0644 "$A/hackbox-mock-api.service" /etc/systemd/system/hackbox-mock-api.service
sudo systemctl daemon-reload
sudo systemctl enable --now hackbox-mock-api.service
sudo install -m 0755 "$A/50-ai-seed" /etc/hackbox/reset.d/50-ai-seed
sudo install -m 0755 "$A/hackbox-skills-update" /usr/local/sbin/hackbox-skills-update
# 0G agent skills (refresh later with `hackbox skills update`). A network hiccup here must
# not fail the install: the agents then simply have no 0G pointers until the next update.
if [ ! -d /opt/0g-agent-skills ] || [ -n "${HACKBOX_SKILLS_REFRESH:-}" ]; then
  sudo /usr/local/sbin/hackbox-skills-update || echo "warn: could not fetch the 0G agent skills; run: hackbox skills update" >&2
fi
chmod 0755 "$HERE/lib/hackbox.d/agents" "$HERE/lib/hackbox.d/secret" "$HERE/lib/hackbox.d/lock" \
  "$HERE/lib/hackbox.d/skills" 2>/dev/null || true

# Render for the saved selection, or the default on a fresh box. A rerun of bootstrap
# keeps whatever staff chose with `hackbox agents set`.
if [ -s /etc/hackbox/agents.selected ]; then
  read -r -a selection < /etc/hackbox/agents.selected
else
  read -r -a selection <<< "${AGENTS_DEFAULT:-claude opencode}"
fi
bash "$HERE/lib/hackbox.d/agents" set "${selection[@]}"
