#!/usr/bin/env bash
# Shared helpers for the hack-box black-box test suite. Sourced by tests/run.sh
# and by every group in tests/groups/. Written from docs/SPEC.md section 7; it
# asserts what the spec promises, not how the build achieves it.
#
# No em dashes, no double hyphens in prose (CLI flags are fine). Bash 4+.

# ---------------------------------------------------------------------------
# Result accounting. The runner sets CURRENT_GROUP before invoking a group.
# ---------------------------------------------------------------------------
: "${PASS_N:=0}"
: "${FAIL_N:=0}"
: "${SKIP_N:=0}"
: "${CURRENT_GROUP:=misc}"
# FAILED_NAMES collects "group/name" for the summary.
: "${FAILED_NAMES:=}"

pass() {
  PASS_N=$((PASS_N + 1))
  printf 'PASS %s/%s\n' "$CURRENT_GROUP" "$1"
}

fail() {
  local name="$1"; shift
  FAIL_N=$((FAIL_N + 1))
  FAILED_NAMES="$FAILED_NAMES $CURRENT_GROUP/$name"
  # Collapse newlines so one check is always one line.
  local msg; msg="$(printf '%s' "$*" | tr '\n' ' ' | sed 's/  */ /g')"
  printf 'FAIL %s/%s: %s\n' "$CURRENT_GROUP" "$name" "$msg"
}

skip() {
  local name="$1"; shift
  SKIP_N=$((SKIP_N + 1))
  local msg; msg="$(printf '%s' "$*" | tr '\n' ' ' | sed 's/  */ /g')"
  printf 'SKIP %s/%s: %s\n' "$CURRENT_GROUP" "$name" "$msg"
}

