#!/usr/bin/env bash
# Dev toolchain: chromium (Mint's own deb, not a snap), Node 22 system-wide via NodeSource,
# git, rsync and the GitHub CLI, and the two coding agents installed system-wide so they
# survive the attendee's home wipe and the attendee cannot modify them:
#   Claude Code  Anthropic's signed apt repository (/usr/bin/claude), package held
#   OpenCode     pinned release binary in /usr/local/bin (OPENCODE_VERSION in 20-ai.conf)
# Neither updates itself: apt installs never self-update, the OpenCode upgrader skips a
# binary it did not install, and the managed configs (setup/65-ai.sh) switch updates off.
# To move Claude Code to a newer build: CLAUDE_CODE_UPGRADE=1 bash setup/30-tools.sh
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

HERE="$(cd "$(dirname "$0")/.." && pwd)"

# Agent settings: the repo's defaults, then any box overrides already in conf.d (the
# installed 20-ai.conf is skipped so a newer repo default wins over a stale copy).
. "$HERE/files/conf/20-ai.conf"
for f in /etc/hackbox/conf.d/*.conf; do
  if [ -r "$f" ] && [ "$(basename "$f")" != 20-ai.conf ]; then . "$f"; fi
done

sudo apt-get install -y -qq chromium htop sysstat build-essential scrot xdotool \
  git rsync curl gnupg ca-certificates

# Chromium must never block a session on a dialog. Mint's /usr/bin/chromium
# honours $CHROMIUM_FLAGS, and /etc/environment reaches every login path.
#   --password-store=basic   no keyring prompt (autologin leaves it locked)
#   --no-first-run           no terms-of-service dialog
if ! grep -q '^CHROMIUM_FLAGS=' /etc/environment; then
  echo 'CHROMIUM_FLAGS="--password-store=basic --no-first-run --no-default-browser-check"' | sudo tee -a /etc/environment >/dev/null
fi
sudo mkdir -p /etc/chromium/policies/managed
sudo tee /etc/chromium/policies/managed/hack-box.json >/dev/null <<'JSON'
{
  "BrowserSignin": 0,
  "DefaultBrowserSettingEnabled": false,
  "PasswordManagerEnabled": false,
  "MetricsReportingEnabled": false,
  "BackgroundModeEnabled": false
}
JSON

if ! command -v node >/dev/null || [ "$(node -v | cut -d. -f1)" != v22 ]; then
  curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
  sudo apt-get install -y -qq nodejs
fi

# GitHub CLI: Ubuntu noble ships gh in universe; fall back to GitHub's own apt repository
# (commands from cli/cli docs/install_linux.md) if this mirror has no candidate.
if ! command -v gh >/dev/null; then
  # Output captured first: `| grep -q` under pipefail can fail on SIGPIPE.
  gh_candidate=$(apt-cache policy gh 2>/dev/null | awk '/Candidate:/ {print $2}')
  if [ -n "$gh_candidate" ] && [ "$gh_candidate" != "(none)" ]; then
    sudo apt-get install -y -qq gh
  else
    sudo install -d -m 0755 /etc/apt/keyrings
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null
    sudo chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      | sudo tee /etc/apt/sources.list.d/github-cli.list >/dev/null
    sudo apt-get update -qq
    sudo apt-get install -y -qq gh
  fi
fi

# Claude Code from Anthropic's apt repository (code.claude.com/docs/en/setup, "Install with
# Linux package managers"). The signing key's fingerprint is checked before apt trusts it.
CLAUDE_KEY_FPR=31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE
channel=${CLAUDE_CODE_CHANNEL:-stable}
case "$channel" in stable|latest) ;; *) echo "CLAUDE_CODE_CHANNEL must be stable or latest" >&2; exit 1 ;; esac
sudo install -d -m 0755 /etc/apt/keyrings
key_tmp=$(mktemp)
curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o "$key_tmp"
fprs=$(gpg --show-keys --with-colons "$key_tmp" 2>/dev/null | awk -F: '$1 == "fpr" {print $10}')
if ! printf '%s\n' "$fprs" | grep -qx "$CLAUDE_KEY_FPR"; then
  rm -f "$key_tmp"
  echo "claude-code signing key fingerprint mismatch, refusing to install" >&2
  exit 1
fi
sudo install -m 0644 "$key_tmp" /etc/apt/keyrings/claude-code.asc
rm -f "$key_tmp"
list="deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/$channel $channel main"
if [ "$(cat /etc/apt/sources.list.d/claude-code.list 2>/dev/null)" != "$list" ]; then
  echo "$list" | sudo tee /etc/apt/sources.list.d/claude-code.list >/dev/null
fi
# CLAUDE_CODE_VERSION is the Debian version, for example 2.1.280-1.
want=claude-code
[ -n "${CLAUDE_CODE_VERSION:-}" ] && want="claude-code=$CLAUDE_CODE_VERSION"
have=$(dpkg-query -W -f='${db:Status-Status} ${Version}' claude-code 2>/dev/null || true)
if [ "${have%% *}" != installed ] || [ "${CLAUDE_CODE_UPGRADE:-0}" = 1 ] \
   || { [ -n "${CLAUDE_CODE_VERSION:-}" ] && [ "${have#* }" != "$CLAUDE_CODE_VERSION" ]; }; then
  sudo apt-mark unhold claude-code >/dev/null 2>&1 || true
  sudo apt-get update -qq
  sudo apt-get install -y -qq --allow-downgrades --allow-change-held-packages "$want"
fi
sudo apt-mark hold claude-code >/dev/null

# OpenCode: pinned release binary. The asset matches the CPU (AVX2 or baseline build); its
# sha256 is checked against the digest GitHub publishes for the release and, for the x64
# asset, against OPENCODE_SHA256 from the conf.
oc_ver=${OPENCODE_VERSION:?OPENCODE_VERSION missing from 20-ai.conf}
oc_have=$(/usr/local/bin/opencode --version 2>/dev/null || true)
if [ "$oc_have" != "$oc_ver" ]; then
  case "$(uname -m)" in
    x86_64)
      if grep -qw avx2 /proc/cpuinfo; then asset=opencode-linux-x64.tar.gz; else asset=opencode-linux-x64-baseline.tar.gz; fi ;;
    aarch64) asset=opencode-linux-arm64.tar.gz ;;
    *) echo "no OpenCode build for $(uname -m)" >&2; exit 1 ;;
  esac
  api="https://api.github.com/repos/anomalyco/opencode/releases/tags/v$oc_ver"
  digest=$(curl -fsSL "$api" | python3 -c '
import json, sys
for a in json.load(sys.stdin).get("assets", []):
    if a.get("name") == sys.argv[1]:
        print((a.get("digest") or "").replace("sha256:", ""))
' "$asset" || true)
  pinned=""
  [ "$asset" = opencode-linux-x64.tar.gz ] && pinned=${OPENCODE_SHA256:-}
  if [ -z "$digest" ] && [ -z "$pinned" ]; then
    echo "no checksum available for $asset v$oc_ver, refusing to install" >&2
    exit 1
  fi
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  curl -fsSL -o "$tmp/$asset" "https://github.com/anomalyco/opencode/releases/download/v$oc_ver/$asset"
  sum=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
  for expect in "$digest" "$pinned"; do
    if [ -n "$expect" ] && [ "$expect" != "$sum" ]; then
      echo "OpenCode $asset checksum mismatch: got $sum, expected $expect" >&2
      exit 1
    fi
  done
  tar -xzf "$tmp/$asset" -C "$tmp" opencode
  sudo install -m 0755 -o root -g root "$tmp/opencode" /usr/local/bin/opencode
  rm -rf "$tmp"
  trap - EXIT
fi
if ! grep -q '^OPENCODE_DISABLE_AUTOUPDATE=' /etc/environment; then
  echo 'OPENCODE_DISABLE_AUTOUPDATE=1' | sudo tee -a /etc/environment >/dev/null
fi

# Earlier versions of this script installed both agents into the admin's home with their
# curl installers. Remove those copies and their PATH lines so the admin runs the same
# system-wide builds the attendee gets.
if [ -L ~/.local/bin/claude ] && [[ "$(readlink ~/.local/bin/claude)" == *"/.local/share/claude/"* ]]; then
  rm -f ~/.local/bin/claude
  rm -rf ~/.local/share/claude
fi
rm -rf ~/.opencode/bin
if grep -q '^# hack-box path$' ~/.bashrc 2>/dev/null; then
  sed -i '/^# hack-box path$/{N;d}' ~/.bashrc
fi

echo "claude: $(/usr/bin/claude --version 2>/dev/null || echo missing)"
echo "opencode: $(/usr/local/bin/opencode --version 2>/dev/null || echo missing)"
echo "gh: $(gh --version 2>/dev/null | head -1)"
