# reset (destructive): a full session teardown. Everything an attendee left
# behind is gone, the home is a fresh skeleton, state is idle, the timing is
# within the spec limit, and the desktop returns. Spec 3.3.
#
# Only runs with --destructive. Each check restores the box to idle.

# trigger_reset_and_time: run a reset and echo the seconds until state==idle.
# Echoes -1 if idle was never reached inside the window.
_trigger_reset_and_time() {
  local start now
  start="$(date +%s)"
  # hackbox reset is the admin "immediate" path (spec 3.8). Allow a generous
  # ceiling so a slow box cannot hang the suite; the assertion checks the real
  # elapsed value against the spec limit.
  as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
  if wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]"; then
    now="$(date +%s)"
    echo $(( now - start ))
  else
    echo -1
  fi
}

group_reset() {
  need_linux_or_skip "reset_basic" || return
  need_sudo_or_skip "reset_basic" || return
  have hackbox || command -v hackbox >/dev/null || { fail "reset_basic" "hackbox CLI absent"; return; }

  # ---- 1. Populate a session's worth of debris, then reset. --------------
  as_hacker_t 20 "touch \"\$HOME/$MARK\""
  as_hacker_t 60 "fallocate -l 300M \"\$HOME/big.bin\" 2>/dev/null || dd if=/dev/zero of=\"\$HOME/big.bin\" bs=1M count=300 2>/dev/null" >/dev/null 2>&1
  as_hacker_t 20 "touch /tmp/$MARK /var/tmp/$MARK /dev/shm/$MARK" 2>/dev/null
  # A process holding a file open in the home.
  hold_pid="$(as_hacker_t 15 "nohup sh -c 'exec 9>\"\$HOME/$MARK.open\"; sleep 600' >/dev/null 2>&1 & echo \$!" | tail -1)"
  # A detached busy loop.
  loop_pid="$(as_hacker_t 15 "nohup sh -c 'while :; do sleep 5; done' >/dev/null 2>&1 & echo \$!" | tail -1)"
  # A crontab attempt (should already be denied, but leave the try in the mix).
  as_hacker_t 15 "echo '* * * * * true' | crontab -" >/dev/null 2>&1 || true

  dur="$(_trigger_reset_and_time)"

  # Timing: spec 3.3 says the reset must finish in under 20 seconds on the VM.
  if [ "$dur" -lt 0 ]; then
    fail "reset_completes" "state never returned to idle within 60s after reset"
  elif [ "$dur" -le 20 ]; then
    pass "reset_completes"
    pass "reset_within_time_limit"
  else
    pass "reset_completes"
    fail "reset_within_time_limit" "reset took ${dur}s, spec limit is under 20s"
  fi

  # State is idle.
  if [ "$(read_state)" = idle ]; then
    pass "reset_state_idle"
  else
    fail "reset_state_idle" "state is '$(read_state)' after reset"
  fi

  # Home marker and the big file are gone (fresh skeleton).
  if as_hacker_t 15 "test ! -e \"\$HOME/$MARK\" && test ! -e \"\$HOME/big.bin\""; then
    pass "reset_home_fresh"
  else
    fail "reset_home_fresh" "session files survived in the home after reset"
  fi
  # And the skeleton is back.
  if as_hacker_t 15 'test -d "$HOME/project" && test -f "$HOME/project/CLAUDE.md"'; then
    pass "reset_skeleton_restored"
  else
    fail "reset_skeleton_restored" "~/project skeleton not restored after reset"
  fi

  # tmp-family markers gone.
  gone=1; left=""
  for p in "/tmp/$MARK" "/var/tmp/$MARK" "/dev/shm/$MARK"; do
    if sudo -n test -e "$p" 2>/dev/null; then gone=0; left="$left $p"; fi
  done
  if [ "$gone" -eq 1 ]; then pass "reset_tmpdirs_clean"; else fail "reset_tmpdirs_clean" "survived:$left"; fi

  # Processes gone.
  psurv=""
  for pid in "$hold_pid" "$loop_pid"; do
    [ -n "$pid" ] || continue
    if sudo -n kill -0 "$pid" 2>/dev/null; then psurv="$psurv $pid"; fi
  done
  if [ -z "$psurv" ]; then
    pass "reset_processes_killed"
  else
    fail "reset_processes_killed" "attendee processes survived reset:$psurv"
    for pid in $psurv; do sudo -n kill -KILL "$pid" 2>/dev/null || true; done
  fi
  # No processes owned by hacker other than a freshly relaunched desktop.
  # (Belt: the specific debris pids are the real signal above.)

  # Desktop returns: a cinnamon process owned by hacker within 60s.
  if wait_for 60 "pgrep -u '$HACKER_USER' -x cinnamon >/dev/null 2>&1 || pgrep -u '$HACKER_USER' -f cinnamon-session >/dev/null 2>&1"; then
    pass "reset_desktop_returns"
  else
    fail "reset_desktop_returns" "no cinnamon process for $HACKER_USER within 60s of reset"
  fi

  # ---- 2. Reset with the home image filled to 100 percent. --------------
  as_hacker_t 120 "cat /dev/zero > \"\$HOME/fill.bin\" 2>/dev/null; true" >/dev/null 2>&1
  as_hacker_t 20 "touch \"\$HOME/$MARK.full\"" 2>/dev/null || true
  dur2="$(_trigger_reset_and_time)"
  if [ "$dur2" -ge 0 ] && [ "$(read_state)" = idle ] && as_hacker_t 15 "test ! -e \"\$HOME/fill.bin\""; then
    pass "reset_with_full_home"
  else
    fail "reset_with_full_home" "reset from a full home failed (dur=${dur2}s state=$(read_state))"
  fi

  # ---- 3. Two resets started at once are safe (re-entrant lock). --------
  as_hacker_t 15 "touch \"\$HOME/$MARK.race\"" 2>/dev/null || true
  ( as_admin_t 180 'hackbox reset' >/dev/null 2>&1 ) &
  ( as_admin_t 180 'hackbox reset' >/dev/null 2>&1 ) &
  wait
  if wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" \
       && mountpoint -q "/home/$HACKER_USER" \
       && as_hacker_t 15 "test ! -e \"\$HOME/$MARK.race\" && test -d \"\$HOME/project\""; then
    pass "reset_concurrent_safe"
  else
    fail "reset_concurrent_safe" "box not cleanly idle after two concurrent resets (state=$(read_state))"
  fi

  # ---- 4. A reset hook that exits nonzero does not abort the reset. -----
  if sudo -n test -d /etc/hackbox/reset.d 2>/dev/null; then
    fh="/etc/hackbox/reset.d/89-hbtest-fail"
    sh="/etc/hackbox/reset.d/91-hbtest-sentinel"
    sent="/run/hackbox/$MARK.hooksentinel"
    sudo -n bash -c "printf '#!/bin/sh\nexit 3\n' > '$fh'; chmod 0755 '$fh'" 2>/dev/null
    sudo -n bash -c "printf '#!/bin/sh\ntouch \"$sent\"\n' > '$sh'; chmod 0755 '$sh'" 2>/dev/null
    sudo -n rm -f "$sent" 2>/dev/null || true
    dur4="$(_trigger_reset_and_time)"
    if [ "$dur4" -ge 0 ] && [ "$(read_state)" = idle ] && sudo -n test -e "$sent" 2>/dev/null; then
      pass "reset_hook_failure_nonfatal"
    else
      fail "reset_hook_failure_nonfatal" "a failing hook aborted the reset (dur=${dur4}s state=$(read_state), later hook ran=$(sudo -n test -e "$sent" 2>/dev/null && echo yes || echo no))"
    fi
    sudo -n rm -f "$fh" "$sh" "$sent" 2>/dev/null || true
  else
    skip "reset_hook_failure_nonfatal" "/etc/hackbox/reset.d not present"
  fi

  # Final restore to a clean idle box.
  _trigger_reset_and_time >/dev/null 2>&1 || true
}
