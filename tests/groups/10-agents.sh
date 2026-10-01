# agents: the agent switch, the generated managed configs, the secrets handling
# and the fresh-home onboarding seed. Spec 4.2 to 4.5. Live model calls are an
# opt-in extra (--live).

# _set_ids: the agent ids present in `hackbox agents show`.
# Only the "agents: <ids>" line is the set: the per-agent lines below it list
# every known agent, offered or off.
_set_ids() {
  as_admin_t 20 'hackbox agents show' 2>/dev/null \
    | sed -n 's/^agents:[[:space:]]*//p' | head -n 1 \
    | grep -oE 'claude-0g|claude|opencode' | awk '!seen[$0]++'
}
# _agents_set: apply a set (space separated ids). Echoes rc.
_agents_set() { as_admin_t 30 "hackbox agents set $*" >/dev/null 2>&1; echo $?; }

# _valid_json_file: sudo-read a root file and check it parses (python3 or jq).
_valid_json_file() {
  local f="$1"
  sudo -n cat "$f" 2>/dev/null | json_valid
}

group_agents() {
  need_linux_or_skip "agents_show" || return
  command -v hackbox >/dev/null || { fail "agents_show" "hackbox CLI absent"; return; }

  # show lists a configured set.
  cur="$(_set_ids | tr '\n' ' ')"
  if [ -n "$cur" ]; then
    pass "agents_show"
  else
    fail "agents_show" "hackbox agents show listed no known agent ids"
  fi

  # Mutually exclusive claude and claude-0g are rejected, and the set is
  # unchanged by the rejected call.
  before="$(_set_ids | tr '\n' ' ')"
  rc="$(_agents_set claude claude-0g)"
  after="$(_set_ids | tr '\n' ' ')"
  if [ "$rc" != 0 ] && [ "$before" = "$after" ]; then
    pass "agents_reject_claude_conflict"
  else
    fail "agents_reject_claude_conflict" "set claude+claude-0g rc=$rc, set went '$before' to '$after'"
  fi

  # onboarding pre-seeded in the current (fresh) home.
  if as_hacker_t 15 'test -f "$HOME/.claude.json"' ; then
    cj="$(as_hacker_t 15 'cat "$HOME/.claude.json"')"
    onb="$(printf '%s' "$cj" | json_path 'hasCompletedOnboarding')"
    trust="$(printf '%s' "$cj" | json_path "projects./home/$HACKER_USER/project.hasTrustDialogAccepted")"
    if [ "$onb" = "True" ] || [ "$onb" = "true" ]; then
      if [ "$trust" = "True" ] || [ "$trust" = "true" ]; then
        pass "onboarding_seeded"
      else
        # json_path splits on dots and the project key contains slashes not dots,
        # so fall back to a textual check for the trust flag.
        if printf '%s' "$cj" | grep -q '"hasTrustDialogAccepted"[[:space:]]*:[[:space:]]*true'; then
          pass "onboarding_seeded"
        else
          fail "onboarding_seeded" "~/.claude.json onboarding ok but project trust not set"
        fi
      fi
    elif printf '%s' "$cj" | grep -q 'hasCompletedOnboarding' && printf '%s' "$cj" | grep -q 'hasTrustDialogAccepted'; then
      pass "onboarding_seeded"
    else
      fail "onboarding_seeded" "~/.claude.json present but onboarding/trust not set"
    fi
  else
    fail "onboarding_seeded" "~/.claude.json missing from a fresh home"
  fi

  # Secrets directory owner and mode.
  d=/etc/hackbox/secrets
  if sudo -n test -d "$d" 2>/dev/null; then
    line="$(sudo -n stat -c '%a %U:%G' "$d" 2>/dev/null)"
    # SPEC 1 as amended by A1: root:<attendee group> 0750, so helpers running
    # as the attendee can open the agent-facing key files.
    if [ "$line" = "750 root:$HACKER_USER" ]; then
      pass "secrets_dir_mode"
    else
      fail "secrets_dir_mode" "$d is '$line', expected '750 root:$HACKER_USER'"
    fi
    # Per-file owner and mode (only assert files that exist).
    prob=""
    # All four names are agent-facing (read by the apiKeyHelper or the launcher,
    # both running as the attendee), so SPEC 1 makes each root:hacker 0640.
    for pair in "anthropic-api-key 640 root:$HACKER_USER" "0g-router-key 640 root:$HACKER_USER" \
                "claude-gateway-token 640 root:$HACKER_USER" "claude-oauth-token 640 root:$HACKER_USER"; do
      set -- $pair; nm="$1"; wantmode="$2"; wantown="$3"
      f="$d/$nm"
      if sudo -n test -e "$f" 2>/dev/null; then
        got="$(sudo -n stat -c '%a %U:%G' "$f" 2>/dev/null)"
        [ "$got" = "$wantmode $wantown" ] || prob="$prob $nm($got)"
      fi
    done
    if [ -z "$prob" ]; then pass "secrets_file_modes"; else fail "secrets_file_modes" "wrong owner/mode:$prob"; fi
  else
    skip "secrets_dir_mode" "/etc/hackbox/secrets not present"
    skip "secrets_file_modes" "/etc/hackbox/secrets not present"
  fi

  # secret list never prints a secret value: compare the listing against the
  # real contents of every secret file on the box.
  listing="$(as_admin_t 20 'hackbox secret list' 2>/dev/null)"
  if [ -z "$listing" ]; then
    skip "secret_list_hides_values" "hackbox secret list produced no output"
  else
    leaked=""
    if sudo -n test -d "$d" 2>/dev/null; then
      while IFS= read -r f; do
        [ -s "$f" ] || continue
        val="$(sudo -n cat "$f" 2>/dev/null | tr -d '[:space:]')"
        [ ${#val} -ge 6 ] || continue
        if printf '%s' "$listing" | tr -d '[:space:]' | grep -qF "$val"; then
          leaked="$leaked $(basename "$f")"
        fi
      done < <(sudo -n find "$d" -maxdepth 1 -type f 2>/dev/null)
    fi
    if [ -z "$leaked" ]; then
      pass "secret_list_hides_values"
    else
      fail "secret_list_hides_values" "secret list leaked the value of:$leaked"
    fi
  fi

  # Cycle valid combinations and validate the generated configs. Restore the
  # original set afterwards. Needs sudo (agents set rewrites root files).
  orig="$cur"
  if ! have_sudo || [ -z "$orig" ]; then
    skip "agents_configs_consistent" "no sudo or unknown current set; not cycling combinations"
  else
    fails=""
    for combo in "claude opencode" "claude-0g opencode" "opencode" "claude"; do
      rc="$(_agents_set $combo)"
      if [ "$rc" != 0 ]; then fails="$fails set[$combo]=rc$rc"; continue; fi
      got="$(_set_ids | tr '\n' ' ' | sed 's/ *$//')"
      # Consistency of agents.json (3.7): valid JSON, and its commands match set.
      aj=/etc/hackbox/agents.json
      if _valid_json_file "$aj"; then
        cmds="$(sudo -n cat "$aj" 2>/dev/null | json_path '[].command' | tr '\n' ' ')"
        case "$combo" in
          *opencode*) printf '%s' "$cmds" | grep -qw opencode || fails="$fails agents.json[$combo]no-opencode";;
        esac
        case "$combo" in
          claude*|*claude*) printf '%s' "$cmds" | grep -qw claude || fails="$fails agents.json[$combo]no-claude";;
        esac
      else
        fails="$fails agents.json[$combo]invalid"
      fi
      # managed-settings.json when a Claude harness is in the set.
      case "$combo" in
        claude|claude*opencode|*claude*)
          ms=/etc/claude-code/managed-settings.json
          if sudo -n test -e "$ms" 2>/dev/null; then
            _valid_json_file "$ms" || fails="$fails managed-settings[$combo]invalid"
          fi ;;
      esac
      # claude-0g must point the base URL at the 0G router: OG_ROUTER_URL from
      # the box config (SPEC 4.4 makes it overridable, for example the lab's
      # stand-in), as the bare host the Anthropic client appends /v1/messages to.
      case "$combo" in
        claude-0g*)
          ms=/etc/claude-code/managed-settings.json
          url="$(sudo -n cat "$ms" 2>/dev/null | json_path 'env.ANTHROPIC_BASE_URL')"
          want_url="${OG_ROUTER_URL%/}"; want_url="${want_url%/v1}"
          [ -n "$url" ] && [ "$url" = "$want_url" ] || fails="$fails claude-0g-baseurl='$url'(want '$want_url')"
          ;;
      esac
      # opencode.json when opencode is in the set.
      case "$combo" in
        *opencode*)
          oj=/etc/opencode/opencode.json
          if sudo -n test -e "$oj" 2>/dev/null; then
            _valid_json_file "$oj" || fails="$fails opencode.json[$combo]invalid"
          else
            fails="$fails opencode.json[$combo]missing"
          fi ;;
      esac
    done
    # Restore original set.
    _agents_set $orig >/dev/null 2>&1
    if [ -z "$fails" ]; then
      pass "agents_configs_consistent"
    else
      fail "agents_configs_consistent" "$fails"
    fi
  fi

  # Opt-in live model calls.
  if [ "${LIVE:-0}" -ne 1 ]; then
    skip "agents_live_calls" "live calls are opt-in; pass --live"
    return
  fi
  # Through hackbox-agent, the command the welcome buttons run (SPEC 3.7): it
  # is the attendee's real path, and on the subscription route a bare `claude`
  # has no credentials by design.
  # HB_LIVE_CLAUDE_ARGS (default empty) is appended to the Claude Code call. It
  # exists for a stand-in endpoint whose context window is smaller than Claude
  # Code's full request (the lab gateway caps at 16K tokens, and the 21 built-in
  # tool schemas alone exceed that); for example HB_LIVE_CLAUDE_ARGS="--tools
  # Bash Read Edit". Leave it empty against the real 0G Router or Anthropic.
  if [ -n "${HB_LIVE_CLAUDE_ARGS:-}" ]; then
    echo "# note: agents_live_calls runs Claude Code with HB_LIVE_CLAUDE_ARGS=$HB_LIVE_CLAUDE_ARGS"
  fi
  enabled="$(_set_ids)"
  any=0; lfail=""
  for id in $enabled; do
    case "$id" in
      claude|claude-0g)
        any=1
        if ! as_hacker_t 120 "cd \"\$HOME/project\" && hackbox-agent $id -p 'Reply with exactly: OK' ${HB_LIVE_CLAUDE_ARGS:-} </dev/null 2>/dev/null | grep -qiE 'ok|okay'"; then
          lfail="$lfail $id"
        fi ;;
      opencode)
        any=1
        if ! as_hacker_t 120 "cd \"\$(mktemp -d)\" && hackbox-agent opencode run 'Reply with exactly: OK' </dev/null 2>/dev/null | grep -qiE 'ok|okay'"; then
          lfail="$lfail opencode"
        fi ;;
    esac
  done
  if [ "$any" -eq 0 ]; then
    skip "agents_live_calls" "no live-capable agent enabled"
  elif [ -z "$lfail" ]; then
    pass "agents_live_calls"
  else
    fail "agents_live_calls" "no sane answer from:$lfail"
  fi
}
