#!/usr/bin/env bash
# Stop anything that costs RAM, wakes the CPU or interrupts a session:
# print/modem/bluetooth daemons, suspend, periodic apt runs, crash reporting,
# and the tray apps Mint starts at login.
# Set KEEP_BLUETOOTH=1 if the box uses a BT keyboard/mouse.
set -euo pipefail

UNITS=(cups cups-browsed ModemManager avahi-daemon apport whoopsie)
[ "${KEEP_BLUETOOTH:-0}" = 1 ] || UNITS+=(bluetooth)

for u in "${UNITS[@]}"; do
  if systemctl list-unit-files "$u.service" >/dev/null 2>&1; then
    sudo systemctl disable --now "$u.service" 2>/dev/null || true
    # socket units restart cups on demand
    sudo systemctl disable --now "$u.socket" 2>/dev/null || true
  fi
done

# Ubuntu's daily apt update/upgrade timers: CPU spikes and dpkg locks at
# random times. 15-upgrade.sh is the only place upgrades happen.
for t in apt-daily apt-daily-upgrade; do
  sudo systemctl disable --now "$t.timer" 2>/dev/null || true
done

# No suspend or hibernate, ever.
sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target 2>/dev/null || true

# Crash reporter stays off even if the package is reinstalled.
if [ -f /etc/default/apport ]; then
  sudo sed -i 's/^enabled=.*/enabled=0/' /etc/default/apport
fi

# Tray apps that autostart via /etc/xdg/autostart. A user-level copy with
# Hidden=true wins over the system file. It has to be a full copy:
# cinnamon-session rejects a stub with only Hidden=true and falls back.
#   blueman      bluetooth tray
#   mintupdate   update manager tray, nags about updates
#   mintreport   system reports tray
#   mintwelcome  welcome screen on login
mkdir -p ~/.config/autostart
for app in blueman mintupdate mintreport mintwelcome; do
  if [ -f "/etc/xdg/autostart/$app.desktop" ]; then
    { cat "/etc/xdg/autostart/$app.desktop"; echo "Hidden=true"; } > ~/.config/autostart/"$app.desktop"
  fi
done
