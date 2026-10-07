#!/usr/bin/env bash
# hack-box black-box test runner. Run in the guest as the admin user (who has
# passwordless sudo). Prints exactly one line per check:
#   PASS <group>/<name>
#   FAIL <group>/<name>: <what was observed>
#   SKIP <group>/<name>: <why>
# then a summary line, and exits nonzero if anything failed.
#
# Usage:
#   tests/run.sh                 run every group (non-destructive by default)
#   tests/run.sh install network run only those groups
#   tests/run.sh --list          print the group names and exit
#   tests/run.sh --destructive   also run checks that reset or stress the box
#   tests/run.sh --live          also run the opt-in live model-call checks
#
# Written from docs/SPEC.md section 7. No em dashes or double hyphens in prose.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GROUPS_DIR="$HERE/groups"

# shellcheck source=tests/lib.sh
. "$HERE/lib.sh"

# The full ordered set of groups. Destructive ones only run with --destructive.
ALL_GROUPS=(install accounts home states reset timer archive network limits agents desktop persistence cli)
DESTRUCTIVE_GROUPS=" reset timer limits persistence "

DESTRUCTIVE=0
LIVE=0
WANT=()

is_destructive() { case "$DESTRUCTIVE_GROUPS" in *" $1 "*) return 0;; *) return 1;; esac; }

usage() {
  echo "usage: run.sh [--list] [--destructive] [--live] [group ...]"
  echo "groups: ${ALL_GROUPS[*]}"
}

for arg in "$@"; do
  case "$arg" in
    --list)        printf '%s\n' "${ALL_GROUPS[@]}"; exit 0 ;;
    --destructive) DESTRUCTIVE=1 ;;
    --live)        LIVE=1 ;;
    -h|--help)     usage; exit 0 ;;
    --*)           echo "unknown option: $arg" >&2; usage >&2; exit 2 ;;
    *)             WANT+=("$arg") ;;
  esac
done

# Validate requested group names.
if [ "${#WANT[@]}" -gt 0 ]; then
  for g in "${WANT[@]}"; do
    found=0
    for a in "${ALL_GROUPS[@]}"; do [ "$g" = "$a" ] && found=1; done
    if [ "$found" -eq 0 ]; then echo "unknown group: $g" >&2; usage >&2; exit 2; fi
  done
  SELECTED=("${WANT[@]}")
else
  SELECTED=("${ALL_GROUPS[@]}")
fi

export DESTRUCTIVE LIVE
load_hackbox_conf

run_group() {
  local g="$1"
  local file="$GROUPS_DIR/"[0-9][0-9]"-$g.sh"
  # Resolve the numbered filename.
  local match
  match="$(ls "$GROUPS_DIR"/[0-9][0-9]-"$g".sh 2>/dev/null | head -1)"
  if [ -z "$match" ]; then
    CURRENT_GROUP="$g"; skip "group" "no group file for '$g'"; return
  fi
  CURRENT_GROUP="$g"
  # A destructive group is skipped wholesale unless enabled.
  if is_destructive "$g" && [ "$DESTRUCTIVE" -ne 1 ]; then
    skip "group" "destructive; pass --destructive to run"
    return
  fi
  # shellcheck disable=SC1090
  . "$match"
  # By convention each file defines group_<name>.
  if declare -F "group_$g" >/dev/null 2>&1; then
    "group_$g"
  else
    skip "group" "group file '$match' defines no group_$g function"
  fi
}

echo "# hack-box tests  (destructive=$DESTRUCTIVE live=$LIVE)  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
for g in "${SELECTED[@]}"; do
  run_group "$g"
done

echo "# summary: ${PASS_N} passed, ${FAIL_N} failed, ${SKIP_N} skipped"
if [ "$FAIL_N" -gt 0 ]; then
  echo "# failed:${FAILED_NAMES}"
  exit 1
fi
exit 0
