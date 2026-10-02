#!/bin/bash
# Off-box test of the hub agent (files/hub/hackbox-hubagent) against a real hub
# built from hub/. Runs on any machine with Go and Python 3 (the Mac is fine).
# Fakes /run/hackbox, the archive dir, the attendee's name file and the
# `hackbox` CLI in a temporary directory, then walks one session:
# start, heartbeat, extend request, staff approval, extend command, end,
# archive upload, download page, zip download. Prints PASS/FAIL per check.
#   tests/hubagent-test.sh
set -uo pipefail
REPO=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d); trap 'kill $HUB_PID $AGENT_PID 2>/dev/null; rm -rf "$T"' EXIT
PORT=$(( 20000 + RANDOM % 20000 ))
fails=0
ok()   { echo "PASS $*"; }
bad()  { echo "FAIL $*"; fails=$((fails + 1)); }
check() { local what=$1; shift; if "$@"; then ok "$what"; else bad "$what"; fi; }
wait_for() { local i; for i in $(seq 40); do eval "$1" && return 0; sleep 0.5; done; return 1; }

# --- hub ---
(cd "$REPO/hub" && go build -o "$T/hackbox-hub" .) || { echo "hub build failed"; exit 1; }
mkdir -p "$T/hubdata"
cat > "$T/hub.json" <<EOF
{"public_base_url":"http://127.0.0.1:$PORT","boxes":{"testtoken-0123456789abcdef":"hackbox9"},
 "enroll_token":"enroll-0123456789abcdef","name_prefix":"hackbox",
 "github":{"org":"","token":""},"download_days":7}
EOF
"$T/hackbox-hub" -listen 127.0.0.1:$PORT -data "$T/hubdata" -config "$T/hub.json" 2>"$T/hub.log" &
HUB_PID=$!
wait_for "curl -fs http://127.0.0.1:$PORT/healthz >/dev/null" || { echo "hub did not start"; cat "$T/hub.log"; exit 1; }

# --- fake box ---
RUN=$T/run; mkdir -p "$RUN" "$T/archive" "$T/state" "$T/home"
printf "HUB_URL='http://127.0.0.1:%s'\nHUB_TOKEN='testtoken-0123456789abcdef'\n" "$PORT" > "$T/hub.conf"
chmod 600 "$T/hub.conf"
cat > "$T/hackbox" <<EOF
#!/bin/bash
# fake CLI: records commands, extends the fake session
echo "\$*" >> "$T/commands.log"
case "\$1" in
  status) echo '{"state":"'"\$(cat $RUN/state)"'"}' ;;
  extend) e=\$(cat $RUN/session-end); echo \$(( e + \$2 * 60 )) > $RUN/session-end ;;
  end) echo ending > $RUN/state ;;
  secret) cat > "$T/secret-\$3" ;;
esac
EOF
chmod +x "$T/hackbox"
export HB_RUN=$RUN HB_STATE_DIR=$T/state HB_ARCHIVE_DIR=$T/archive HB_HUB_CONF=$T/hub.conf \
  HB_NAME_FILE=$T/home/attendee HB_HACKER_UID=$(id -u) HB_HACKBOX_CMD=$T/hackbox \
  HB_SKIP_PERM_CHECK=1 HB_TICK=0.3 HB_BEAT_EVERY=0.5 HB_JOB_BACKOFF_MAX=1 HB_HUB_BACKOFF_MAX=1
echo idle > "$RUN/state"
python3 "$REPO/files/hub/hackbox-hubagent" 2>"$T/agent.log" &
AGENT_PID=$!
sleep 1

# --- session starts ---
printf 'Ada  Lovelace\x07\nclaude\nada@example.com\n@ada_lovelace\n' > "$T/home/attendee"
echo $(( $(date +%s) + 1800 )) > "$RUN/session-end"
echo active > "$RUN/state"
check "agent registers the session and writes hub-url" wait_for "[ -s $RUN/hub-url ]"
check "qr.png is a PNG" wait_for "file $RUN/qr.png 2>/dev/null | grep -q 'PNG image'"
check "attendee name cleaned" [ "$(cat "$RUN/attendee" 2>/dev/null)" = "Ada Lovelace" ]
URL=$(cat "$RUN/hub-url"); CODE=$(cat "$RUN/hub-code")
check "download page says preparing" bash -c "curl -fs '$URL' | grep -qi preparing"
STATE=http://127.0.0.1:$PORT/dash/state
check "dashboard shows the box active with the name" \
  wait_for "curl -fs $STATE | python3 -c 'import json,sys; b=[x for x in json.load(sys.stdin)[\"boxes\"] if x.get(\"name\")==\"hackbox9\"]; sys.exit(0 if b and b[0].get(\"state\")==\"active\" else 1)'"
