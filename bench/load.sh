#!/usr/bin/env bash
# Run one load profile and record it.
#   bench/load.sh <label> <claude_sessions> <dev_servers> <chromium_tabs>
# Dev servers and tabs need a built app at ~/bench/template (copy one from a
# finished session dir). Everything lands in ~/bench/runs/<label>/.
set -euo pipefail

LABEL=${1:?label}; N_CLAUDE=${2:-1}; N_DEV=${3:-0}; N_TABS=${4:-0}
# HOLD=<seconds>: keep the load up at least this long by looping sessions.
# TABS_FILE=<path>: one URL per line, replaces the default tab list.
HOLD=${HOLD:-0}
HERE="$(cd "$(dirname "$0")" && pwd)"
RUN=~/bench/runs/$LABEL
TEMPLATE=~/bench/template
rm -rf "$RUN" && mkdir -p "$RUN"
pids=()
cleanup() { kill "${pids[@]}" 2>/dev/null || true; pkill -f "vite --port 51" 2>/dev/null || true; pkill -x chromium 2>/dev/null || true; }
trap cleanup EXIT

"$HERE/monitor.sh" "$RUN/monitor.csv" 5 & pids+=($!)
sleep 15   # settle, gives a pre-load reference in the csv

for i in $(seq 1 "$N_DEV"); do
  d="$RUN/dev$i"; cp -r "$TEMPLATE" "$d"
  (cd "$d" && npx vite --port $((5172 + i)) --strictPort >/dev/null 2>&1) & pids+=($!)
done
[ "$N_DEV" -gt 0 ] && sleep 10

# Tabs cycle through the dev servers and the sites people actually keep open.
# Dialog-suppressing flags come from CHROMIUM_FLAGS in /etc/environment.
if [ "$N_TABS" -gt 0 ]; then
  export DISPLAY=:0
  urls=()
  for i in $(seq 1 "$N_DEV"); do urls+=("http://localhost:$((5172 + i))/"); done
  if [ -n "${TABS_FILE:-}" ]; then
    while read -r u; do [ -n "$u" ] && urls+=("$u"); done < "$TABS_FILE"
  else
    urls+=(https://docs.0g.ai/ https://github.com/0gfoundation https://code.claude.com/docs/en/overview https://vite.dev/guide/)
  fi
  pkill -x chromium 2>/dev/null || true; sleep 1
  for i in $(seq 1 "$N_TABS"); do
    url="${urls[$(( (i - 1) % ${#urls[@]} ))]}"
    chromium "$url" >/dev/null 2>&1 &
    sleep 3
  done
fi

t0=$(date +%s); round=0
while :; do
  round=$((round + 1)); spids=()
  for i in $(seq 1 "$N_CLAUDE"); do
    d="$RUN/s$i"; [ "$round" -gt 1 ] && d="$RUN/s${i}r$round"
    "$HERE/session.sh" "$d" & spids+=($!)
    sleep 3
  done
  wait "${spids[@]}"
  [ $(( $(date +%s) - t0 )) -ge "$HOLD" ] && break
done
sleep 15   # tail, catches post-session settle

cleanup; trap - EXIT
"$HERE/summary.sh" "$RUN" | tee "$RUN/summary.txt"
