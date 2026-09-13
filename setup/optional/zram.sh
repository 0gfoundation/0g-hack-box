#!/usr/bin/env bash
# Compressed swap in RAM. Not run by bootstrap: measure without it first,
# then with, so the effect shows up in results/.
set -euo pipefail
sudo apt-get install -y -qq zram-tools
sudo tee /etc/default/zramswap >/dev/null <<'CONF'
ALGO=zstd
PERCENT=50
CONF
sudo systemctl restart zramswap
zramctl