check "dashboard JSON has the attendee name" bash -c "curl -fs $STATE | grep -q 'Ada Lovelace'"
check "dashboard has the attendee email" bash -c "curl -fs $STATE | grep -q 'ada@example.com'"
check "dashboard has the Telegram handle" bash -c "curl -fs $STATE | grep -q '@ada_lovelace'"
check "download page does not show the email" bash -c "! curl -fs '$URL' | grep -q 'ada@example.com'"

# --- extend request, approval ---
date +%s > "$RUN/extend-request"; echo pending > "$RUN/extend-status"
REQ=""
wait_for "REQ=\$(curl -fs $STATE | python3 -c 'import json,sys; d=json.load(sys.stdin); r=[b.get(\"request\") for b in d[\"boxes\"] if b.get(\"request\")]; print(r[0][\"id\"] if r else \"\")'); [ -n \"\$REQ\" ]"
check "hub shows a pending extend request" [ -n "$REQ" ]
END0=$(cat "$RUN/session-end")
curl -fs -X POST -H 'Content-Type: application/json' -d '{"approve":true,"minutes":10}' \
  "http://127.0.0.1:$PORT/dash/requests/$REQ" >/dev/null
check "approval runs 'hackbox extend 10' on the box" wait_for "grep -qx 'extend 10' $T/commands.log 2>/dev/null"
check "session end moved by 10 minutes" [ "$(( $(cat "$RUN/session-end") - END0 ))" = 600 ]
check "box shows 'approved 10'" wait_for "grep -qx 'approved 10' $RUN/extend-status"
check "request flag cleared" [ ! -e "$RUN/extend-request" ]

# --- staff extend + refused command ---
curl -fs -X POST -H 'Content-Type: application/json' -d '{"minutes":5}' \
  "http://127.0.0.1:$PORT/dash/boxes/hackbox9/extend" >/dev/null
check "staff +5 runs 'hackbox extend 5'" wait_for "grep -qx 'extend 5' $T/commands.log"

# --- end, archive, upload ---
echo ending > "$RUN/state"; echo ABCDEF > "$RUN/code"; sleep 1.5
curl -fs -X POST -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$PORT/dash/boxes/hackbox9/finish" >/dev/null
check "staff Finish sets the Done flag on the box" wait_for "[ -e $RUN/ending-done ]"
mkdir -p "$T/proj/project/src"; echo 'console.log("hi")' > "$T/proj/project/src/index.js"
tar -C "$T/proj" -czf "$T/archive/ABCDEF.tar.gz" project
echo resetting > "$RUN/state"; sleep 0.6
echo "ABCDEF $(date +%s)" > "$RUN/last-archive"
echo idle > "$RUN/state"
check "upload job finishes" wait_for "[ -z \"\$(ls $T/state/jobs)\" ] && grep -q uploaded $T/agent.log"
check "download page now offers the download" wait_for "curl -fs '$URL' | grep -qi download"
curl -fs -o "$T/dl.zip" "$URL/download"
check "zip contains the project file" bash -c "unzip -l '$T/dl.zip' | grep -q 'src/index.js'"
check "short code page redirects" bash -c "curl -fs -o /dev/null -w '%{redirect_url}' -X POST -d code=$CODE http://127.0.0.1:$PORT/code | grep -q '/d/'"
check "dashboard /dash is refused through the tunnel" \
  bash -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -H 'Cf-Connecting-IP: 1.2.3.4' $STATE)\" = 404 ]"

