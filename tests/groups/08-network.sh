# network: the attendee reaches the public internet and loopback but not the
# private, link-local or tailnet ranges, and the admin is not filtered. Spec 5.
#
# The strong, discriminating checks use the QEMU user-network addresses that
# certainly exist in the lab (gateway 10.0.2.2, DNS 10.0.2.3): the attendee must
# be actively prohibited on them while the admin is not. The other private
# ranges are only distinguishable from a plain no-route when they are actually
# routable, so those checks self-skip when the lab has no route to them.

# _classify: echo one of connect|refused|prohibited|unreach|timeout|other for a
# TCP connect attempt by the given runner (as_hacker_t / as_admin_t).
# The socket errno is read with python3 (always on Mint): curl 8.5 on Mint 22.3
# reports every failed connect as "Couldn't connect to server" without the
# errno text, which made a filtered and an unfiltered failure look the same.
# An nftables "reject with icmpx admin-prohibited" in the output path surfaces
# to the caller as EHOSTUNREACH (or EACCES/EPERM for a plain drop-reject).
_CLASSIFY_PY='import socket,sys,errno
s=socket.socket(); s.settimeout(float(sys.argv[3]))
try:
    s.connect((sys.argv[1],int(sys.argv[2]))); print("connect")
except socket.timeout: print("timeout")
except OSError as e: print({errno.ECONNREFUSED:"refused",errno.EACCES:"prohibited",errno.EPERM:"prohibited",errno.EHOSTUNREACH:"unreach",errno.ENETUNREACH:"unreach"}.get(e.errno,"other"))'
_classify() {
  local runner="$1" addr="$2" port="$3" to="$4" out rc
  if "$runner" 5 'command -v python3' >/dev/null 2>&1; then
    out="$("$runner" "$((to + 3))" "python3 -c '$_CLASSIFY_PY' $addr $port $to" 2>/dev/null | tail -n 1)"
    case "$out" in connect|refused|prohibited|unreach|timeout|other) echo "$out"; return ;; esac
  fi
  out="$("$runner" "$((to + 3))" "curl -sS -o /dev/null -m $to http://$addr:$port/" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ]; then echo connect; return; fi
  case "$out" in
    *"Permission denied"*|*rohibited*|*"not permitted"*) echo prohibited ;;
    *"Connection refused"*)                              echo refused ;;
    *"Network is unreachable"*|*"No route to host"*|*"Host is unreachable"*|*nreachable*) echo unreach ;;
    *"timed out"*|*"Timeout"*|*"Operation too slow"*)    echo timeout ;;
    *) [ "$rc" -eq 28 ] && echo timeout || echo other ;;
  esac
}

group_network() {
  need_linux_or_skip "public_internet" || return
  if ! as_hacker_t 10 'command -v curl >/dev/null'; then
    skip "public_internet" "curl not available to hacker"; return
  fi

  # Public internet works for the attendee (two well-known TLS hosts).
  ok=0; detail=""
  for host in github.com registry.npmjs.org; do
    if as_hacker_t 20 "curl -sS -o /dev/null -m 12 https://$host" >/dev/null 2>&1; then
      ok=$((ok + 1))
    else
      detail="$detail $host"
    fi
  done
  if [ "$ok" -ge 1 ]; then
    pass "public_internet"
  else
    fail "public_internet" "attendee could not reach:$detail"
  fi

  # DNS resolution works (the loopback stub resolver must keep working).
  if as_hacker_t 15 'getent hosts github.com >/dev/null'; then
    pass "dns_resolves"
  else
    fail "dns_resolves" "getent hosts github.com failed for the attendee"
  fi

  # Loopback works: attendee starts a listener and connects to it.
  port=$(( (RANDOM % 2000) + 20000 ))
  lp="$(as_hacker_t 10 "nohup python3 -m http.server $port --bind 127.0.0.1 >/dev/null 2>&1 & echo \$!" | tail -1)"
  sleep 1
  if as_hacker_t 12 "curl -sS -o /dev/null -m 5 http://127.0.0.1:$port/"; then
    pass "loopback_works"
  else
    fail "loopback_works" "attendee could not connect to its own 127.0.0.1:$port listener"
  fi
  [ -n "$lp" ] && sudo -n kill "$lp" 2>/dev/null; as_hacker_t 8 "kill $lp" >/dev/null 2>&1 || true

  # Strong isolation checks on routable lab addresses.
  for pair in "gateway 10.0.2.2" "dns_ip 10.0.2.3"; do
    set -- $pair; label="$1"; addr="$2"
    hc="$(_classify as_hacker_t "$addr" 80 4)"
    ac="$(_classify as_admin_t  "$addr" 80 4)"
    # Attendee must be blocked: prohibited (nft admin-prohibited). A plain
    # refused/connect means the filter is not in force for this uid.
    if [ "$hc" = prohibited ] || [ "$hc" = unreach ]; then
      pass "hacker_blocked_$label"
    else
      fail "hacker_blocked_$label" "attendee reached $addr with result '$hc' (expected prohibited)"
    fi
    # Admin must NOT be prohibited by our table.
    if [ "$ac" = prohibited ]; then
      fail "admin_not_blocked_$label" "admin was filtered on $addr (result 'prohibited')"
    else
      pass "admin_not_blocked_$label"
    fi
  done

  # Coverage of the other blocked ranges. Only meaningful when the lab can
  # actually route to them; otherwise a no-route is indistinguishable from a
  # filter and the check would be unfalsifiable, so skip with a reason.
  for pair in "rfc1918_192 192.168.0.1" "rfc1918_172 172.16.0.1" "linklocal 169.254.169.254" "cgnat 100.100.100.100"; do
    set -- $pair; label="$1"; addr="$2"
    ac="$(_classify as_admin_t "$addr" 80 3)"
    if [ "$ac" = unreach ] || [ "$ac" = timeout ]; then
      skip "hacker_blocked_$label" "no route to $addr in this lab; cannot distinguish filter from no-route"
      continue
    fi
    hc="$(_classify as_hacker_t "$addr" 80 3)"
    if [ "$hc" = prohibited ] || [ "$hc" = unreach ]; then
      pass "hacker_blocked_$label"
    else
      fail "hacker_blocked_$label" "attendee reached $addr with result '$hc' while admin got '$ac'"
    fi
  done
}
