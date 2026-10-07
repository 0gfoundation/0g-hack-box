#!/usr/bin/env bash
# The 0G Pay partner regrant settings live in hub/.env.pay (git ignored, mode 600) so the
# signer's private key never goes through a chat or a shell history. This script uses them:
#
#   hub/deploy/pay-env.sh check            parse the file, print the settings (key masked)
#   hub/deploy/pay-env.sh local [port]     run a hub on 127.0.0.1:<port> (8299) with these
#                                          settings, one ended test session per wallet in
#                                          PAY_TEST_WALLETS, and print the page URLs
#   hub/deploy/pay-env.sh grant <wallet>   on the running local hub: grant to one wallet
#   hub/deploy/pay-env.sh jarvis           write the pay block into jarvis's config.json
#                                          (backup kept) and restart the hub there
#   hub/deploy/pay-env.sh off              remove the pay block on jarvis and restart
#   hub/deploy/pay-env.sh ledger           lot total, granted by the hub on jarvis, granted
#                                          elsewhere (PAY_MANUAL_SPENT_MICROS), remaining
#
# .env.pay keys: PAY_API_URL, PAY_SOURCE_LOT_ID, PAY_SIGNER_KEY, PAY_AMOUNT_MICROS,
# PAY_MAX_PER_WALLET, PAY_REF_TAG, PAY_EXPIRES_AT, PAY_SPEND_URL, PAY_EXPIRY_LABEL,
# PAY_TEST_WALLETS (space separated).
set -euo pipefail
HUB_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${PAY_ENV_FILE:-$HUB_DIR/.env.pay}"
HOST="${JARVIS_HOST:-jarvis}"
REMOTE_CONFIG="/Users/jarvis/hackbox-hub/config.json"
LABEL="com.udhay.hackbox-hub"

[ -f "$ENV_FILE" ] || { echo "missing $ENV_FILE (copy hub/.env.pay.example)" >&2; exit 1; }
[ "$(stat -f %Lp "$ENV_FILE" 2>/dev/null || stat -c %a "$ENV_FILE")" = "600" ] || chmod 600 "$ENV_FILE"
set -a; . "$ENV_FILE"; set +a
PAY_AMOUNT_MICROS="${PAY_AMOUNT_MICROS:-10000000}"
PAY_MAX_PER_WALLET="${PAY_MAX_PER_WALLET:-2}"
PAY_REF_TAG="${PAY_REF_TAG:-hackbox}"
PAY_EXPIRES_AT="${PAY_EXPIRES_AT:-}"
PAY_SPEND_URL="${PAY_SPEND_URL:-https://pc.0g.ai}"
PAY_EXPIRY_LABEL="${PAY_EXPIRY_LABEL:-}"

pay_json() {
  python3 - "$PAY_API_URL" "$PAY_SOURCE_LOT_ID" "$PAY_SIGNER_KEY" "$PAY_AMOUNT_MICROS" "$PAY_MAX_PER_WALLET" "$PAY_REF_TAG" "$PAY_EXPIRES_AT" "$PAY_SPEND_URL" "$PAY_EXPIRY_LABEL" <<'EOF'
import json, sys
a = sys.argv[1:]
print(json.dumps({"api_url": a[0], "source_lot_id": a[1], "signer_key": a[2], "amount_micros": int(a[3]),
                  "max_per_wallet": int(a[4]), "ref_tag": a[5], "expires_at": a[6], "spend_url": a[7], "expiry_label": a[8]}))
EOF
}

