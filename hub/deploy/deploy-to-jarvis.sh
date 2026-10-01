#!/usr/bin/env bash
# Deploy hackbox-hub to jarvis (the Mac mini). Idempotent, safe to re-run.
# Run from the laptop, from anywhere.
#
#   hub/deploy/deploy-to-jarvis.sh            build, push, (re)load the service
#   hub/deploy/deploy-to-jarvis.sh --tunnel   also add the public tunnel route (optional step 5)
#
# Env:
#   JARVIS_HOST   ssh host or alias (default: jarvis, the ~/.ssh/config alias)
#   HUB_BOXES     box names for a NEW config.json (default: "hackbox1 hackbox2 hackbox3")
#
# Never overwrites an existing config.json. Never restarts or kills cloudflared.
set -euo pipefail

HOST="${JARVIS_HOST:-jarvis}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=8 ${HOST}"
SCP="scp -o BatchMode=yes -o ConnectTimeout=8"
HUB_DIR="$(cd "$(dirname "$0")/.." && pwd)"

APP="hackbox-hub"
PORT="8210"
LABEL="com.udhay.${APP}"
REMOTE_HOME="/Users/jarvis"
REMOTE_DIR="${REMOTE_HOME}/${APP}"
DATA_DIR="${REMOTE_DIR}/data"
CONFIG="${REMOTE_DIR}/config.json"
PLIST="${REMOTE_HOME}/Library/LaunchAgents/${LABEL}.plist"
PUBLIC_HOST="hackbox.udhaykumarbala.dev"
TUNNEL="mac-mini"
# Non-interactive ssh has a minimal PATH; Homebrew binaries live here.
CFD="/opt/homebrew/bin/cloudflared"

DO_TUNNEL=0
for a in "$@"; do
  case "$a" in
    --tunnel) DO_TUNNEL=1 ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "unknown argument: $a" >&2; exit 2 ;;
  esac
done

say(){ printf '\n=== %s ===\n' "$*"; }

# ---------------------------------------------------------------------------
say "0. preflight"
command -v go >/dev/null || { echo "ABORT: go not found on this laptop"; exit 1; }
$SSH 'echo connected host=$(hostname) uid=$(id -u)' || { echo "ABORT: ssh ${HOST} failed"; exit 1; }
# The port held by OUR OWN hub is the normal redeploy case. A foreign listener is fatal.
PORT_PID=$($SSH "/usr/sbin/lsof -ti tcp:${PORT} -sTCP:LISTEN 2>/dev/null | head -1" || true)
if [ -z "${PORT_PID}" ]; then
  echo "port ${PORT}: free (fresh deploy)"
else
  OWN=$($SSH "ps -p ${PORT_PID} -o command= 2>/dev/null | grep -c '${REMOTE_DIR}/${APP}'" || true)
  if [ "${OWN:-0}" = "0" ]; then
    echo "ABORT: port ${PORT} is held by a FOREIGN process (pid ${PORT_PID}) on ${HOST}"; exit 1
  fi
  echo "port ${PORT}: held by our own ${APP} (pid ${PORT_PID}), redeploy will replace it"
fi
$SSH "test -x /usr/bin/git" || echo "WARNING: /usr/bin/git missing on ${HOST}; GitHub pushes will fail until the Xcode command line tools are installed"

# ---------------------------------------------------------------------------
say "1. build darwin/arm64"
BUILD="$(mktemp -d)"
trap 'rm -rf "${BUILD}"' EXIT
(cd "${HUB_DIR}" && go test ./... >/dev/null && echo "tests pass")
(cd "${HUB_DIR}" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "${BUILD}/${APP}" .)
ls -l "${BUILD}/${APP}"

# ---------------------------------------------------------------------------
say "2. push the binary"
$SSH "mkdir -p ${DATA_DIR} ${REMOTE_HOME}/Library/LaunchAgents ${REMOTE_HOME}/Library/Logs && chmod 700 ${REMOTE_DIR} ${DATA_DIR}"
$SCP "${BUILD}/${APP}" "${HOST}:${REMOTE_DIR}/${APP}.new"
# Swap in place; the running process keeps its old inode until the reload below.
$SSH "chmod 755 ${REMOTE_DIR}/${APP}.new && mv -f ${REMOTE_DIR}/${APP}.new ${REMOTE_DIR}/${APP}"
echo "installed ${REMOTE_DIR}/${APP}"

