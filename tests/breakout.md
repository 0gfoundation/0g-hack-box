# hack-box breakout checklist

The attendee-eye adversarial pass. Sit at a kiosk as the `hacker` user and try to
break out. Each item lists what to try from the keyboard, the defence that should
stop it, and how to confirm the defence held. This complements the automated suite
in `tests/`; it is meant for a human or an adversarial tester with 30 minutes and a
mean streak.

House rule for the tester: if any item succeeds where it should fail, capture the
exact command and its output and file it against the owning engineer. A single hole
in privilege, credit theft or persistence is a stop-ship.

Legend for "check": PASS means the defence held (the attack failed).

## A. Privilege escalation

1. Run `sudo -n true` and `sudo id`. Defence: `hacker` is not in `sudo`, no
   sudoers entry. Check: both fail; `sudo -l` says not allowed.
2. Run `su -` and `su root`. Defence: locked passwords plus `pam_wheel group=sudo`
   in `/etc/pam.d/su`. Check: every `su` target fails; no password is even accepted.
3. Run `pkexec id`. Defence: polkit default-deny for `hacker`. Check: not authorized.
4. Try `systemctl start anything.service` and `systemctl stop hackbox-session.service`.
   Defence: polkit allows only `start` of `hackbox-session.service` and
   `hackbox-reset.service`. Check: every other unit and verb is denied.
5. Look for setuid helpers to abuse: `find / -perm -4000 -type f 2>/dev/null`, then
   try the interesting ones. Defence: the home is `nosuid`, and stock setuid tools
   do not grant a shell. Check: none yields root.
6. Write a setuid-root binary into the home and run it. Defence: `/home/hacker` is
   mounted `nosuid`. Check: the setuid bit is ignored; it runs as `hacker`.
7. Read the AI credential files directly: `cat /etc/hackbox/secrets/*`,
   `grep -r sk- /etc/opencode /etc/claude-code`. Defence: NONE for the key the agent
   uses; a key readable by the agent is readable by the attendee by design. Check:
   the spend cap lives at the credential owner (workspace or router credit limit),
   NOT on the box. Confirm the key on the box is per-box and budgeted so reading it
   is not a jackpot.
8. Edit the Claude managed policy: `echo x >> /etc/claude-code/managed-settings.json`.
   Defence: file is root-owned. Check: permission denied.
9. Escape a user namespace to fake root and touch a root file:
   `unshare -Urm id; unshare -Ur bash -c 'echo x > /etc/hostname'`. Defence: cgroup
   membership and file ownership still bind; mapped-root is not real root. Check: the
   write fails.

## B. Reaching the tailnet or the LAN

10. Curl the tunnel gateway and DNS: `curl -m3 http://10.0.2.2/`, `curl -m3 http://10.0.2.3/`.
    Defence: nftables `inet hackbox` rejects private ranges for uid 2000. Check: fast
    "Permission denied" style refusal, not a hang.
11. Reach RFC1918 hosts a venue LAN would have: `curl -m3 http://192.168.0.1/`,
    `http://172.16.0.1/`, `http://10.0.0.1/`. Defence: same table. Check: refused fast.
12. Hit cloud metadata and CGNAT: `curl -m3 http://169.254.169.254/`,
    `curl -m3 http://100.100.100.100/`. Defence: link-local and 100.64/10 blocked.
    Check: refused.
13. Reach a tailnet peer by IP: `curl -m3 http://100.x.y.z/` for a known peer, and
    anything on `tailscale0`. Defence: `oifname tailscale0 reject` plus the tailnet
    ACL grants the tag nothing outbound. Check: refused at the box and, even from
    root, refused at the tailnet ACL.
14. Enumerate the tailnet: `tailscale status`, `tailscale ip`. Defence: tagged device
    with no grants sees little; the LocalAPI is read-only. Check: no usable route to
    peers; connections still refused.
15. Confirm the public internet still works (so the block is not just "no network"):
    `curl -sI https://github.com`, `curl -sI https://registry.npmjs.org`. Check: 200s.
16. Confirm DNS still resolves through the stub: `getent hosts github.com`. Check:
    returns an address (the loopback stub resolver must keep working).
17. Try to be reached from outside: start `python3 -m http.server 8080 --bind 0.0.0.0`
    and hit it from another laptop on the venue Wi-Fi. Defence: the input chain drops
    new inbound except loopback, tailnet and established. Check: the peer cannot connect.

## C. Surviving or denying the reset

18. Leave a marker and a running process, wait for the timer to expire, then on the
    next session look for them: `ls ~`, `ps -u hacker`, `crontab -l`. Defence: the
    home image is rebuilt and the uid is fully killed. Check: nothing survives.
19. Hold the home busy during reset: open many files, run a Chromium sandbox, keep a
    `while :; do :; done` loop. Defence: lazy unmount plus image replacement. Check:
    reset still completes and the next session is clean.
20. Fill the home to 100 percent, then trigger the reset (let the timer run out).
    Defence: `mkfs` on a fresh image does not care about the old contents. Check:
    reset completes; new home has free space.
