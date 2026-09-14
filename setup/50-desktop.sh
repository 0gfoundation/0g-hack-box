#!/usr/bin/env bash
# Desktop appearance and session behaviour: what the Welcome screen's "Dark"
# switch sets, and nothing that can interrupt a timed session (screensaver,
# lock, display sleep, notification popups). Per-user, so it runs as the
# user. Works over ssh too by pointing at the user's session bus.
set -euo pipefail

export DBUS_SESSION_BUS_ADDRESS=${DBUS_SESSION_BUS_ADDRESS:-unix:path=/run/user/$(id -u)/bus}

# A missing key must not abort bootstrap. Warn and carry on.
gs() { gsettings set "$@" 2>/dev/null || echo "warn: gsettings set $*"; }

THEME=Mint-Y-Dark-Aqua
gs org.cinnamon.desktop.interface gtk-theme "$THEME"
gs org.cinnamon.theme name "$THEME"
gs org.cinnamon.desktop.wm.preferences theme "$THEME"
gs org.cinnamon.desktop.interface icon-theme Mint-Y-Aqua
gs org.x.apps.portal color-scheme prefer-dark

# Screensaver and lock off. Sessions are short and the box is attended.
gs org.cinnamon.desktop.screensaver lock-enabled false
gs org.cinnamon.desktop.screensaver idle-activation-enabled false
gs org.cinnamon.desktop.session idle-delay 0

# Display never dims or sleeps, machine never suspends on idle.
gs org.cinnamon.settings-daemon.plugins.power sleep-display-ac 0
gs org.cinnamon.settings-daemon.plugins.power sleep-inactive-ac-timeout 0
gs org.cinnamon.settings-daemon.plugins.power idle-dim-time 0

# No notification popups over someone's editor.
gs org.cinnamon.desktop.notifications display-notifications false

# No input method framework. ibus is ~250 MB across four processes and only
# needed for CJK and similar input. Takes effect at next login.
im-config -n none >/dev/null 2>&1 || true
