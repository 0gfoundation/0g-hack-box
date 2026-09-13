#!/usr/bin/env bash
# Sample system load to CSV until killed.
#   bench/monitor.sh results/n100-16gb/idle.csv [interval_seconds]
# Columns: ts, load1, cpu_pct, mem_used_mb, mem_avail_mb, swap_used_mb, top_proc
set -euo pipefail

OUT=${1:?output csv}
INTERVAL=${2:-5}
mkdir -p "$(dirname "$OUT")"

read_cpu() { awk '/^cpu /{print $2+$3+$4+$6+$7+$8, $5}' /proc/stat; }

echo "ts,load1,cpu_pct,mem_used_mb,mem_avail_mb,swap_used_mb,top_proc" > "$OUT"
read -r busy0 idle0 < <(read_cpu)
while sleep "$INTERVAL"; do
  read -r busy1 idle1 < <(read_cpu)
  total=$(( (busy1 - busy0) + (idle1 - idle0) ))
  cpu=$(( total > 0 ? 100 * (busy1 - busy0) / total : 0 ))
  busy0=$busy1; idle0=$idle1

  load1=$(cut -d' ' -f1 /proc/loadavg)
  read -r mem_used mem_avail < <(free -m | awk '/^Mem:/{print $3, $7}')
  swap_used=$(free -m | awk '/^Swap:/{print $3}')
  top=$(ps -eo comm,%mem --sort=-%mem | awk 'NR==2{print $1}')

  echo "$(date +%s),$load1,$cpu,$mem_used,$mem_avail,$swap_used,$top" >> "$OUT"
done
