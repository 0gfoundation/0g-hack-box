# timer (destructive): a real one minute session runs itself down through the
# ending screen to idle with no operator action, and archives work left in the
# project. Spec 3.2, 3.4, 3.5. Only runs with --destructive.

group_timer() {
  need_linux_or_skip "timer_autorun" || return
  need_sudo_or_skip "timer_autorun" || return
  command -v hackbox >/dev/null || { fail "timer_autorun" "hackbox CLI absent"; return; }

  # Start from idle.
  as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
  wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" || true

  # Seed project content so the archive is not skipped (spec 3.5 skips empty).
  as_hacker_t 20 "mkdir -p \"\$HOME/project\" && echo '$MARK' > \"\$HOME/project/$MARK.txt\""

  # Count archives before. grep -c already prints 0 and exits 1 on no match, so
  # do not add a second "|| echo 0" (that would print 0 twice).
  before_n="$(as_admin_t 20 'hackbox archive list' 2>/dev/null | grep -cE '[A-Z0-9]')"
  before_n="${before_n:-0}"

  # Start a one minute slot.
  if ! as_admin_t 25 'hackbox start 1' >/dev/null 2>&1; then
    fail "timer_autorun" "hackbox start 1 failed"
    return
  fi
  if wait_for 15 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = active ]"; then
    :
  else
    fail "timer_autorun" "session did not become active after start 1 (state=$(read_state))"
    as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
    return
  fi

  # Watch the machine run itself down. Budget: 60s active + grace + reset.
  saw_ending=0; reached_idle=0
  deadline=$(( $(date +%s) + 60 + END_GRACE_SECONDS + 60 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    st="$(read_state)"
    [ "$st" = ending ] && saw_ending=1
    if [ "$st" = idle ]; then
      # Only count idle after we have left the initial active phase.
      reached_idle=1; break
    fi
    sleep 1
  done

  if [ "$saw_ending" -eq 1 ]; then
    pass "timer_passes_through_ending"
  else
    fail "timer_passes_through_ending" "never observed the 'ending' state during the self-driven run"
  fi
  if [ "$reached_idle" -eq 1 ]; then
    pass "timer_autorun"
  else
    fail "timer_autorun" "box did not reach idle on its own (last state=$(read_state))"
  fi

  # An archive appeared because the project had content.
  after_n="$(as_admin_t 20 'hackbox archive list' 2>/dev/null | grep -cE '[A-Z0-9]')"
  after_n="${after_n:-0}"
  if [ "$after_n" -gt "$before_n" ]; then
    pass "timer_archive_created"
  else
    fail "timer_archive_created" "archive count did not grow (before=$before_n after=$after_n)"
  fi

  # Leave the box idle.
  as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
  wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" || true
}
