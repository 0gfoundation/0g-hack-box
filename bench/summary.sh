#!/usr/bin/env bash
# Summarise a run directory: monitor CSV peaks and per-session token usage.
#   bench/summary.sh <rundir>
set -euo pipefail
RUN=${1:?rundir}

python3 - "$RUN" <<'PY'
import csv, json, sys, glob, os
run = sys.argv[1]

rows = list(csv.DictReader(open(os.path.join(run, "monitor.csv"))))
if rows:
    f = lambda k: [float(r[k]) for r in rows]
    n = len(rows)
    print(f"samples {n}  ({n*5}s)")
    print(f"cpu%      avg {sum(f('cpu_pct'))/n:5.1f}   max {max(f('cpu_pct')):5.0f}")
    print(f"load1     avg {sum(f('load1'))/n:5.2f}   max {max(f('load1')):5.2f}")
    print(f"mem used  avg {sum(f('mem_used_mb'))/n:5.0f}   max {max(f('mem_used_mb')):5.0f} MB")
    print(f"mem avail min {min(f('mem_avail_mb')):5.0f} MB")
    print(f"swap used max {max(f('swap_used_mb')):5.0f} MB")

for rf in sorted(glob.glob(os.path.join(run, "s*/result.json"))):
    d = os.path.dirname(rf)
    try:
        j = json.load(open(rf))
    except Exception as e:
        print(f"{os.path.basename(d)}: no result ({e})"); continue
    u = j.get("usage", {})
    wall = open(os.path.join(d, "wall_seconds")).read().strip() if os.path.exists(os.path.join(d, "wall_seconds")) else "?"
    print(f"{os.path.basename(d)}: turns {j.get('num_turns')}  wall {wall}s  err {j.get('is_error')}  "
          f"in {u.get('input_tokens',0)}  cache_w {u.get('cache_creation_input_tokens',0)}  "
          f"cache_r {u.get('cache_read_input_tokens',0)}  out {u.get('output_tokens',0)}  "
          f"${j.get('total_cost_usd',0):.3f}")
PY
