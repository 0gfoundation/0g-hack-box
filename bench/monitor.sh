#!/usr/bin/env bash
# Sample system load to CSV until killed.
#   bench/monitor.sh <out.csv> [interval_seconds]
# Columns: ts, load1, cpu_pct, mem_used_mb, mem_avail_mb, swap_used_mb,
#          claude_mb, node_mb, chromium_mb   (summed RSS by process name)
set -euo pipefail

OUT=${1:?output csv}
INTERVAL=${2:-5}
mkdir -p "$(dirname "$OUT")"

read_cpu() { awk '/^cpu /{print $2+$3+$4+$6+$7+$8, $5}' /proc/stat; }
rss_mb() { ps -eo rss,comm | awk -v n="$1" '$2==n{s+=$1} END{printf "%d", s/1024}'; }

echo "ts,load1,cpu_pct,mem_used_mb,mem_avail_mb,swap_used_mb,claude_mb,node_mb,chromium_mb" > "$OUT"
read -r busy0 idle0 < <(read_cpu)
while sleep "$INTERVAL"; do
  read -r busy1 idle1 < <(read_cpu)
  total=$(( (busy1 - busy0) + (idle1 - idle0) ))
  cpu=$(( total > 0 ? 100 * (busy1 - busy0) / total : 0 ))
  busy0=$busy1; idle0=$idle1

  load1=$(cut -d' ' -f1 /proc/loadavg)
  read -r mem_used mem_avail < <(free -m | awk '/^Mem:/{print $3, $7}')
  swap_used=$(free -m | awk '/^Swap:/{print $3}')

  echo "$(date +%s),$load1,$cpu,$mem_used,$mem_avail,$swap_used,$(rss_mb claude),$(rss_mb node),$(rss_mb chromium)" >> "$OUT"
done
