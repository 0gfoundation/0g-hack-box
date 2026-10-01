# limits (destructive): the attendee slice absorbs a fork bomb and a memory hog
# without taking down the admin session or the box. Spec 5. Only with --destructive.

group_limits() {
  need_linux_or_skip "slice_limits" || return
  need_sudo_or_skip "slice_limits" || return
  local slice="user-${HACKER_UID}.slice"

  # Slice resource limits are in force.
  info="$(systemctl show "$slice" -p MemoryMax -p TasksMax -p CPUQuotaPerSecUSec 2>/dev/null)"
  mm="$(printf '%s' "$info" | sed -n 's/^MemoryMax=//p')"
  tm="$(printf '%s' "$info" | sed -n 's/^TasksMax=//p')"
  cq="$(printf '%s' "$info" | sed -n 's/^CPUQuotaPerSecUSec=//p')"
  problems=""
  case "$mm" in ''|infinity|18446744073709551615) problems="$problems MemoryMax=$mm";; esac
  case "$tm" in ''|infinity|18446744073709551615) problems="$problems TasksMax=$tm";; esac
  if [ -z "$problems" ]; then
    pass "slice_limits"
  else
    fail "slice_limits" "slice caps missing or unbounded:$problems (cpuquota=$cq)"
  fi

  # ---- Fork bomb (bounded: the children exit on their own). --------------
  # Tag the children with an unusual sleep interval so cleanup is precise.
  as_hacker_t 20 "nohup bash -c 'for i in \$(seq 1 6000); do sleep 47 & done; wait' >/dev/null 2>&1 & echo started" >/dev/null 2>&1
  # Admin stays responsive: a trivial admin command returns within 5 seconds.
  ms="$(elapsed_ms 'ls / >/dev/null 2>&1; id >/dev/null 2>&1')"
  if [ "$ms" -lt 5000 ]; then
    pass "admin_responsive_under_forkbomb"
  else
    fail "admin_responsive_under_forkbomb" "admin command took ${ms}ms under the fork bomb (limit 5000)"
  fi
  # Task cap held: the attendee cannot exceed TasksMax. Count its tasks; must
  # not blow far past the configured ceiling.
  ntasks="$(sudo -n bash -c "cat /sys/fs/cgroup/user.slice/$slice/pids.current 2>/dev/null" 2>/dev/null)"
  if [ -n "$ntasks" ] && [ -n "$tm" ] && [ "$tm" != infinity ]; then
    if [ "$ntasks" -le $(( tm + 50 )) ]; then
      pass "forkbomb_capped"
    else
      fail "forkbomb_capped" "attendee has $ntasks tasks, above TasksMax=$tm"
    fi
  else
    skip "forkbomb_capped" "could not read pids.current for $slice"
  fi
  # Clean up the fork bomb.
  sudo -n pkill -KILL -u "$HACKER_USER" -f 'sleep 47' 2>/dev/null || true

  # ---- Memory hog. -------------------------------------------------------
  # Allocate and touch far more than MemoryMax so the in-slice OOM killer acts.
  hog="$(as_hacker_t 15 "nohup python3 -c 'a=bytearray(0);
import sys
b=[]
try:
  while True: b.append(bytearray(256*1024*1024))
except Exception: pass' >/dev/null 2>&1 & echo \$!" | tail -1)"
  # Admin still responsive while the hog runs.
  ms2="$(elapsed_ms 'ls / >/dev/null 2>&1')"
  if [ "$ms2" -lt 5000 ]; then
    pass "admin_responsive_under_memhog"
  else
    fail "admin_responsive_under_memhog" "admin command took ${ms2}ms under the memory hog (limit 5000)"
  fi
  # The hog is killed (in-slice OOM) or capped below MemoryMax.
  sleep 5
  if [ -z "$hog" ] || ! sudo -n kill -0 "$hog" 2>/dev/null; then
    pass "memhog_killed_or_capped"
  else
    rss_kb="$(sudo -n bash -c "grep VmRSS /proc/$hog/status 2>/dev/null | awk '{print \$2}'" 2>/dev/null)"
    max_kb=$(( ${mm:-0} / 1024 ))
    if [ -n "$rss_kb" ] && [ "$max_kb" -gt 0 ] && [ "$rss_kb" -le "$max_kb" ]; then
      pass "memhog_killed_or_capped"
    else
      fail "memhog_killed_or_capped" "hog alive with RSS=${rss_kb}kB (MemoryMax=${max_kb}kB)"
    fi
    sudo -n kill -KILL "$hog" 2>/dev/null || true
  fi
  sudo -n pkill -KILL -u "$HACKER_USER" -f 'bytearray' 2>/dev/null || true
}
