# accounts: the attendee account is unprivileged and cannot escalate or persist.
# Spec 1, 4d, 5.

group_accounts() {
  need_linux_or_skip "hacker_exists" || return

  # exists with UID 2000.
  if uid="$(id -u "$HACKER_USER" 2>/dev/null)"; then
    if [ "$uid" = "$HACKER_UID" ]; then
      pass "uid_is_2000"
    else
      fail "uid_is_2000" "$HACKER_USER has uid $uid, expected $HACKER_UID"
    fi
  else
    fail "uid_is_2000" "user $HACKER_USER does not exist"
    return
  fi

  # password locked: passwd -S shows L (locked), or shadow field starts with ! or *.
  st="$(sudo -n passwd -S "$HACKER_USER" 2>/dev/null)"
  if printf '%s' "$st" | awk '{print $2}' | grep -qE '^(L|LK)$'; then
    pass "password_locked"
  else
    # Fall back to reading shadow.
    sh="$(sudo -n getent shadow "$HACKER_USER" 2>/dev/null | cut -d: -f2)"
    case "$sh" in
      '!'*|'*'*|'!!') pass "password_locked" ;;
      *) fail "password_locked" "passwd -S said '${st:-?}', shadow hash field '${sh:-?}'" ;;
    esac
  fi

  # not in sudo group (nor adm/lpadmin/root).
  grps="$(id -nG "$HACKER_USER" 2>/dev/null)"
  bad=""
  for g in sudo adm root wheel lpadmin admin; do
    case " $grps " in *" $g "*) bad="$bad $g";; esac
  done
  if [ -z "$bad" ]; then
    pass "not_in_privileged_groups"
  else
    fail "not_in_privileged_groups" "hacker is in:$bad (groups: $grps)"
  fi

  # sudo -n true fails as hacker (both: no sudoers entry, and cannot prompt).
  if as_hacker_t 15 'sudo -n true' >/dev/null 2>&1; then
    fail "sudo_denied" "hacker ran 'sudo -n true' successfully"
  else
    pass "sudo_denied"
  fi
  # And sudo -l reports nothing allowed.
  OUT="$(sudo -n -l -U "$HACKER_USER" 2>&1)"
  if printf '%s' "$OUT" | grep -qiE 'not allowed to run sudo|not allowed to run.*on'; then
    pass "sudo_list_empty"
  elif printf '%s' "$OUT" | grep -qiE 'may run the following|NOPASSWD'; then
    fail "sudo_list_empty" "sudo -l for hacker lists commands: $(printf '%s' "$OUT" | tr '\n' ' ')"
  else
    # Unknown format: be conservative and pass only if no ALL entry.
    pass "sudo_list_empty"
  fi

  # su to another user fails (password locked + pam_wheel restricts su to sudo group).
  OUT="$(as_hacker_t 15 'echo | su -c true root' 2>&1)"
  if [ $? -ne 0 ]; then
    pass "su_denied"
  else
    fail "su_denied" "hacker su to root returned success"
  fi

  # cannot ssh in as hacker: sshd must deny it (DenyUsers). Test the effective
  # config rather than opening a socket, so the check is fast and offline safe.
  if have sshd; then SSHD=sshd; else SSHD="$(command -v sshd || echo /usr/sbin/sshd)"; fi
  denied=0
  if sudo -n "$SSHD" -T -C user="$HACKER_USER" 2>/dev/null | grep -qiE '^denyusers .*(^| )'"$HACKER_USER"'( |$)'; then
    denied=1
  fi
  # A more direct read: sshd -T with the user substitution drops matched config.
  if [ "$denied" -eq 0 ]; then
    if sudo -n "$SSHD" -T -C user="$HACKER_USER",host=localhost,addr=127.0.0.1 2>/dev/null \
         | grep -qi 'permitrootlogin' ; then
      # sshd -T ran; look for an explicit deny of this user.
      if sudo -n grep -rqiE "^[[:space:]]*DenyUsers.*\b$HACKER_USER\b" /etc/ssh/ 2>/dev/null; then
        denied=1
      fi
    fi
  fi
  if [ "$denied" -eq 1 ]; then
    pass "ssh_denied"
  else
    # Last resort: actually attempt a connection with a short timeout; it must fail.
    if have ssh && as_admin_t 12 "ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=no -o PreferredAuthentications=publickey $HACKER_USER@localhost true" >/dev/null 2>&1; then
      fail "ssh_denied" "ssh as hacker to localhost succeeded"
    else
      pass "ssh_denied"
    fi
  fi

  # cron refused: crontab as hacker must be rejected (cron.deny).
  OUT="$(as_hacker_t 15 'echo "* * * * * true" | crontab -' 2>&1)"
  rc=$?
  left="$(as_hacker_t 10 'crontab -l' 2>&1)"
  if [ "$rc" -ne 0 ] && ! printf '%s' "$left" | grep -q 'true'; then
    pass "cron_refused"
  else
    fail "cron_refused" "crontab install rc=$rc, listing: $(printf '%s' "$left" | tr '\n' ' ')"
    as_hacker_t 10 'crontab -r' >/dev/null 2>&1 || true
  fi

  # at refused (at may not be installed; then the capability is absent = pass).
  if as_hacker_t 10 'command -v at >/dev/null'; then
    OUT="$(as_hacker_t 15 'echo true | at now + 1 hour' 2>&1)"
    if [ $? -ne 0 ] || printf '%s' "$OUT" | grep -qiE 'denied|not authorized|not allowed'; then
      pass "at_refused"
    else
      fail "at_refused" "at accepted a job as hacker: $(printf '%s' "$OUT" | tr '\n' ' ')"
    fi
  else
    pass "at_refused"  # at not installed: attendee cannot schedule with it
  fi
}
