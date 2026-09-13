#!/usr/bin/env bash
# Desktop appearance: what the Welcome screen's "Dark" switch sets, plus a
# few things a hack station doesn't need. Per-user, so it runs as the user.
# Works over ssh too by pointing at the user's session bus.
set -euo pipefail

export DBUS_SESSION_BUS_ADDRESS=${DBUS_SESSION_BUS_ADDRESS:-unix:path=/run/user/$(id -u)/bus}

THEME=Mint-Y-Dark-Aqua
gsettings set org.cinnamon.desktop.interface gtk-theme "$THEME"
gsettings set org.cinnamon.theme name "$THEME"
gsettings set org.cinnamon.desktop.wm.preferences theme "$THEME"
gsettings set org.cinnamon.desktop.interface icon-theme Mint-Y-Aqua
gsettings set org.x.apps.portal color-scheme prefer-dark

# No screensaver or lock: sessions are short and the box is attended.
gsettings set org.cinnamon.desktop.screensaver lock-enabled false
gsettings set org.cinnamon.desktop.session idle-delay 0
gsettings set org.cinnamon.settings-daemon.plugins.power sleep-display-ac 0
gsettings set org.cinnamon.settings-daemon.plugins.power sleep-inactive-ac-timeout 0