# ---------------------------------------------------------------------------
say "3. config.json (created only if missing)"
if $SSH "test -f ${CONFIG}"; then
  echo "${CONFIG} exists, left untouched"
  $SSH "chmod 600 ${CONFIG}"
else
  BOXES="${HUB_BOXES:-hackbox1 hackbox2 hackbox3}"
  ENROLL=$(openssl rand -hex 24)
  echo "fleet enroll token (put it in the build .env): HUB_ENROLL_TOKEN=${ENROLL}"
  ENTRIES=""
  echo "new config: one random token per box. Put each in that box with:"
  for b in ${BOXES}; do
    t=$(openssl rand -hex 24)
    ENTRIES="${ENTRIES}${ENTRIES:+,}
    \"${t}\": \"${b}\""
    echo "  ${b}:  sudo hackbox hub set http://<jarvis tailnet address>:${PORT} ${t}"
  done
  $SSH "umask 077 && cat > ${CONFIG}" <<JSON
{
  "public_base_url": "https://${PUBLIC_HOST}",
  "boxes": {${ENTRIES}
  },
  "github": {"org": "0g-hackbox-sessions", "token": ""},
  "download_days": 7,
  "enroll_token": "${ENROLL}",
  "name_prefix": "hackbox"
}
JSON
  $SSH "chmod 600 ${CONFIG}"
  echo "wrote ${CONFIG} (mode 600). GitHub token is empty, so pushes are disabled until you add one."
fi
$SSH "/usr/bin/python3 -c 'import json,sys; c=json.load(open(\"${CONFIG}\")); print(\"config ok:\", len(c.get(\"boxes\",{})), \"boxes, github\", \"on\" if c.get(\"github\",{}).get(\"token\") else \"disabled\")'"