# --- hub down: the session still starts, the job waits and then goes through ---
kill $HUB_PID; wait $HUB_PID 2>/dev/null
printf 'Grace Hopper\n\n' > "$T/home/attendee"
echo $(( $(date +%s) + 1800 )) > "$RUN/session-end"; echo active > "$RUN/state"; sleep 1.5
echo ending > "$RUN/state"; sleep 0.5; echo "none $(date +%s)" > "$RUN/last-archive"; echo idle > "$RUN/state"
check "job queued while the hub is down" wait_for "[ -n \"\$(ls $T/state/jobs)\" ]"
"$T/hackbox-hub" -listen 127.0.0.1:$PORT -data "$T/hubdata" -config "$T/hub.json" 2>>"$T/hub.log" &
HUB_PID=$!
check "queued job delivered after the hub is back" wait_for "[ -z \"\$(ls $T/state/jobs)\" ]"
check "late session registered with its name" wait_for "curl -fs $STATE | grep -q 'Grace Hopper'"

# --- agent keys from the dashboard: applied only while idle ---
CFG=http://127.0.0.1:$PORT/dash/config
post() { curl -fs -X POST -H 'Content-Type: application/json' -d "$1" "$CFG" >/dev/null; }
printf 'Linus\n\n' > "$T/home/attendee"
echo $(( $(date +%s) + 1800 )) > "$RUN/session-end"; echo active > "$RUN/state"; sleep 1
post '{"scope":"*","set":{"anthropic-api-key":"sk-ant-test-0123456789","agents":"claude-0g opencode"}}'
sleep 3
check "keys not applied during a session" bash -c "! grep -q '^secret set' $T/commands.log"
echo idle > "$RUN/state"
check "default key applied when idle" wait_for "grep -qx 'secret set anthropic-api-key' $T/commands.log"
check "key value delivered on stdin" [ "$(cat "$T/secret-anthropic-api-key" 2>/dev/null)" = "sk-ant-test-0123456789" ]
check "agents set from the dashboard" wait_for "grep -qx 'agents set claude-0g opencode' $T/commands.log"
check "welcome screen reset after applying" wait_for "grep -qx 'reset' $T/commands.log"
check "dashboard config never returns the key" bash -c "! curl -fs $CFG | grep -q sk-ant-test"
post '{"scope":"hackbox9","set":{"0g-router-key":"0g-box-key-abcdefgh"}}'
check "box override applied" wait_for "[ \"\$(cat $T/secret-0g-router-key 2>/dev/null)\" = 0g-box-key-abcdefgh ]"
check "dashboard shows keys up to date" wait_for "curl -fs $STATE | python3 -c 'import json,sys; b=[x for x in json.load(sys.stdin)[\"boxes\"] if x[\"name\"]==\"hackbox9\"][0]; sys.exit(0 if b.get(\"config_version\") and b.get(\"config_version\")==b.get(\"applied_config_version\") else 1)'"

# --- fleet stick: the hub numbers new boxes ---
EN=http://127.0.0.1:$PORT/api/v1/enroll
enroll() { curl -fs -H 'Authorization: Bearer enroll-0123456789abcdef' -H 'Content-Type: application/json' -d "{\"mac\":\"$1\"}" $EN; }
R1=$(enroll aa:bb:cc:00:00:01); N1=$(echo "$R1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')
check "first new box gets hackbox1" [ "$N1" = hackbox1 ]
N2=$(enroll aa:bb:cc:00:00:02 | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')
check "second new box gets hackbox2" [ "$N2" = hackbox2 ]
R1b=$(enroll aa:bb:cc:00:00:01)
check "same MAC gets the same name back" [ "$(echo "$R1b" | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')" = hackbox1 ]
T1=$(echo "$R1b" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
check "enrolled token works for heartbeats" bash -c "curl -fs -H 'Authorization: Bearer $T1' -H 'Content-Type: application/json' -d '{\"state\":\"active\",\"seconds_left\":60,\"activity\":[\"Terminal\",\"OpenCode\"]}' http://127.0.0.1:$PORT/api/v1/heartbeat >/dev/null"
check "activity shows on the dashboard" bash -c "curl -fs $STATE | grep -q '\"Terminal\",\"OpenCode\"'"
check "wrong enroll token refused" bash -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer nope-nope-nope-nope' -d '{\"mac\":\"aa:bb:cc:00:00:03\"}' $EN)\" = 401 ]"

echo; [ $fails = 0 ] && echo "ALL PASSED" || { echo "$fails FAILED"; echo "--- agent log"; tail -20 "$T/agent.log"; echo "--- hub log"; tail -20 "$T/hub.log"; }
exit $fails
