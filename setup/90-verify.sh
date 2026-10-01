#!/usr/bin/env bash
# Bootstrap's last step: run the non-destructive test groups and print the
# summary. A failing check must NOT abort the install (a box with one failing
# check is still more useful than an aborted install), so this never exits
# nonzero on FAIL; it prints a clear banner instead. Runs as the admin user
# (who has passwordless sudo), same as the other setup scripts.
set -uo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
RUN="$HERE/tests/run.sh"

if [ ! -x "$RUN" ]; then
  echo "90-verify: tests/run.sh not found at $RUN, skipping verification" >&2
  exit 0
fi

# Non-destructive groups only. run.sh would skip the destructive ones anyway,
# but naming them keeps the output focused and fast at install time.
# Not called GROUPS: that is a bash builtin (the caller's group ids), and an
# assignment to it is silently ignored.
VERIFY_GROUPS="install accounts home states archive network agents desktop cli"

echo "==> 90-verify: running non-destructive checks"
# No set -e anywhere below: this script must reach the banner and exit 0.
# HB_VERIFY_READONLY: skip the checks that start and reset a session. A reset
# stops LightDM, and bootstrap may be running inside the admin's own desktop
# session (setup/60-hacker.sh never restarts LightDM for the same reason).
out="$(HB_VERIFY_READONLY=1 bash "$RUN" $VERIFY_GROUPS 2>&1)"
rc=$?

printf '%s\n' "$out"

# Extract the summary line for the banner.
summary="$(printf '%s\n' "$out" | grep '^# summary:' | tail -1 || true)"
fails="$(printf '%s\n' "$out" | grep -c '^FAIL ' || true)"

echo
if [ "$rc" -eq 0 ]; then
  echo "############################################################"
  echo "#  hack-box verification: ALL NON-DESTRUCTIVE CHECKS PASSED  "
  echo "#  ${summary:-# summary: (none)}"
  echo "############################################################"
else
  echo "############################################################"
  echo "#  hack-box verification: ${fails} CHECK(S) FAILED           "
  echo "#  ${summary:-# summary: (none)}"
  echo "#  The install is NOT aborted. Review the FAIL lines above  "
  echo "#  and re-run:  bash $RUN <group>                            "
  echo "############################################################"
fi

# Never fail the bootstrap on a check failure.
exit 0