21. Deny service to the next attendee with a fork bomb: `:(){ :|:& };:` (bounded is
    kinder to test). Defence: `TasksMax` on `user-2000.slice`. Check: the box and the
    admin SSH session stay responsive; the bomb dies inside the slice.
22. Deny service with a memory hog: allocate until the OOM killer runs. Defence:
    `MemoryMax` scopes the kill to the slice. Check: Xorg, LightDM and sshd survive;
    only the attendee's process is killed.
23. Stop the countdown: `systemctl stop hackbox-session.service`, kill its PID
    (`pgrep -f hackbox-session`). Defence: the unit is root-owned and outside the
    attendee slice; polkit denies stop. Check: the timer keeps running.
24. Extend your own time: `echo 99999 > /run/hackbox/session-end`, or edit the state.
    Defence: `/run/hackbox/*` is root-owned. Check: write denied; time does not change.
25. Persist through a user systemd unit and linger: write
    `~/.config/systemd/user/eternal.service`, `loginctl enable-linger`. Defence: the
    home wipe removes the unit; polkit denies set-self-linger; reset disables linger.
    Check: nothing runs after the reset.
26. Persist through SysV IPC or `/dev/shm`: `ipcmk -M 1048576`, write to `/dev/shm`.
    Defence: the reset clears IPC owned by the uid and the attendee's shm files.
    Check: `ipcs` shows nothing owned by `hacker` next session.

## D. Stealing or overspending the AI keys

27. Point Claude Code at an expensive model or buy extra usage: `/model opus`,
    `/usage-credits`. Defence: managed `availableModels` allowlist with
    `enforceAvailableModels`, and `DISABLE_EXTRA_USAGE_COMMAND`. Check: Opus is not
    selectable; the purchase command is hidden.
28. Exfiltrate the key and use it off the box: copy `/etc/hackbox/secrets/*` to a USB
    stick, then use it from your laptop. Defence: per-box, revocable, budgeted key.
    Check: the key spends only up to its cap and can be revoked; it is not the org's
    master key.
29. Turn off the guardrails: `--dangerously-skip-permissions`, `--plugin-dir`,
    `--mcp-config`, add an MCP server. Defence: `disableBypassPermissionsMode`,
    `disableSideloadFlags`, `allowManagedMcpServersOnly`. Check: all rejected at start.
30. Make the agent run `sudo` for you. Defence: managed `permissions.deny` includes
    `Bash(sudo *)` and `Bash(su *)`, and the account has no sudo anyway. Check: denied.

## E. Kiosk UI, TTY, USB, boot

31. Switch to a text console: Ctrl+Alt+F2 through F6. Defence: `NAutoVTs=0`,
    `ReserveVT=0`, `DontVTSwitch`. Check: no VT switch; nothing to log into.
32. Log out or switch users from the menu, or `cinnamon-session-quit --logout`.
    Defence: dconf `disable-log-out`, `disable-user-switching`; any session end is
    treated as reset. Check: no greeter appears; the box resets instead.
33. Lock the screen (Ctrl+Alt+L) to strand the box. Defence: `disable-lock-screen`,
    `lock-enabled=false`. Check: it does not lock.
34. Kill the overlay to free the desktop: `pkill -f hackbox-overlay`. Defence: the
    guard relaunches it within seconds. Check: it comes back.
35. Escape into a file manager or settings and change the login window, autologin or
    dconf. Defence: dconf locks plus the settings tools that write LightDM config are
    not to be used on kiosks. Check: locked keys do not stick.
36. Plug in a USB stick and try to autorun something. Defence: `automount-open=false`,
    `autorun-never=true`; Linux has no autorun. Check: the stick mounts `nosuid,nodev`
    under `/media/hacker` but nothing runs on its own.
37. Boot another OS: reboot and hit F12/F11/F7 for the boot menu; try a USB installer.
    Defence: BIOS supervisor password, boot order fixed to internal NVMe, USB and PXE
    boot off, Secure Boot on. Check (physical): the menu asks for the BIOS password;
    external media does not boot.
38. Hold the power button or pull the plug to skip the wipe. Defence: the boot-time
    reset rebuilds the home before LightDM starts. Check: a cold boot still yields a
    clean session.

## F. Reading the previous attendee's work

39. Read another home or the admin home: `ls -la /home`, `cat /home/hbadmin/*`.
    Defence: the admin home is `0750`; the previous attendee's home was destroyed.
    Check: permission denied on the admin home; no prior home exists.
40. Read the archives: `ls /var/lib/hackbox/archive`, `cat` one. Defence: archives are
    root-only. Check: permission denied. (If archives are encrypted to an organiser
    key, even root on a stolen box cannot read older ones.)
41. Scrape agent history or browser profile of a prior session: `~/.claude`,
    `~/.local/share/opencode`, the Chromium profile. Defence: all under the wiped home.
    Check: only your own fresh session data is present.