check() {
  local k="${PAY_SIGNER_KEY:-}"
  echo "api_url        ${PAY_API_URL:-<missing>}"
  echo "source_lot_id  ${PAY_SOURCE_LOT_ID:-<missing>} ($(printf %s "${PAY_SOURCE_LOT_ID:-}" | wc -c | tr -d ' ') chars, want 32 hex)"
  echo "signer_key     $([ -n "$k" ] && echo "${k:0:6}…${k: -4} (${#k} chars, want 0x + 64 hex)" || echo "<missing>")"
  echo "amount_micros  $PAY_AMOUNT_MICROS (\$$(python3 -c "print($PAY_AMOUNT_MICROS/1e6)"))"
  echo "max_per_wallet $PAY_MAX_PER_WALLET   ref_tag $PAY_REF_TAG   expires_at ${PAY_EXPIRES_AT:-<lot expiry>}"
  echo "spend_url      $PAY_SPEND_URL   expiry_label ${PAY_EXPIRY_LABEL:-<hidden>}"
  echo "test wallets   ${PAY_TEST_WALLETS:-<none>}"
  [ -n "${PAY_API_URL:-}" ] && [ -n "${PAY_SOURCE_LOT_ID:-}" ] && [ -n "$k" ] || { echo "fill in the missing values first" >&2; exit 1; }
}

local_run() {
  check
  local port="${1:-8299}" run="${TMPDIR:-/tmp}/hackbox-pay-local"
  rm -rf "$run"; mkdir -p "$run/data"
  (cd "$HUB_DIR" && go build -o "$run/hub" .)
  python3 - "$port" "$(pay_json)" > "$run/config.json" <<'EOF'
import json, sys
print(json.dumps({"public_base_url": "http://127.0.0.1:" + sys.argv[1], "boxes": {"localtesttoken": "hackbox-local"},
                  "github": {"org": "x", "token": ""}, "download_days": 7, "pay": json.loads(sys.argv[2])}, indent=1))
EOF
  chmod 600 "$run/config.json"
  pkill -f "$run/hub -config" 2>/dev/null || true
  (cd "$run" && nohup ./hub -config "$run/config.json" -data "$run/data" -listen "127.0.0.1:$port" > "$run/hub.log" 2>&1 &)
  sleep 2
  grep -q "listening" "$run/hub.log" || { cat "$run/hub.log"; exit 1; }
  grep "pay:" "$run/hub.log" | head -1
  echo "$port" > "$run/port"
  local base="http://127.0.0.1:$port" H="Authorization: Bearer localtesttoken" J="Content-Type: application/json"
  : > "$run/sessions"
  for w in ${PAY_TEST_WALLETS:-}; do
    r=$(curl -s -X POST "$base/api/v1/sessions" -H "$H" -H "$J" \
      -d "{\"name\":\"Test $w\",\"agent\":\"claude\",\"minutes\":30,\"started_at\":$(date +%s),\"local_code\":\"\"}")
    id=$(printf %s "$r" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
    tok=$(printf %s "$r" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
    curl -s -X POST "$base/api/v1/sessions/$id/end" -H "$H" -H "$J" -d "{\"ended_at\":$(date +%s),\"reason\":\"done\",\"empty\":true}" >/dev/null
    echo "$w $tok" >> "$run/sessions"
    echo "page for $w:  $base/d/$tok"
  done
  echo "log: $run/hub.log   stop: pkill -f $run/hub"
}

grant() {
  local w="${1:?wallet}" run="${TMPDIR:-/tmp}/hackbox-pay-local"
  local port; port=$(cat "$run/port")
  local tok; tok=$(awk -v w="$w" 'tolower($1)==tolower(w) {print $2}' "$run/sessions" | head -1)
  if [ -z "$tok" ]; then  # a wallet not in the list: make it a session
    local base="http://127.0.0.1:$port" H="Authorization: Bearer localtesttoken" J="Content-Type: application/json"
    r=$(curl -s -X POST "$base/api/v1/sessions" -H "$H" -H "$J" -d "{\"name\":\"Test $w\",\"agent\":\"claude\",\"minutes\":30,\"started_at\":$(date +%s),\"local_code\":\"\"}")
    id=$(printf %s "$r" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
    tok=$(printf %s "$r" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
    curl -s -X POST "$base/api/v1/sessions/$id/end" -H "$H" -H "$J" -d "{\"ended_at\":$(date +%s),\"reason\":\"done\",\"empty\":true}" >/dev/null
    echo "$w $tok" >> "$run/sessions"
  fi
  curl -s -X POST "http://127.0.0.1:$port/d/$tok/credit" -H "Content-Type: application/json" -d "{\"wallet\":\"$w\"}" -w "  HTTP %{http_code}\n"
  grep "pay:" "$run/hub.log" | tail -1 || true
}

jarvis() {
  check
  local pj; pj=$(pay_json)
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "cd $(dirname "$REMOTE_CONFIG") && cp config.json config.json.bak-\$(date +%Y%m%d-%H%M%S) && \
    /usr/bin/python3 -c 'import json,sys; c=json.load(open(\"config.json\")); c[\"pay\"]=json.loads(sys.argv[1]); json.dump(c, open(\"config.json\",\"w\"), indent=2)' '$pj' && \
    chmod 600 config.json && launchctl kickstart -k gui/501/$LABEL && sleep 2 && \
    (curl -fsS http://127.0.0.1:8210/healthz; echo) && grep 'pay:' ~/Library/Logs/hackbox-hub.err.log | tail -1"
}

ledger() {
  local total="${PAY_LOT_TOTAL_MICROS:-0}" manual="${PAY_MANUAL_SPENT_MICROS:-0}"
  local hub; hub=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" \
    "/usr/bin/sqlite3 -separator ' ' /Users/jarvis/hackbox-hub/data/hub.db \"SELECT COUNT(*), COALESCE(SUM(amount_micros),0) FROM credits WHERE outcome IN ('granted','duplicate')\"" 2>/dev/null || echo "0 0")
  python3 -c '
import sys
total, manual, n, hub = (int(x) for x in sys.argv[1:5])
usd = lambda m: "$%.2f" % (m / 1e6)
print("lot total            " + usd(total))
print("granted by the hub   " + usd(hub) + "  (" + str(n) + " grants on jarvis)")
print("granted elsewhere    " + usd(manual) + "  (PAY_MANUAL_SPENT_MICROS: tests, one-off grants)")
print("remaining            " + usd(total - hub - manual))
' "$total" "$manual" $hub
}

off() {
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$HOST" "cd $(dirname "$REMOTE_CONFIG") && cp config.json config.json.bak-\$(date +%Y%m%d-%H%M%S) && \
    /usr/bin/python3 -c 'import json; c=json.load(open(\"config.json\")); c.pop(\"pay\", None); json.dump(c, open(\"config.json\",\"w\"), indent=2)' && \
    chmod 600 config.json && launchctl kickstart -k gui/501/$LABEL && sleep 2 && curl -fsS http://127.0.0.1:8210/healthz && echo"
}

case "${1:-}" in
  check) check ;;
  local) local_run "${2:-}" ;;
  grant) grant "${2:-}" ;;
  jarvis) jarvis ;;
  off) off ;;
  ledger) ledger ;;
  *) sed -n '2,16p' "$0"; exit 2 ;;
esac
