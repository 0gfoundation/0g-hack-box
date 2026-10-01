# install: the agents and toolchain are installed system-wide and runnable by
# the attendee, not tucked in the admin home. Spec 4.1.

group_install() {
  need_linux_or_skip "toolchain" || return

  # claude runnable by hacker.
  if OUT="$(as_hacker_t 30 'claude --version')"; then
    pass "claude_runs_as_hacker"
  else
    fail "claude_runs_as_hacker" "claude --version failed as hacker: ${OUT:-no output}"
  fi

  # opencode runnable by hacker.
  if OUT="$(as_hacker_t 30 'opencode --version')"; then
    pass "opencode_runs_as_hacker"
  else
    fail "opencode_runs_as_hacker" "opencode --version failed as hacker: ${OUT:-no output}"
  fi

  # node is version 22.
  OUT="$(as_hacker_t 20 'node -v')"
  if printf '%s' "$OUT" | grep -q '^v22\.'; then
    pass "node_is_22"
  else
    fail "node_is_22" "node -v as hacker was '${OUT:-empty}', expected v22.x"
  fi

  # chromium present and runnable (version query only, no display).
  if as_hacker_t 30 'command -v chromium >/dev/null || command -v chromium-browser >/dev/null'; then
    pass "chromium_present"
  else
    fail "chromium_present" "no chromium/chromium-browser on hacker PATH"
  fi

  # System-wide location: the resolved binaries must live outside any home dir.
  for tool in claude opencode; do
    path="$(as_hacker_t 15 "command -v $tool" | tail -1)"
    # Resolve symlinks to the real target.
    real="$(as_hacker_t 15 "readlink -f \"\$(command -v $tool)\" 2>/dev/null" | tail -1)"
    loc="${real:-$path}"
    case "$loc" in
      /home/*)
        fail "${tool}_not_in_a_home" "$tool resolves into a home dir: $loc" ;;
      "")
        fail "${tool}_not_in_a_home" "could not resolve $tool location" ;;
      *)
        pass "${tool}_not_in_a_home" ;;
    esac
  done

  # Nothing of the agents lives ONLY in the admin home: the admin's private
  # per-user install dirs must not be the source of truth. If claude exists in
  # the admin home but not system-wide, that is the failure the spec warns of.
  admin_home="$HOME"
  sys_claude="$(command -v claude 2>/dev/null)"
  if [ -n "$sys_claude" ] && case "$sys_claude" in "$admin_home"/*) true;; *) false;; esac; then
    fail "agents_not_admin_only" "the claude on PATH is the admin's private copy: $sys_claude"
  else
    pass "agents_not_admin_only"
  fi
}
