#!/usr/bin/env bash
# Remove desktop apps nobody at a hack station will open. Only touches
# packages that are actually installed, so it is safe to rerun.
set -euo pipefail

PATTERNS=(
  'libreoffice*'
  thunderbird
  hypnotix
  rhythmbox
  celluloid
  pix
  drawing
  warpinator
  webapp-manager
  simple-scan
  gnome-calendar
  sticky
  transmission-gtk
  onboard
)

installed=()
for p in "${PATTERNS[@]}"; do
  while read -r pkg; do
    [ -n "$pkg" ] && installed+=("$pkg")
  done < <(dpkg-query -W -f='${Package}\n' "$p" 2>/dev/null || true)
done

if [ ${#installed[@]} -gt 0 ]; then
  sudo apt-get purge -y -qq "${installed[@]}"
fi
sudo apt-get autoremove -y -qq --purge
