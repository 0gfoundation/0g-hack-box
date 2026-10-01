# cli: every documented hackbox subcommand exists, an unknown one prints the
# list, bad input prints usage and exits nonzero, read only subcommands work.
# Spec 3.8, 4.2, 4.5, 5.

group_cli() {
  need_linux_or_skip "cli_dispatch" || return
  command -v hackbox >/dev/null || { fail "cli_dispatch" "hackbox CLI absent"; return; }

  # Unknown subcommand: nonzero, and the output lists the real subcommands.
  OUT="$(as_admin_t 15 'hackbox definitely-not-a-subcommand' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ]; then
    pass "cli_unknown_nonzero"
  else
    fail "cli_unknown_nonzero" "unknown subcommand exited 0"
  fi
  # The listing should name the documented subcommands.
  miss=""
  for s in status start extend end reset archive agents secret lock; do
    printf '%s' "$OUT" | grep -qw "$s" || miss="$miss $s"
  done
  if [ -z "$miss" ]; then
    pass "cli_lists_subcommands"
  else
    fail "cli_lists_subcommands" "dispatcher listing omits:$miss"
  fi

  # Read only subcommands succeed (exit 0).
  for pair in "status:hackbox status" "lock:hackbox lock status" \
              "agents:hackbox agents show" "secret:hackbox secret list" \
              "archive:hackbox archive list"; do
    name="${pair%%:*}"; cmd="${pair#*:}"
    if as_admin_t 25 "$cmd" >/dev/null 2>&1; then
      pass "cli_${name}_ok"
    else
      fail "cli_${name}_ok" "'$cmd' exited nonzero"
    fi
  done

  # Bad input: usage and nonzero, without side effects.
  # extend with no argument.
  OUT="$(as_admin_t 15 'hackbox extend' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qiE 'usage|minutes|argument'; then
    pass "cli_extend_usage"
  else
    fail "cli_extend_usage" "extend with no arg: rc=$RC out='$(printf '%s' "$OUT" | head -c 120)'"
  fi
  # extend with a non-numeric argument.
  OUT="$(as_admin_t 15 'hackbox extend not-a-number' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ]; then pass "cli_extend_badnum"; else fail "cli_extend_badnum" "extend with bad number exited 0"; fi
  # start with a non-numeric argument must not start a session.
  before="$(read_state)"
  OUT="$(as_admin_t 15 'hackbox start not-a-number' 2>&1)"; RC=$?
  after="$(read_state)"
  if [ "$RC" -ne 0 ] && [ "$before" = "$after" ]; then
    pass "cli_start_badnum"
  else
    fail "cli_start_badnum" "start with bad arg: rc=$RC, state '$before'->'$after'"
  fi
  # archive get with no code.
  OUT="$(as_admin_t 15 'hackbox archive get' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qiE 'usage|code|argument'; then
    pass "cli_archive_get_usage"
  else
    fail "cli_archive_get_usage" "archive get with no code: rc=$RC out='$(printf '%s' "$OUT" | head -c 120)'"
  fi
  # archive get with a code that cannot exist.
  OUT="$(as_admin_t 20 'hackbox archive get ZZZZZZ' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ]; then pass "cli_archive_get_missing"; else fail "cli_archive_get_missing" "archive get of a missing code exited 0"; fi
}
