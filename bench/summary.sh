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
    for k in ("claude_mb", "node_mb", "chromium_mb"):
        if k in rows[0]:
            print(f"{k:9s} avg {sum(f(k))/n:5.0f}   max {max(f(k)):5.0f} MB")

for d in sorted(glob.glob(os.path.join(run, "s[0-9]*"))):
    tot = dict(turns=0, cache_r=0, cache_w=0, inp=0, out=0, cost=0.0); errs = []
    for rf in sorted(glob.glob(os.path.join(d, "result*.json"))):
        try: j = json.load(open(rf))
        except Exception as e: errs.append(os.path.basename(rf)); continue
        u = j.get("usage", {})
        tot["turns"] += j.get("num_turns", 0); tot["cost"] += j.get("total_cost_usd", 0)
        tot["inp"] += u.get("input_tokens", 0); tot["out"] += u.get("output_tokens", 0)
        tot["cache_r"] += u.get("cache_read_input_tokens", 0); tot["cache_w"] += u.get("cache_creation_input_tokens", 0)
        if j.get("is_error"): errs.append(os.path.basename(rf))
    wp = os.path.join(d, "wall_seconds")
    wall = open(wp).read().strip() if os.path.exists(wp) else "?"
    print(f"{os.path.basename(d)}: turns {tot['turns']}  wall {wall}s  in {tot['inp']}  cache_w {tot['cache_w']}  "
          f"cache_r {tot['cache_r']}  out {tot['out']}  ${tot['cost']:.2f}" + (f"  errors: {errs}" if errs else ""))
PY
