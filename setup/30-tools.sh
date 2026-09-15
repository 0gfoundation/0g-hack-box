#!/usr/bin/env bash
# Dev toolchain: chromium (Mint's own deb, not a snap), Node 22 system-wide
# via NodeSource, Claude Code and OpenCode via their native installers.
set -euo pipefail

sudo apt-get install -y -qq chromium htop sysstat build-essential scrot

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

if ! command -v claude >/dev/null && [ ! -x ~/.local/bin/claude ]; then
  curl -fsSL https://claude.ai/install.sh | bash
fi

if ! command -v opencode >/dev/null && [ ! -x ~/.opencode/bin/opencode ]; then
  curl -fsSL https://opencode.ai/install | bash
fi

# Make the per-user installers reachable in non-login shells (ssh commands).
if ! grep -q 'hack-box path' ~/.bashrc; then
  cat >> ~/.bashrc <<'RC'
# hack-box path
export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$PATH"
RC
fi
