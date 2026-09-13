#!/usr/bin/env bash
# Bring the system fully up to date, non-interactively. The apt periodic
# timers are switched off in 20-services.sh, so this is the only place
# upgrades happen: rerun bootstrap to patch a box.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq
sudo apt-get full-upgrade -y -qq -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold
