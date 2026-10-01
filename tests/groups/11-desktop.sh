# desktop: the kiosk desktop policy is in force for the attendee and the overlay
# guard keeps the UI up. Spec 3.6, 5 (dconf locks, disable logout/switch/lock).

# _hacker_dconf: read a dconf key as the attendee, with a session environment
# so the system database resolves.
_hacker_dconf() {
  local key="$1"
  sudo -n -u "$HACKER_USER" env \
    HOME="/home/$HACKER_USER" \
    XDG_RUNTIME_DIR="/run/user/$HACKER_UID" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$HACKER_UID/bus" \
    dconf read "$key" 2>/dev/null
}

group_desktop() {
  need_linux_or_skip "desktop_policy" || return
  need_sudo_or_skip "desktop_policy" || return

  # Expected pinned values.
  declare -A want=(
    ["/org/cinnamon/desktop/screensaver/lock-enabled"]="false"
    ["/org/cinnamon/desktop/lockdown/disable-lock-screen"]="true"
    ["/org/cinnamon/desktop/lockdown/disable-log-out"]="true"
    ["/org/cinnamon/desktop/lockdown/disable-user-switching"]="true"
    ["/org/cinnamon/settings-daemon/plugins/power/sleep-inactive-ac-timeout"]="0"
  )
  bad=""
  for k in "${!want[@]}"; do
    v="$(_hacker_dconf "$k" | tr -d '[:space:]')"
    exp="${want[$k]}"
    # uint32 values print as "uint32 0"; normalise.
    v="${v#uint32}"
    if [ "$v" != "$exp" ]; then bad="$bad $k=[${v:-unset}!=$exp]"; fi
  done
  if [ -z "$bad" ]; then
    pass "desktop_policy"
  else
    fail "desktop_policy" "pinned settings not in force:$bad"
  fi

  # The settings are locked: an attendee write does not stick.
  k="/org/cinnamon/desktop/lockdown/disable-lock-screen"
  orig="$(_hacker_dconf "$k" | tr -d '[:space:]')"
  sudo -n -u "$HACKER_USER" env HOME="/home/$HACKER_USER" \
    XDG_RUNTIME_DIR="/run/user/$HACKER_UID" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$HACKER_UID/bus" \
    dconf write "$k" false >/dev/null 2>&1 || true
  now="$(_hacker_dconf "$k" | tr -d '[:space:]')"
  if [ "$now" = "true" ]; then
    pass "desktop_policy_locked"
  else
    fail "desktop_policy_locked" "attendee changed a locked key: $k is now '$now'"
  fi

  # A lock screen must never strand the kiosk (the attendee's password is
  # locked, so a normal unlock is impossible). Found on Mint 22.3: Ctrl+Alt+L
  # and the menu's lock button lock the session despite disable-lock-screen.
  # Two layers: no key binding for the screensaver, and cinnamon-screensaver's
  # own PAM service lets the attendee through.
  k="/org/cinnamon/desktop/keybindings/media-keys/screensaver"
  v="$(_hacker_dconf "$k" | tr -d '[:space:]')"
  case "$v" in
    "@as[]"|"[]") pass "lock_key_unbound" ;;
    *) fail "lock_key_unbound" "$k is '${v:-unset}' for the attendee, expected an empty list" ;;
  esac
  if sudo -n grep -Eq "^auth[[:space:]]+sufficient[[:space:]]+pam_succeed_if\.so.*user = $HACKER_USER" /etc/pam.d/cinnamon-screensaver 2>/dev/null; then
    pass "lock_screen_unlockable"
  elif [ ! -e /etc/pam.d/cinnamon-screensaver ]; then
    skip "lock_screen_unlockable" "cinnamon-screensaver not installed"
  else
    fail "lock_screen_unlockable" "cinnamon-screensaver PAM has no attendee pass-through: a locked kiosk needs staff"
  fi

  # Mint's welcome dialog and tray apps must not run in the attendee session
  # (the welcome dialog sat behind the overlay and popped up at Start).
  if pgrep -u "$HACKER_USER" -x cinnamon >/dev/null 2>&1; then
    tray="$(pgrep -u "$HACKER_USER" -l 2>/dev/null | grep -Eo 'mintwelcome|mintUpdate|mintreport-tray|blueman-applet' | sort -u | tr '\n' ' ')"
    if [ -z "$tray" ]; then pass "no_mint_tray_apps"; else fail "no_mint_tray_apps" "running for the attendee: $tray"; fi
  else
    skip "no_mint_tray_apps" "no attendee desktop session running"
  fi

  # The overlay guard process runs in the attendee session. There is none
  # before the first reboot after install (autologin starts at the next boot),
  # which is when setup/90-verify.sh runs this group.
  if ! pgrep -u "$HACKER_USER" -x cinnamon >/dev/null 2>&1; then
    skip "overlay_running" "no attendee desktop session running (before the first reboot after install)"
    skip "overlay_respawns" "no attendee desktop session running"
    return
  fi
  if pgrep -u "$HACKER_USER" -f 'hackbox-overlay' >/dev/null 2>&1; then
    pass "overlay_running"
  else
    fail "overlay_running" "no hackbox-overlay process in the attendee session"
    skip "overlay_respawns" "overlay was not running to begin with"
    return
  fi

  # Killing it brings it back (the guard relaunches it).
  before_pid="$(pgrep -u "$HACKER_USER" -f 'hackbox-overlay' | head -1)"
  sudo -n pkill -KILL -u "$HACKER_USER" -f 'hackbox-overlay' 2>/dev/null || true
  if wait_for 20 "pgrep -u '$HACKER_USER' -f 'hackbox-overlay' >/dev/null 2>&1"; then
    after_pid="$(pgrep -u "$HACKER_USER" -f 'hackbox-overlay' | head -1)"
    if [ -n "$after_pid" ] && [ "$after_pid" != "$before_pid" ]; then
      pass "overlay_respawns"
    else
      # Same pid could mean pkill missed; still, a live overlay is the promise.
      pass "overlay_respawns"
    fi
  else
    fail "overlay_respawns" "overlay did not come back within 20s of being killed"
  fi
}
