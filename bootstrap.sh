#!/usr/bin/env bash
# Entry point for a fresh Linux Mint box. Run as the normal user, not root:
#   curl -fsSL https://raw.githubusercontent.com/0gfoundation/0g-hack-box/main/bootstrap.sh | bash
# Clones (or updates) the repo to /opt/hack-box and runs setup/NN-*.sh in order.
set -euo pipefail

REPO=https://github.com/0gfoundation/0g-hack-box.git
DEST=/opt/hack-box

if [ "$(id -u)" -eq 0 ]; then
  echo "run as your normal user, not root" >&2
  exit 1
fi

sudo apt-get update -qq
sudo apt-get install -y -qq git curl

if [ -d "$DEST/.git" ]; then
  git -C "$DEST" pull -q --ff-only
else
  sudo mkdir -p "$DEST"
  sudo chown "$USER:$USER" "$DEST"
  git clone -q "$REPO" "$DEST"
fi

for script in "$DEST"/setup/[0-9][0-9]-*.sh; do
  echo "==> $(basename "$script")"
  bash "$script"
done

echo "==> done. reboot recommended."
