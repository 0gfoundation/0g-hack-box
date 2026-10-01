#!/bin/bash
# Follow a headless hack-box install from your Mac (or any machine with ssh):
#   vm/usb/watch-install.sh <hostname-or-ip> [admin-user]
# Example: vm/usb/watch-install.sh hackbox1.local
# Waits until the box answers ssh, then prints the progress lines as they appear:
#   while the installer runs:  ssh mint@<host>  /var/log/hackbox-install.log
#   after the first reboot:    ssh <admin>@<host> /var/log/hackbox-firstboot.log
# and stops at HACKBOX-FIRSTBOOT-DONE with a clear verdict. Uses your default ssh key (or
# SSH_KEY=/path). Host keys are not recorded or checked (the installer and the installed
# system have different keys, and a reinstall changes them): use it on your own LAN only.
# Environment: SSH_KEY, WATCH_TIMEOUT (minutes, default 90), POLL (seconds, default 10),
# SSH_EXTRA (more ssh options, for example "-p 2290 -J user@jumphost" in the VM lab).
# Runs with macOS's bash 3.2.
set -u
HOST=${1:?usage: watch-install.sh <hostname-or-ip> [admin-user]}
ADMIN=${2:-hackadmin}
TIMEOUT_MIN=${WATCH_TIMEOUT:-90}
POLL=${POLL:-10}
SSHO="-o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 -o ServerAliveInterval=10 -o ServerAliveCountMax=3"
[ -n "${SSH_KEY:-}" ] && SSHO="$SSHO -i $SSH_KEY"
[ -n "${SSH_EXTRA:-}" ] && SSHO="$SSHO $SSH_EXTRA"
start=$(date +%s)
el() { local s=$(( $(date +%s) - start )); printf '%02d:%02d' $((s / 60)) $((s % 60)); }
say() { echo "[watch $(el)] $*"; }
over() { [ $(( $(date +%s) - start )) -gt $((TIMEOUT_MIN * 60)) ]; }
# shellcheck disable=SC2086
rssh() { local u=$1; shift; ssh $SSHO "$u@$HOST" "$@"; }

say "waiting for $HOST to answer ssh (installer: user mint; installed system: $ADMIN)"
phase="" seen_install=0 seen_fb=0 last_state=""
while :; do
  if over; then say "gave up after $TIMEOUT_MIN minutes"; echo "VERDICT: TIMEOUT (no DONE line). See README.md, troubleshooting."; exit 3; fi
  if out=$(rssh "$ADMIN" 'cat /var/log/hackbox-firstboot.log 2>/dev/null; echo "@@state $(systemctl is-active hackbox-firstboot 2>/dev/null)"' 2>/dev/null); then
    [ "$phase" = fb ] || { phase=fb; say "connected to the installed system as $ADMIN@$HOST"; }
    n=$(printf '%s\n' "$out" | grep -vc '^@@state')
    if [ "$n" -gt "$seen_fb" ]; then printf '%s\n' "$out" | grep -v '^@@state' | tail -n +$((seen_fb + 1)); seen_fb=$n; fi
    done_line=$(printf '%s\n' "$out" | grep 'HACKBOX-FIRSTBOOT-DONE' | tail -n 1)
    state=$(printf '%s\n' "$out" | sed -n 's/^@@state //p')
    if [ -n "$done_line" ]; then
      rc=$(echo "$done_line" | sed 's/.*rc=//')
      # A rerun after a failure starts a new RUN: only trust a DONE after the last RUN line.
      last_run=$(printf '%s\n' "$out" | grep -n ' RUN ' | tail -n 1 | cut -d: -f1)
      last_done=$(printf '%s\n' "$out" | grep -n 'HACKBOX-FIRSTBOOT-DONE' | tail -n 1 | cut -d: -f1)
      if [ "${last_done:-0}" -gt "${last_run:-0}" ]; then
        echo
        if [ "$rc" = 0 ]; then
          echo "VERDICT: SUCCESS. $HOST is provisioned and reboots to the attendee welcome screen now."
          echo "Next: ssh $ADMIN@$HOST hackbox status    (then hackbox lock status, and SECRETS.md for the keys)"
          exit 0
        fi
        echo "VERDICT: FAILED (rc=$rc). Last lines of the detail log:"
        rssh "$ADMIN" 'sudo -n tail -n 40 /var/log/hackbox-firstboot.detail.log' 2>/dev/null
        echo "Fix the cause, then rerun on the box: ssh $ADMIN@$HOST sudo systemctl start hackbox-firstboot"
        exit 1
      fi
    fi
    if [ "$n" = 0 ] && [ "$state" != "$last_state" ]; then say "first-boot unit: ${state:-unknown}"; last_state=$state; fi
  elif out=$(rssh mint 'cat /var/log/hackbox-install.log 2>/dev/null' 2>/dev/null); then
    [ "$phase" = inst ] || { phase=inst; say "connected to the installer as mint@$HOST"; }
    n=$(printf '%s\n' "$out" | grep -c .)
    if [ "$n" -gt "$seen_install" ]; then printf '%s\n' "$out" | tail -n +$((seen_install + 1)); seen_install=$n; fi
  else
    if [ "$phase" != wait ]; then
      case "$phase" in
        inst) say "installer went away (it reboots into the installed system; about 1 to 2 minutes)" ;;
        fb) say "installed system went away (reboot?)" ;;
      esac
      phase=wait
    fi
  fi
  sleep "$POLL"
done
