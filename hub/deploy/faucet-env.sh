#!/usr/bin/env bash
# The faucet service-account key lives in hub/.env.faucet (git ignored, mode 600) so it never
# goes through a chat or a shell history. This script uses it:
#
#   hub/deploy/faucet-env.sh check     parse the file, print the settings (key masked) and
#                                      what the key may do (read-only probes, sends nothing)
#   hub/deploy/faucet-env.sh jarvis    write the faucet block into jarvis's config.json
#                                      (backup kept, the key goes over stdin) and restart
#   hub/deploy/faucet-env.sh off       remove the faucet block on jarvis and restart
#   hub/deploy/faucet-env.sh ledger    testnet 0G the hub on jarvis sent, per status and box
#
# .env.faucet keys: FAUCET_API_KEY (required), FAUCET_API_URL, FAUCET_PROMO_CODE,
# FAUCET_AMOUNT_OG, FAUCET_SESSION_MAX_OG, FAUCET_WALLET_MAX_OG, FAUCET_BOX_HOURLY_MAX_OG,
# FAUCET_EVENT_BUDGET_OG, FAUCET_EVENT_SINCE, FAUCET_IP_PER_MINUTE, FAUCET_REF_TAG.
# Unset values take the hub's defaults (see DEPLOYMENT.md).
set -euo pipefail
HUB_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${FAUCET_ENV_FILE:-$HUB_DIR/.env.faucet}"
HOST="${JARVIS_HOST:-jarvis}"
REMOTE_CONFIG="/Users/jarvis/hackbox-hub/config.json"
LABEL="com.udhay.hackbox-hub"

[ -f "$ENV_FILE" ] || { echo "missing $ENV_FILE" >&2; exit 1; }
[ "$(stat -f %Lp "$ENV_FILE" 2>/dev/null || stat -c %a "$ENV_FILE")" = "600" ] || chmod 600 "$ENV_FILE"
set -a; . "$ENV_FILE"; set +a
FAUCET_API_URL="${FAUCET_API_URL:-https://faucet-api.udhaykumarbala.dev}"

faucet_json() {
  python3 <<'EOF'
import json, os
e = os.environ.get
out = {"api_url": e("FAUCET_API_URL"), "api_key": e("FAUCET_API_KEY", "")}
for key, name, conv in [("promo_code", "FAUCET_PROMO_CODE", str), ("amount_og", "FAUCET_AMOUNT_OG", float),
                        ("session_max_og", "FAUCET_SESSION_MAX_OG", float), ("wallet_max_og", "FAUCET_WALLET_MAX_OG", float),
                        ("box_hourly_max_og", "FAUCET_BOX_HOURLY_MAX_OG", float),
                        ("event_budget_og", "FAUCET_EVENT_BUDGET_OG", float), ("event_since", "FAUCET_EVENT_SINCE", str),
                        ("ip_per_minute", "FAUCET_IP_PER_MINUTE", int), ("ref_tag", "FAUCET_REF_TAG", str)]:
    if e(name):
        out[key] = conv(e(name))
print(json.dumps(out))
EOF
}

check() {
  local k="${FAUCET_API_KEY:-}"
  [ -n "$k" ] || { echo "FAUCET_API_KEY is empty in $ENV_FILE" >&2; exit 1; }
  echo "api_url  $FAUCET_API_URL"
  echo "api_key  set (${#k} chars, not shown)"
  faucet_json | python3 -c 'import json,sys; c=json.load(sys.stdin); c.pop("api_key"); print("settings", json.dumps(c))'
  export FAUCET_API_URL
  python3 <<'EOF'
import json, os, urllib.request, urllib.error
base, key = os.environ["FAUCET_API_URL"].rstrip("/"), os.environ["FAUCET_API_KEY"]
def call(method, path, body=None, auth=True):
    req = urllib.request.Request(base + path, data=body, method=method, headers={"Content-Type": "application/json"})
    if auth:
        req.add_header("Authorization", "Bearer " + key)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
code, info = call("GET", "/api/info", auth=False)
if code == 200:
    i = json.loads(info)
    print("faucet   %s (chain %s), drip %s 0G per %s h, balance %s" % (i["network"]["name"], i["network"]["chain_id"],
          int(i["drip"]["base_amount"]) / 1e18, i["drip"]["rate_limit_hours"], i["faucet_balance"]))
code, _ = call("GET", "/v1/transfers/00000000-0000-0000-0000-000000000000")
print("read     " + {404: "ok (transfers:read)", 403: "missing transfers:read", 401: "key rejected"}.get(code, "HTTP %d" % code))
code, body = call("POST", "/v1/promo-codes", b"{")  # invalid body: never mints
print("mint     " + ("ok (promo:mint with a ceiling)" if code == 400 else "no (%s)" % body.strip()))
EOF
}

jarvis() {
  check
  faucet_json | ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "cd $(dirname "$REMOTE_CONFIG") && cp config.json config.json.bak-\$(date +%Y%m%d-%H%M%S) && \
    /usr/bin/python3 -c 'import json,sys; c=json.load(open(\"config.json\")); c[\"faucet\"]=json.load(sys.stdin); json.dump(c, open(\"config.json\",\"w\"), indent=2)' && \
    chmod 600 config.json && launchctl kickstart -k gui/501/$LABEL && sleep 2 && \
    (curl -fsS http://127.0.0.1:8210/healthz; echo) && grep 'faucet:' ~/Library/Logs/hackbox-hub.err.log | tail -1"
}

off() {
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "cd $(dirname "$REMOTE_CONFIG") && cp config.json config.json.bak-\$(date +%Y%m%d-%H%M%S) && \
    /usr/bin/python3 -c 'import json; c=json.load(open(\"config.json\")); c.pop(\"faucet\", None); json.dump(c, open(\"config.json\",\"w\"), indent=2)' && \
    chmod 600 config.json && launchctl kickstart -k gui/501/$LABEL && sleep 2 && curl -fsS http://127.0.0.1:8210/healthz && echo"
}

ledger() {
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" \
    "/usr/bin/sqlite3 -header -column /Users/jarvis/hackbox-hub/data/hub.db \"SELECT box, status, COUNT(*) AS n, printf('%.3f', SUM(amount_milli)/1000.0) AS og FROM fundings GROUP BY box, status ORDER BY box, status\""
}

case "${1:-}" in
  check) check ;;
  jarvis) jarvis ;;
  off) off ;;
  ledger) ledger ;;
  *) sed -n '2,16p' "$0"; exit 2 ;;
esac
