#!/usr/bin/env bash
# Stop background services that cost RAM or wake the CPU and serve no purpose
# on a hack station. Set KEEP_BLUETOOTH=1 if the box uses a BT keyboard/mouse.
set -euo pipefail

UNITS=(cups cups-browsed ModemManager avahi-daemon)
[ "${KEEP_BLUETOOTH:-0}" = 1 ] || UNITS+=(bluetooth)

for u in "${UNITS[@]}"; do
  if systemctl list-unit-files "$u.service" >/dev/null 2>&1; then
    sudo systemctl disable --now "$u.service" 2>/dev/null || true
    # socket units restart cups on demand
    sudo systemctl disable --now "$u.socket" 2>/dev/null || true
  fi
done

# Tray apps that autostart via /etc/xdg/autostart. A user-level override
# with Hidden=true wins over the system file.
mkdir -p ~/.config/autostart
for app in blueman; do
  if [ -f "/etc/xdg/autostart/$app.desktop" ]; then
    printf '[Desktop Entry]\nHidden=true\n' > ~/.config/autostart/"$app.desktop"
  fi
done
