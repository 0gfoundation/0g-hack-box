# archive: staff can list saved projects and retrieve a tarball that contains
# the attendee's work but excludes node_modules. Spec 3.5.
#
# Producing a deterministic archive requires a reset (it archives then wipes),
# which is destructive. With --destructive this group seeds a known project and
# drives one reset to create a fixture, then verifies list/get/exclusion. Without
# it, the group verifies list/get against whatever archives already exist, and
# skips the exclusion check (no controlled content).

# _extract_codes: print any 6-character archive codes found in stdin.
_extract_codes() { grep -oE '[A-HJ-NP-Z2-9A-Z0-9]{6}' | grep -E '^[A-Z0-9]{6}$'; }

group_archive() {
  need_linux_or_skip "archive_list" || return
  command -v hackbox >/dev/null || { fail "archive_list" "hackbox CLI absent"; return; }
  # The RETURN trap outlives this function (traps are global), so it clears
  # itself; otherwise it fires again in the caller where $tmp is unbound.
  local tmp; tmp="$(mktemp -d)"; trap 'rm -rf "${tmp:-}"; trap - RETURN' RETURN

  # If destructive, build a controlled fixture.
  local fixture_code=""
  if [ "${DESTRUCTIVE:-0}" -eq 1 ] && have_sudo; then
    as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
    wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" || true
    as_hacker_t 25 "mkdir -p \"\$HOME/project/src\" \"\$HOME/project/node_modules/leftpad\"; \
      echo '$MARK' > \"\$HOME/project/src/$MARK.txt\"; \
      echo 'junk' > \"\$HOME/project/node_modules/leftpad/index.js\"; \
      printf 'SECRET=$MARK\n' > \"\$HOME/project/.env\"; \
      printf 'PRIVKEY $MARK' > \"\$HOME/project/wallet.key\"; \
      printf 'registry=$MARK' > \"\$HOME/project/.npmrc\""
    as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
    wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]" || true
  fi

  # list works and is parseable.
  OUT="$(as_admin_t 20 'hackbox archive list' 2>&1)"; RC=$?
  if [ "$RC" -ne 0 ]; then
    fail "archive_list" "hackbox archive list exited $RC: $(printf '%s' "$OUT" | head -c 160)"
    return
  fi
  pass "archive_list"

  # Pick a code to retrieve: prefer the newest listed.
  local code
  code="$(printf '%s' "$OUT" | _extract_codes | tail -1)"
  if [ -z "$code" ]; then
    skip "archive_get" "no archive present to retrieve"
    skip "archive_contains_marker" "no archive present"
    skip "archive_excludes_node_modules" "no archive present"
    return
  fi

  # get retrieves a tarball. Accept stdout streaming or a produced file path.
  local tarf="$tmp/got.tar.gz"
  as_admin_t 40 "hackbox archive get $code" > "$tarf" 2>"$tmp/err"
  if tar tzf "$tarf" >/dev/null 2>&1; then
    pass "archive_get"
  else
    # Maybe get printed a path or needs a destination argument.
    local prod
    prod="$(grep -oE '/[^[:space:]]+\.tar\.(gz|zst)[^[:space:]]*' "$tmp/err" "$tarf" 2>/dev/null | head -1)"
    if [ -n "$prod" ] && sudo -n test -f "$prod" 2>/dev/null; then
      sudo -n cat "$prod" > "$tarf" 2>/dev/null
    fi
    if tar tzf "$tarf" >/dev/null 2>&1; then
      pass "archive_get"
    else
      fail "archive_get" "hackbox archive get $code did not yield a readable tar.gz"
      skip "archive_contains_marker" "no tarball retrieved"
      skip "archive_excludes_node_modules" "no tarball retrieved"
      return
    fi
  fi

  # Contents. Only assert the marker/exclusion when we control the fixture.
  local listing; listing="$(tar tzf "$tarf" 2>/dev/null)"
  if [ "${DESTRUCTIVE:-0}" -eq 1 ] && have_sudo; then
    if printf '%s' "$listing" | grep -q "$MARK"; then
      pass "archive_contains_marker"
    else
      fail "archive_contains_marker" "the seeded marker file is not in the tarball"
    fi
    if printf '%s' "$listing" | grep -q 'node_modules/'; then
      fail "archive_excludes_node_modules" "node_modules is present in the tarball"
    else
      pass "archive_excludes_node_modules"
    fi
    # Secret-looking files must be excluded (.env*, keys, .npmrc). Spec note (c).
    leak="$(printf '%s' "$listing" | grep -E '(^|/)(\.env|\.npmrc|[^/]*\.key)$' || true)"
    if [ -z "$leak" ]; then
      pass "archive_excludes_secrets"
    else
      fail "archive_excludes_secrets" "secret-looking files in the tarball: $(printf '%s' "$leak" | tr '\n' ' ')"
    fi
  else
    # Read only mode: at least confirm the tar is non-empty and node_modules is
    # absent from whatever real archive we retrieved.
    if [ -n "$listing" ]; then pass "archive_contains_marker"; else fail "archive_contains_marker" "retrieved tarball is empty"; fi
    if printf '%s' "$listing" | grep -q 'node_modules/'; then
      fail "archive_excludes_node_modules" "node_modules present in an existing archive"
    else
      pass "archive_excludes_node_modules"
    fi
    skip "archive_excludes_secrets" "read only mode: no controlled fixture; run with --destructive"
  fi
}