# ---------------------------------------------------------------------------
say "4. LaunchAgent ${LABEL}"
$SSH "cat > ${PLIST}" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>${LABEL}</string>
  <key>ProgramArguments</key>
    <array>
      <string>${REMOTE_DIR}/${APP}</string>
      <!-- 0.0.0.0 so the tailnet reaches the dashboard and the box API. The
           tunnel only carries /d/, /code and /healthz, and the hub itself
           404s /, /dash/* and /api/* for anything with Cf-Connecting-IP. -->
      <string>-listen</string><string>0.0.0.0:${PORT}</string>
      <string>-data</string><string>${DATA_DIR}</string>
      <string>-config</string><string>${CONFIG}</string>
    </array>
  <key>WorkingDirectory</key><string>${REMOTE_DIR}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/><key>Crashed</key><true/></dict>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>${REMOTE_HOME}/Library/Logs/${APP}.out.log</string>
  <key>StandardErrorPath</key><string>${REMOTE_HOME}/Library/Logs/${APP}.err.log</string>
</dict>
</plist>
PLIST
$SSH "plutil -lint ${PLIST}"

if $SSH "launchctl print gui/501/${LABEL} >/dev/null 2>&1"; then
  echo "service loaded, restarting with the new binary"
  # Re-bootstrap so plist changes apply too, then kickstart in case bootstrap raced.
  $SSH "launchctl bootout gui/501/${LABEL} 2>/dev/null; sleep 1; launchctl bootstrap gui/501 ${PLIST} 2>/dev/null || launchctl kickstart -k gui/501/${LABEL}"
else
  $SSH "launchctl bootstrap gui/501 ${PLIST}"
fi
sleep 2
$SSH "launchctl list | grep ${LABEL} || true"
echo "local health on ${HOST}:"
if $SSH "curl -fsS --max-time 5 http://127.0.0.1:${PORT}/healthz"; then
  echo "  <- OK"
else
  echo "FAILED; last log lines:"
  $SSH "tail -20 ${REMOTE_HOME}/Library/Logs/${APP}.err.log" || true
  exit 1
fi

# ---------------------------------------------------------------------------
if [ "${DO_TUNNEL}" != "1" ]; then
  say "5. public tunnel route: SKIPPED (optional, run again with --tunnel)"
else
  say "5. OPTIONAL: public tunnel route for ${PUBLIC_HOST} (download pages only)"
  $SSH 'test -f ~/.cloudflared/config.yml' || { echo "ABORT: ~/.cloudflared/config.yml not found"; exit 1; }
  TS=$(date -u +%Y%m%d-%H%M%S)
  $SSH "cp ~/.cloudflared/config.yml ~/.cloudflared/config.yml.bak-${TS} && echo backed up to config.yml.bak-${TS}"
  # Insert above the http_status:404 catch-all, only if our host is absent.
  # Path-scoped: only /d/..., /code and /healthz go to the hub; the rest of
  # the hostname gets a 404 from cloudflared. Other apps' entries are never
  # reordered or removed.
  $SSH "/usr/bin/python3 - <<'PY'
import re, sys
p = '/Users/jarvis/.cloudflared/config.yml'
s = open(p).read()
host = '${PUBLIC_HOST}'
if host in s:
    print('ingress entry already present, no change'); sys.exit(0)
lines = s.splitlines()
out = []; inserted = False
for ln in lines:
    # Only the catch-all list item itself, never another app's same-host 404 line.
    if not inserted and re.match(r'^\s*-\s*service:\s*http_status:\s*404\s*$', ln):
        indent = ln[:len(ln) - len(ln.lstrip())]
        out.append(f'{indent}- hostname: {host}')
        out.append(f'{indent}  path: \"^/(d/|code|healthz)\"')
        out.append(f'{indent}  service: http://localhost:${PORT}')
        out.append(f'{indent}- hostname: {host}')
        out.append(f'{indent}  service: http_status:404')
        inserted = True
    out.append(ln)
if not inserted:
    print('ABORT: no http_status:404 catch-all found; not editing'); sys.exit(1)
open(p, 'w').write('\n'.join(out) + '\n')
print('inserted path-scoped ingress for', host)
PY" || { echo "edit failed, restoring backup"; $SSH "cp ~/.cloudflared/config.yml.bak-${TS} ~/.cloudflared/config.yml"; exit 1; }
  echo "validating:"
  if ! $SSH "${CFD} tunnel ingress validate"; then
    echo "VALIDATION FAILED, restoring backup"
    $SSH "cp ~/.cloudflared/config.yml.bak-${TS} ~/.cloudflared/config.yml"
    exit 1
  fi
  echo "rule check (expect localhost:${PORT}, then http_status:404):"
  $SSH "${CFD} tunnel ingress rule https://${PUBLIC_HOST}/d/x | tail -1; ${CFD} tunnel ingress rule https://${PUBLIC_HOST}/dash/state | tail -1" || true
  echo "DNS route:"
  $SSH "${CFD} tunnel route dns ${TUNNEL} ${PUBLIC_HOST}" 2>&1 | tail -1 || echo "(route may already exist, non-fatal)"
  echo
  echo ">>> The running cloudflared only re-reads config.yml on reload. To make"
  echo ">>> ${PUBLIC_HOST} live, run this yourself (sudo, needs a TTY, zero downtime):"
  echo
  echo "    ssh -t ${HOST} \"sudo kill -HUP \\\$(pgrep -f 'cloudflared tunnel.*run')\""
  echo
  echo ">>> Then check: curl -s https://${PUBLIC_HOST}/healthz   (expect ok)"
  echo ">>>             curl -s -o /dev/null -w '%{http_code}' https://${PUBLIC_HOST}/   (expect 404)"
fi

# ---------------------------------------------------------------------------
say "done"
echo "Dashboard: http://<jarvis tailnet address>:${PORT}/   (tailnet only)"
echo "API test:  http://<jarvis tailnet address>:${PORT}/dash/api_test.html"
echo "Logs:      ssh ${HOST} 'tail -f ~/Library/Logs/${APP}.err.log'"
echo "Restart:   ssh ${HOST} 'launchctl kickstart -k gui/501/${LABEL}'"
