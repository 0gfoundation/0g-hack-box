# hack-box headless install: helpers shared by the live installer session (live.sh, late.sh)
# and the installed box (hackbox-firstboot). Sourced by bash. Needs HB_ENV (headless.env) read
# first for REPORT_URL and BEEP; everything is best effort and never fails the caller.

HB_PROGRESS=${HB_PROGRESS:-/var/log/hackbox-firstboot.log}

hb_now() { date '+%Y-%m-%dT%H:%M:%S%z'; }

# One plain line in the progress log (and on stdout for the journal).
hb_line() { echo "$(hb_now) $*" | tee -a "$HB_PROGRESS"; }

# Global IPv4 addresses, space separated.
hb_ips() { ip -4 -o addr show scope global 2>/dev/null | awk '{sub(/\/.*/, "", $4); print $4}' | paste -sd' ' -; }

# POST a tiny JSON status to REPORT_URL (if set): hostname, IPs, stage, detail. 5 s cap.
hb_report() {
  [ -n "${REPORT_URL:-}" ] || return 0
  local stage="$1" detail="${2:-}" body
  body=$(python3 - "$(hostname)" "$(hb_ips)" "$stage" "$detail" <<'PY' 2>/dev/null
import json, sys, time
h, ips, stage, detail = sys.argv[1:5]
print(json.dumps({"hostname": h, "ips": ips.split(), "stage": stage, "detail": detail,
                  "time": time.strftime("%Y-%m-%dT%H:%M:%S%z")}))
PY
) || return 0
  curl -fsS -m 5 -X POST -H 'Content-Type: application/json' --data "$body" "$REPORT_URL" >/dev/null 2>&1 || true
}

# PC speaker patterns (BEEP=0 in headless.env turns them off). Only works when the board has a
# speaker or buzzer wired to the legacy PC speaker port; many fanless mini PCs have none, then
# this is silent. Pattern: a string of s (short, 0.15 s) and l (long, 0.8 s).
hb_beep() {
  [ "${BEEP:-1}" = 1 ] || return 0
  modprobe pcspkr 2>/dev/null || true
  python3 - "$1" <<'PY' >/dev/null 2>&1 || true
import fcntl, glob, os, struct, sys, time
pattern = sys.argv[1]
KIOCSOUND, CLOCK, FREQ = 0x4B2F, 1193180, 880
def tone_console(fd, on):
    fcntl.ioctl(fd, KIOCSOUND, CLOCK // FREQ if on else 0)
def tone_evdev(fd, on):
    # struct input_event: timeval (2 longs), type u16, code u16, value s32. EV_SND=0x12, SND_TONE=0x02
    os.write(fd, struct.pack("llHHi", 0, 0, 0x12, 0x02, FREQ if on else 0))
fd, tone = None, None
for dev in glob.glob("/dev/input/by-path/*pcspkr*"):
    try:
        fd, tone = os.open(dev, os.O_WRONLY), tone_evdev; break
    except OSError:
        pass
if fd is None:
    for dev in ("/dev/tty0", "/dev/console"):
        try:
            fd, tone = os.open(dev, os.O_WRONLY | os.O_NOCTTY), tone_console; break
        except OSError:
            pass
if fd is None:
    sys.exit(0)
for c in pattern:
    tone(fd, True); time.sleep(0.8 if c == "l" else 0.15); tone(fd, False); time.sleep(0.25)
PY
}
