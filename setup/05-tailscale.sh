#!/usr/bin/env bash
# Install Tailscale and bring it up. `tailscale up` prints a login URL the
# first time. Open it in the box's browser to add the machine to the tailnet.
set -euo pipefail

if ! command -v tailscale >/dev/null; then
  curl -fsSL https://tailscale.com/install.sh | sh
fi

if ! tailscale status >/dev/null 2>&1; then
  sudo tailscale up --hostname "$(hostname)"
fi

echo "tailscale ip: $(tailscale ip -4)"
