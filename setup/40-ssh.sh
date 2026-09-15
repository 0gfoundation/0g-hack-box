#!/usr/bin/env bash
# sshd with key-only auth. Public keys live in keys/*.pub in this repo.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"

sudo apt-get install -y -qq openssh-server

mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
for k in "$HERE"/keys/*.pub; do
  grep -qF "$(cut -d' ' -f2 "$k")" ~/.ssh/authorized_keys || cat "$k" >> ~/.ssh/authorized_keys
done

echo "$USER ALL=(ALL) NOPASSWD: ALL" | sudo tee /etc/sudoers.d/"$USER" >/dev/null
sudo chmod 440 /etc/sudoers.d/"$USER"

sudo tee /etc/ssh/sshd_config.d/hack-box.conf >/dev/null <<'CONF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
CONF

sudo systemctl enable --now ssh
sudo systemctl restart ssh
