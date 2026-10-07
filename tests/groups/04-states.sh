# states: the session state machine and the runtime state files. The attendee
# can read state but cannot write it or drive the units. Spec 3.2, 3.4, 3.8.
#
# The state-changing checks (start/active/extend) only run when the box is idle,
# and they always return the box to idle so the check is self-contained. The
# negative checks (hacker cannot write /run/hackbox, cannot stop/start units)
# and status --json are fully read only.

# keys_of: top-level JSON object keys, one per line (python3 or jq via lib).
_keys_of() { printf '%s' "$1" | json_keys; }
# has_key_like: a key name matching a regex exists.
_has_key_like() { _keys_of "$1" | grep -qiE "$2"; }
# seconds_left: pull a countdown-like numeric field from a status JSON string.
_seconds_left() { printf '%s' "$1" | json_find_num 'second|left|remain'; }

group_states() {
  need_linux_or_skip "state_file" || return
  local hb; hb="$(command -v hackbox || echo /usr/local/bin/hackbox)"
  if [ ! -x "$hb" ]; then
    fail "cli_present" "hackbox CLI not found on PATH"
    return
  fi

  # status --json is valid JSON and has a field for every concept the spec lists.
  if ! have python3 && ! have jq; then
    skip "status_json_valid" "neither python3 nor jq available to parse JSON"
  else
    OUT="$(as_admin_t 20 'hackbox status --json')"
    if is_json "$OUT"; then
      pass "status_json_valid"
      # Spec 3.8 field list: state, seconds left, agents, load, memory, disk of
      # the home image, last reset time and duration. Field names are not fixed
      # by the spec, so match key names by concept.
      declare -a miss=()
      _has_key_like "$OUT" 'state'                 || miss+=(state)
      _has_key_like "$OUT" 'second|seconds_left|left|remain' || miss+=(seconds_left)
      _has_key_like "$OUT" 'agent'                 || miss+=(agents)
      _has_key_like "$OUT" 'load'                  || miss+=(load)
      _has_key_like "$OUT" 'mem'                   || miss+=(memory)
      _has_key_like "$OUT" 'disk|home'             || miss+=(disk)
      _has_key_like "$OUT" 'reset'                 || miss+=(last_reset)
      if [ "${#miss[@]}" -eq 0 ]; then
        pass "status_json_fields"
      else
        fail "status_json_fields" "missing fields: ${miss[*]}; keys were: $(_keys_of "$OUT" | tr '\n' ' ')"
      fi
    else
      fail "status_json_valid" "status --json was not valid JSON: $(printf '%s' "$OUT" | head -c 200)"
    fi
  fi

  # /run/hackbox/state readable and holds one of the known words.
  local s; s="$(read_state)"
  case "$s" in
    idle|active|ending|resetting) pass "state_word_valid" ;;
    MISSING) fail "state_word_valid" "/run/hackbox/state is unreadable or absent" ;;
    *) fail "state_word_valid" "unexpected state word: '$s'" ;;
  esac

  # hacker cannot write /run/hackbox/* .
  if as_hacker_t 10 'echo x > /run/hackbox/state' 2>/dev/null; then
    fail "hacker_cannot_write_state" "hacker wrote /run/hackbox/state"
    # restore
    sudo -n bash -c 'echo idle > /run/hackbox/state' 2>/dev/null || true
  else
    pass "hacker_cannot_write_state"
  fi
  if as_hacker_t 10 'echo x > /run/hackbox/session-end' 2>/dev/null; then
    fail "hacker_cannot_write_sessionend" "hacker wrote /run/hackbox/session-end"
    sudo -n rm -f /run/hackbox/session-end 2>/dev/null || true
  else
    pass "hacker_cannot_write_sessionend"
  fi

  # hacker cannot stop the timer unit.
  if as_hacker_t 15 'systemctl stop hackbox-session.service' 2>/dev/null; then
    fail "hacker_cannot_stop_timer" "hacker stopped hackbox-session.service"
  else
    pass "hacker_cannot_stop_timer"
  fi
  # hacker cannot start an arbitrary privileged unit (polkit default-deny).
  if as_hacker_t 15 'systemctl start systemd-tmpfiles-clean.service' 2>/dev/null; then
    fail "hacker_cannot_start_arbitrary_unit" "hacker started a unit outside the allowlist"
  else
    pass "hacker_cannot_start_arbitrary_unit"
  fi

  # State transitions: only from idle, and always restore to idle. Not at
  # install time (setup/90-verify.sh): the restoring reset restarts LightDM,
  # which would end a desktop session the installer may be running in.
  if [ "${HB_VERIFY_READONLY:-0}" = 1 ]; then
    skip "start_moves_to_active" "install-time verification does not start or reset sessions"
    skip "seconds_left_counts_down" "install-time verification does not start or reset sessions"
    skip "admin_can_extend" "install-time verification does not start or reset sessions"
    return
  fi
  if [ "$s" != "idle" ]; then
    skip "start_moves_to_active" "box is '$s', not idle; not disturbing a live session"
    skip "seconds_left_counts_down" "box is '$s', not idle"
    skip "admin_can_extend" "box is '$s', not idle"
    return
  fi
  if ! need_sudo_or_skip "start_moves_to_active"; then
    skip "seconds_left_counts_down" "no sudo"
    skip "admin_can_extend" "no sudo"
    return
  fi

  # start a short session for the state test.
  as_admin_t 25 "hackbox start ${SESSION_MINUTES}" >/dev/null 2>&1 || true
  if wait_for 15 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = active ]"; then
    pass "start_moves_to_active"
  else
    fail "start_moves_to_active" "state did not become active after hackbox start (is '$(read_state)')"
  fi

  # seconds_left counts down: sample twice.
  s1="$(_seconds_left "$(as_admin_t 15 'hackbox status --json')")"
  sleep 3
  s2="$(_seconds_left "$(as_admin_t 15 'hackbox status --json')")"
  if [ -n "$s1" ] && [ -n "$s2" ] && [ "$s2" -lt "$s1" ]; then
    pass "seconds_left_counts_down"
  else
    fail "seconds_left_counts_down" "seconds did not decrease: first='$s1' second='$s2'"
  fi

  # admin can extend: seconds_left jumps up by roughly the added minutes.
  before="$s2"
  as_admin_t 20 'hackbox extend 5' >/dev/null 2>&1
  sleep 1
  after="$(_seconds_left "$(as_admin_t 15 'hackbox status --json')")"
  if [ -n "$before" ] && [ -n "$after" ] && [ "$after" -gt "$before" ]; then
    pass "admin_can_extend"
  else
    fail "admin_can_extend" "extend did not raise seconds_left: before='$before' after='$after'"
  fi

  # Restore: bring the box back to idle for the next check/session.
  as_admin_t 60 'hackbox reset' >/dev/null 2>&1 || true
  wait_for 40 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" || true
}
