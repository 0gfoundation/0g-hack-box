#!/bin/bash
# Token usage per attendee session, pulled from the boxes without touching them.
#   hub/deploy/token-usage.sh run [seconds]   poll every box every N s (default 60), keep the ledger
#   hub/deploy/token-usage.sh report          totals per session, per box and for the event
# Each poll copies the attendee's OpenCode database (~/.local/share/opencode/opencode.db and its
# WAL) to a root-only temp folder on the box and sums the assistant messages' tokens from the
# copy, so the running OpenCode is never opened or locked. The hub session (id, attendee name)
# comes from the hub agent's /var/lib/hackbox/hub/current.json. Read only on the boxes.
# A box wipes the home at the end of a session, so the ledger keeps the highest total seen per
# session; polling every minute captures all but the last minute of a session.
# Ledger: ~/hackbox-token-usage.json (box -> session -> totals). Uses ssh hackbox1..N.
set -uo pipefail
LEDGER=${TOKEN_LEDGER:-$HOME/hackbox-token-usage.json}
BOXES=${BOXES:-"hackbox1 hackbox2 hackbox3 hackbox4"}

probe() {   # prints one JSON line for the box's current session, or nothing
  ssh -o ConnectTimeout=15 -o BatchMode=yes "$1" 'sudo bash -s' <<'EOS' 2>/dev/null
db=/home/hacker/.local/share/opencode/opencode.db
state=$(cat /run/hackbox/state 2>/dev/null)
T=$(mktemp -d) || exit 0
trap 'rm -rf "$T"' EXIT
[ -f "$db" ] && cp "$db" "$db-wal" "$db-shm" "$T/" 2>/dev/null
cp /var/lib/hackbox/hub/current.json "$T/cur.json" 2>/dev/null
python3 - "$T" "$state" <<'PY'
import json, os, sqlite3, sys, time
t, state = sys.argv[1], sys.argv[2]
try:
    cur = json.load(open(os.path.join(t, "cur.json")))
except (OSError, ValueError):
    cur = {}
tot = {"input": 0, "output": 0, "reasoning": 0, "cache_read": 0, "cache_write": 0, "replies": 0}
models = {}
p = os.path.join(t, "opencode.db")
if os.path.exists(p):
    try:
        db = sqlite3.connect(p)
        for (d,) in db.execute("select data from message"):
            r = json.loads(d)
            if r.get("role") != "assistant":
                continue
            k = r.get("tokens") or {}
            c = k.get("cache") or {}
            tot["input"] += k.get("input", 0); tot["output"] += k.get("output", 0)
            tot["reasoning"] += k.get("reasoning", 0)
            tot["cache_read"] += c.get("read", 0); tot["cache_write"] += c.get("write", 0)
            tot["replies"] += 1
            m = r.get("modelID") or "?"
            models[m] = models.get(m, 0) + k.get("input", 0) + k.get("output", 0) + k.get("reasoning", 0)
    except sqlite3.Error:
        pass
print(json.dumps({"state": state, "session_id": cur.get("session_id") or cur.get("local_id"),
                  "name": cur.get("name", ""), "started_at": cur.get("started_at", 0),
                  "closed": bool(cur.get("closed_at")), "tokens": tot, "models": models,
                  "polled_at": int(time.time())}))
PY
EOS
}

merge() {   # merge one box's probe ($2) into the ledger
  python3 - "$LEDGER" "$1" "$2" <<'PY'
import json, os, sys
path, box, line = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    led = json.load(open(path))
except (OSError, ValueError):
    led = {}
p = json.loads(line)
sid = p.get("session_id")
if not sid or p["tokens"]["replies"] == 0:
    sys.exit(0)                      # idle box, or a session that has not used the agent yet
s = led.setdefault(box, {}).setdefault(sid, {"name": p["name"], "started_at": p["started_at"],
                                             "tokens": {}, "models": {}})
s["name"] = p["name"] or s["name"]
s["last_seen"] = p["polled_at"]
for k, v in p["tokens"].items():
    s["tokens"][k] = max(s["tokens"].get(k, 0), v)
for k, v in p["models"].items():
    s["models"][k] = max(s["models"].get(k, 0), v)
tmp = path + ".tmp"
json.dump(led, open(tmp, "w"), indent=1)
os.chmod(tmp, 0o600)
os.replace(tmp, path)
PY
}

report() {
  python3 - "$LEDGER" <<'PY'
import json, sys, time
try:
    led = json.load(open(sys.argv[1]))
except (OSError, ValueError):
    print("no data yet"); sys.exit(0)
keys = ("input", "output", "reasoning", "cache_read")
grand = dict.fromkeys(keys, 0); n = 0
print("%-9s %-20s %-6s %10s %9s %10s %11s" % ("box", "attendee", "start", "input", "output", "reasoning", "cache read"))
for box in sorted(led):
    bt = dict.fromkeys(keys, 0)
    for sid, s in sorted(led[box].items(), key=lambda kv: kv[1].get("started_at", 0)):
        t = s["tokens"]; n += 1
        st = time.strftime("%H:%M", time.localtime(s.get("started_at") or 0))
        print("%-9s %-20s %-6s %10d %9d %10d %11d" % (box, (s.get("name") or "?")[:20], st,
              t.get("input", 0), t.get("output", 0), t.get("reasoning", 0), t.get("cache_read", 0)))
        for k in keys:
            bt[k] += t.get(k, 0); grand[k] += t.get(k, 0)
    print("%-9s %-20s %-6s %10d %9d %10d %11d" % (box, "  = box total", "", *[bt[k] for k in keys]))
print("%-9s %-20s %-6s %10d %9d %10d %11d" % ("ALL", "%d sessions" % n, "", *[grand[k] for k in keys]))
PY
}

case "${1:-report}" in
  run)
    every=${2:-60}
    echo "polling $BOXES every ${every}s into $LEDGER (Ctrl-C to stop)"
    while :; do
      for b in $BOXES; do l=$(probe "$b"); [ -n "$l" ] && merge "$b" "$l"; done
      sleep "$every"
    done ;;
  once) for b in $BOXES; do l=$(probe "$b"); [ -n "$l" ] && merge "$b" "$l"; done; report ;;
  report) report ;;
  *) echo "usage: $0 run [seconds] | once | report" >&2; exit 2 ;;
esac