# ---------------------------------------------------------------------------
# Config. Helpers on the box load /etc/hackbox/conf.d/*.conf; do the same so
# the tests use the box's real values (home size, session length, caps). Fall
# back to the spec defaults so the runner's own plumbing can be exercised off
# the box (static self-test on a workstation).
# ---------------------------------------------------------------------------
load_hackbox_conf() {
  local f
  if compgen -G '/etc/hackbox/conf.d/*.conf' >/dev/null 2>&1; then
    for f in /etc/hackbox/conf.d/*.conf; do
      # shellcheck disable=SC1090
      . "$f" 2>/dev/null || true
    done
  fi
  # Spec section 3.9 / 4 defaults.
  : "${HACKER_USER:=hacker}"
  : "${HACKER_UID:=2000}"
  : "${SESSION_MINUTES:=30}"
  : "${WARN_MINUTES:=5 1}"
  : "${END_GRACE_SECONDS:=20}"
  : "${HOME_IMG_SIZE:=40G}"
  : "${ARCHIVE_HOURS:=24}"
  : "${ARCHIVE_MAX_MB:=200}"
  # Lockdown caps (30-lockdown.conf). Defaults from spec section 5; the box may
  # scale them, so tests assert "a cap is present", not an exact number, unless
  # the conf gives one.
  : "${MEMORY_MAX:=}"
  : "${TASKS_MAX:=}"
  : "${CPU_QUOTA:=}"
  # AI (20-ai.conf).
  : "${CLAUDE_AUTH:=apikey}"
  : "${CLAUDE_MODELS:=sonnet haiku}"
  : "${OG_ROUTER_URL:=https://router-api.0g.ai}"
  : "${OG_MODEL:=glm-5.3}"
}

# ---------------------------------------------------------------------------
# Environment probes.
# ---------------------------------------------------------------------------
on_linux() { [ "$(uname -s)" = "Linux" ]; }

have() { command -v "$1" >/dev/null 2>&1; }

# is_admin_with_sudo: the runner is meant to run as the admin user who has
# passwordless sudo. Prove it without prompting.
have_sudo() { sudo -n true >/dev/null 2>&1; }

hacker_exists() { id -u "$HACKER_USER" >/dev/null 2>&1; }

# ---------------------------------------------------------------------------
# Run a command as the attendee (uid 2000) with a realistic login environment.
# The admin has passwordless sudo, so use sudo to drop to the hacker user and
# run a login shell (loads /etc/profile, PATH additions, the skeleton .bashrc).
# All arguments are joined into one shell command string.
# ---------------------------------------------------------------------------
as_hacker() {
  local cmd="$*"
  sudo -n -u "$HACKER_USER" -H env -i \
    HOME="/home/$HACKER_USER" USER="$HACKER_USER" LOGNAME="$HACKER_USER" \
    SHELL=/bin/bash TERM="${TERM:-xterm}" \
    PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
    bash -lc "$cmd"
}

# Same, but with an explicit wall-clock timeout so a check can never hang. The
# sudo invocation is inlined (not routed through the as_hacker function) because
# `timeout` execs a fresh process that would not carry shell functions.
as_hacker_t() {
  local secs="$1"; shift
  timeout --signal=KILL "$secs" \
    sudo -n -u "$HACKER_USER" -H env -i \
      HOME="/home/$HACKER_USER" USER="$HACKER_USER" LOGNAME="$HACKER_USER" \
      SHELL=/bin/bash TERM="${TERM:-xterm}" \
      PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
      bash -lc "$*" 2>&1
}

# Run as the admin (the current user), timed.
as_admin_t() {
  local secs="$1"; shift
  timeout --signal=KILL "$secs" bash -lc "$*" 2>&1
}

# ---------------------------------------------------------------------------
# wait_for <seconds> <command...>: poll the command (as a shell string) until
# it succeeds or the deadline passes. Returns 0 on success, 1 on timeout.
# ---------------------------------------------------------------------------
wait_for() {
  local secs="$1"; shift
  local deadline=$(( $(date +%s) + secs ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if bash -c "$*" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

# ---------------------------------------------------------------------------
# Small capture helpers. _run captures combined output into OUT and rc into RC.
# ---------------------------------------------------------------------------
_run() { OUT="$("$@" 2>&1)"; RC=$?; return $RC; }
_sh()  { OUT="$(bash -c "$*" 2>&1)"; RC=$?; return $RC; }

# ---------------------------------------------------------------------------
# JSON helpers. Prefer python3 (always present on Mint) over jq so the suite
# does not depend on jq being installed in the guest; fall back to jq if for
# some reason python3 is absent.
# ---------------------------------------------------------------------------
json_valid() { # reads stdin
  if have python3; then python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1
  elif have jq; then jq -e . >/dev/null 2>&1
  else return 2; fi
}

# NOTE: the JSON comes on stdin, so the python program is passed with -c (not
# via `python3 -` which would read the program from stdin and collide).
_JSON_KEYS_PY='import json,sys
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
sys.stdout.write("\n".join(d.keys()) if isinstance(d,dict) else "")'

_JSON_PATH_PY='import json,sys
expr=sys.argv[1]
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
def out(v):
    if v is None: return
    print(v if not isinstance(v,(dict,list)) else json.dumps(v))
if expr.startswith("[]."):
    key=expr[3:]
    if isinstance(d,list):
        for it in d:
            if isinstance(it,dict) and key in it: out(it[key])
else:
    cur=d
    for part in expr.split("."):
        if isinstance(cur,dict) and part in cur: cur=cur[part]
        else: cur=None; break
    out(cur)'

_JSON_FINDNUM_PY='import json,sys,re
rx=re.compile(sys.argv[1],re.I)
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
found=[]
def walk(o):
    if isinstance(o,dict):
        for k,v in o.items():
            if rx.search(str(k)) and isinstance(v,(int,float)) and not isinstance(v,bool):
                found.append(v)
            walk(v)
    elif isinstance(o,list):
        for v in o: walk(v)
walk(d)
sys.stdout.write(str(int(found[0])) if found else "")'

json_keys() { # reads stdin, prints top-level object keys one per line
  if have python3; then python3 -c "$_JSON_KEYS_PY" 2>/dev/null
  elif have jq; then jq -r 'keys[]' 2>/dev/null; fi
}

json_path() { # $1 = dotted path (env.FOO) or [].field for an array map; stdin
  if have python3; then python3 -c "$_JSON_PATH_PY" "$1" 2>/dev/null
  elif have jq; then
    case "$1" in
      '[].'*) jq -r ".[].${1#[].} // empty" 2>/dev/null ;;
      *)      jq -r ".${1} // empty" 2>/dev/null ;;
    esac
  fi
}

json_find_num() { # $1 = key-name regex; stdin; prints the first numeric match
  if have python3; then python3 -c "$_JSON_FINDNUM_PY" "$1" 2>/dev/null
  elif have jq; then jq -r "..|.\"$1\"? // empty" 2>/dev/null | head -1; fi
}

# is_json: validate a JSON string held in $1.
is_json() { printf '%s' "$1" | json_valid; }

# json_field: extract a dotted path from a JSON string held in $1.
json_field() { printf '%s' "$1" | json_path "$2"; }

# elapsed_ms: run a shell string, echo the wall-clock milliseconds it took.
elapsed_ms() {
  local start end
  start=$(date +%s%3N 2>/dev/null || echo $(( $(date +%s) * 1000 )))
  bash -c "$*" >/dev/null 2>&1
  end=$(date +%s%3N 2>/dev/null || echo $(( $(date +%s) * 1000 )))
  echo $(( end - start ))
}

# read_state: current session state word, or the literal "MISSING".
read_state() {
  if [ -r /run/hackbox/state ]; then
    tr -d '[:space:]' < /run/hackbox/state
  else
    echo MISSING
  fi
}

# A unique marker string for this run.
MARK="hb_test_$$_$(date +%s)"

# guard_root_only: a check needs write access under /run/hackbox etc.; assert
# we are root-capable (sudo) or skip cleanly.
need_sudo_or_skip() {
  local name="$1"
  if ! have_sudo; then
    skip "$name" "runner is not the admin with passwordless sudo"
    return 1
  fi
  return 0
}

need_linux_or_skip() {
  local name="$1"
  if ! on_linux; then
    skip "$name" "not running on the target Linux guest"
    return 1
  fi
  return 0
}
