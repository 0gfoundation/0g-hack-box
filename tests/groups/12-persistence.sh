# persistence (destructive): nothing an attendee can leave behind survives a
# reset. One artifact per vector, then a reset, then prove each is gone. Spec 3.3.
# Only with --destructive.

_reset_to_idle() {
  as_admin_t 180 'hackbox reset' >/dev/null 2>&1 || true
  wait_for 60 "[ \"\$(tr -d '[:space:]' < /run/hackbox/state 2>/dev/null)\" = idle ]"
}

group_persistence() {
  need_linux_or_skip "persistence" || return
  need_sudo_or_skip "persistence" || return
  command -v hackbox >/dev/null || { fail "persistence" "hackbox CLI absent"; return; }

  local M="$MARK.persist"

  # ---- Plant one artifact per persistence vector. -----------------------
  as_hacker_t 20 "touch \"\$HOME/$M\""
  as_hacker_t 20 "mkdir -p \"\$HOME/.claude\" && echo hist > \"\$HOME/.claude/$M\"" 2>/dev/null || true
  as_hacker_t 20 "mkdir -p \"\$HOME/.local/share/opencode\" && echo db > \"\$HOME/.local/share/opencode/$M\"" 2>/dev/null || true
  as_hacker_t 20 "mkdir -p \"\$HOME/.config/chromium/Default\" && echo prof > \"\$HOME/.config/chromium/Default/$M\"" 2>/dev/null || true
  as_hacker_t 20 "mkdir -p \"\$HOME/.config/systemd/user\" && printf '[Service]\nExecStart=/bin/true\n' > \"\$HOME/.config/systemd/user/$M.service\"" 2>/dev/null || true
  as_hacker_t 20 "touch /tmp/$M /var/tmp/$M /dev/shm/$M" 2>/dev/null || true
  # SysV IPC (shared memory segment) owned by hacker.
  ipc_made=0
  if as_hacker_t 15 'command -v ipcmk >/dev/null'; then
    as_hacker_t 15 'ipcmk -M 4096' >/dev/null 2>&1 && ipc_made=1
  fi
  # Linger flag (an attendee cannot set it, so set it as admin to prove the
  # reset clears any linger that was somehow enabled).
  sudo -n loginctl enable-linger "$HACKER_USER" >/dev/null 2>&1 || true

  # ---- Reset. -----------------------------------------------------------
  if ! _reset_to_idle; then
    fail "persistence" "box did not return to idle after reset; cannot judge persistence"
    _reset_to_idle >/dev/null 2>&1 || true
    return
  fi

  # ---- Prove each vector is gone. ---------------------------------------
  if as_hacker_t 15 "test ! -e \"\$HOME/$M\""; then pass "persist_home"; else fail "persist_home" "home file survived"; fi

  if as_hacker_t 15 "test ! -e \"\$HOME/.claude/$M\""; then pass "persist_claude_history"; else fail "persist_claude_history" "~/.claude history survived"; fi

  if as_hacker_t 15 "test ! -e \"\$HOME/.local/share/opencode/$M\""; then pass "persist_opencode_data"; else fail "persist_opencode_data" "OpenCode data survived"; fi

  if as_hacker_t 15 "test ! -e \"\$HOME/.config/chromium/Default/$M\""; then pass "persist_browser_profile"; else fail "persist_browser_profile" "browser profile survived"; fi

  if as_hacker_t 15 "test ! -e \"\$HOME/.config/systemd/user/$M.service\""; then pass "persist_user_unit"; else fail "persist_user_unit" "user systemd unit file survived"; fi

  gone=1; left=""
  for p in "/tmp/$M" "/var/tmp/$M" "/dev/shm/$M"; do
    sudo -n test -e "$p" 2>/dev/null && { gone=0; left="$left $p"; }
  done
  if [ "$gone" -eq 1 ]; then pass "persist_tmp_shm"; else fail "persist_tmp_shm" "survived:$left"; fi

  # crontab empty.
  if ! as_hacker_t 15 'crontab -l 2>/dev/null | grep -q .'; then pass "persist_crontab"; else fail "persist_crontab" "attendee crontab survived"; fi

  # Linger cleared.
  if sudo -n test -e "/var/lib/systemd/linger/$HACKER_USER" 2>/dev/null; then
    fail "persist_linger" "linger flag still set after reset"
    sudo -n loginctl disable-linger "$HACKER_USER" >/dev/null 2>&1 || true
  else
    pass "persist_linger"
  fi

  # SysV IPC for hacker gone.
  if [ "$ipc_made" -eq 1 ]; then
    if sudo -n bash -c "ipcs -m | awk '\$3==\"$HACKER_USER\"' | grep -q ." 2>/dev/null; then
      fail "persist_sysv_ipc" "SysV shared memory owned by hacker survived"
    else
      pass "persist_sysv_ipc"
    fi
  else
    skip "persist_sysv_ipc" "ipcmk unavailable; could not plant a SysV segment"
  fi

  _reset_to_idle >/dev/null 2>&1 || true
}
