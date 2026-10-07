#!/usr/bin/env bash
# Kiosk lockdown for the attendee account (SPEC section 5). Each control below is its own
# block and is idempotent; nothing restarts the display manager, logind or sshd, so it is
# safe on a box that is mid-session (some controls take effect at the next reset or boot,
# as noted). Values come from files/conf/30-lockdown.conf, installed to
# /etc/hackbox/conf.d/, with box overrides in /etc/hackbox/conf.d/39-lockdown-local.conf.
# Check the result with `hackbox lock status`.
#
# What this does not do: the polkit rules, logind KillUserProcesses and the tmpfs mounts
# belong to the session engine (setup/70-session.sh).
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
L="$HERE/files/lockdown"

# Config
sudo install -d -m 0755 /etc/hackbox /etc/hackbox/conf.d
sudo install -m 0644 "$HERE/files/conf/30-lockdown.conf" /etc/hackbox/conf.d/30-lockdown.conf
set -a
for f in /etc/hackbox/conf.d/*.conf; do [ -r "$f" ] && . "$f"; done
set +a
HACKER_USER=${HACKER_USER:-hacker}
HACKER_UID=${HACKER_UID:-2000}
if ! id "$HACKER_USER" >/dev/null 2>&1; then
  echo "user $HACKER_USER does not exist yet: run setup/60-hacker.sh first" >&2
  exit 1
fi
if [ "$(id -u "$HACKER_USER")" != "$HACKER_UID" ]; then
  echo "$HACKER_USER has uid $(id -u "$HACKER_USER"), expected $HACKER_UID" >&2
  exit 1
fi

# Account: no privileged groups, no sudo
# The account is created unprivileged by 60-hacker.sh; this makes sure nothing added it
# to a group that grants sudo, logs or printer admin since.
for g in sudo admin wheel adm lpadmin sambashare; do
  if [[ " $(id -nG "$HACKER_USER") " == *" $g "* ]]; then
    sudo gpasswd -d "$HACKER_USER" "$g" >/dev/null
  fi
done
# Mint's own drop-ins (mintupdate, mintdrivers) grant every user passwordless sudo for
# a few helper scripts (`ALL ALL = NOPASSWD:...`), which includes the attendee. Exclude
# the attendee from those user lists and leave everything else as Mint shipped it. Each
# edited file is checked with visudo before it replaces the original.
for f in /etc/sudoers.d/*; do
  [ -f "$f" ] || continue
  sudo grep -Eq '^ALL[[:space:]]+ALL[[:space:]]*=' "$f" || continue
  tmp_sudoers=$(mktemp)
  sudo sed -E "s/^ALL([[:space:]]+ALL[[:space:]]*=)/ALL,!$HACKER_USER\1/" "$f" > "$tmp_sudoers"
  if sudo visudo -cqf "$tmp_sudoers" >/dev/null; then
    sudo install -m 0440 -o root -g root "$tmp_sudoers" "$f"
  else
    echo "warn: could not exclude $HACKER_USER from $f (visudo rejected the edit)" >&2
  fi
  rm -f "$tmp_sudoers"
done
# Output captured first: `| grep -q` under pipefail can fail on SIGPIPE.
mentions=$(sudo grep -rsw -- "$HACKER_USER" /etc/sudoers /etc/sudoers.d | grep -v "ALL,!$HACKER_USER" || true)
if [ -n "$mentions" ]; then
  echo "warn: a sudoers file mentions $HACKER_USER, remove it by hand: $mentions" >&2
fi

# Account: su only for the sudo group
# pam_wheel group=sudo: the attendee cannot even try to su (brute force of the admin
# password is off the table). requisite, not Ubuntu's commented `required`, so su refuses
# at once without a password prompt. Root keeps su through pam_rootok, which comes first.
if ! grep -Eq '^[[:space:]]*auth[[:space:]]+(required|requisite)[[:space:]]+pam_wheel\.so.*group=sudo' /etc/pam.d/su; then
  if grep -Eq '^#[[:space:]]*auth[[:space:]]+required[[:space:]]+pam_wheel\.so[[:space:]]*$' /etc/pam.d/su; then
    sudo sed -i -E 's/^#[[:space:]]*auth[[:space:]]+required[[:space:]]+pam_wheel\.so[[:space:]]*$/auth       requisite  pam_wheel.so group=sudo/' /etc/pam.d/su
  else
    sudo sed -i '/pam_rootok\.so/a auth       requisite  pam_wheel.so group=sudo' /etc/pam.d/su
  fi
fi

# Account: no cron, no at, no linger
for f in /etc/cron.deny /etc/at.deny; do
  if ! sudo grep -qx "$HACKER_USER" "$f" 2>/dev/null; then
    echo "$HACKER_USER" | sudo tee -a "$f" >/dev/null
  fi
done
sudo loginctl disable-linger "$HACKER_USER" 2>/dev/null || true
sudo rm -f "/var/lib/systemd/linger/$HACKER_USER"

# Account: the admin home is not readable by the attendee
if [ $(( 0$(stat -c '%a' "$HOME") & 7 )) -ne 0 ]; then
  chmod o-rwx "$HOME"
fi

# VT switching off
# No gettys on tty2..6 (logind, next boot) and no Ctrl+Alt+Fn or Ctrl+Alt+Backspace in X
# (next X start, which every reset does).
sudo install -d -m 0755 /etc/systemd/logind.conf.d /etc/X11/xorg.conf.d
sudo install -m 0644 "$L/logind-vt.conf" /etc/systemd/logind.conf.d/80-hackbox-vt.conf
sudo install -m 0644 "$L/xorg-10-hackbox.conf" /etc/X11/xorg.conf.d/10-hackbox.conf

# Resources: attendee slice limits
# The attendee's whole login (desktop, terminals, builds, browser) lives in
# user-UID.slice. Memory and task caps there mean the kernel OOM killer and the fork bomb
# ceiling act inside the slice only, so sshd, Xorg and LightDM (system.slice) and the root
# reset path stay responsive. CPUQuota leaves HACKER_CPU_RESERVE threads for the system.
mem_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
mem_max=${HACKER_MEM_MAX:-$(( mem_kb * ${HACKER_MEM_PCT:-75} / 100 / 1024 ))M}
if [ -z "${HACKER_MEM_HIGH:-}" ]; then
  if [ -z "${HACKER_MEM_MAX:-}" ]; then
    mem_high="$(( mem_kb * ${HACKER_MEM_PCT:-75} / 100 / 1024 * 11 / 12 ))M"
  else
    mem_high=$HACKER_MEM_MAX   # an explicit max without a high: no early throttling
  fi
else
  mem_high=$HACKER_MEM_HIGH
fi
threads=$(nproc --all)
cpu_quota=${HACKER_CPU_QUOTA:-$(( (threads - ${HACKER_CPU_RESERVE:-2}) * 100 ))%}
[ "${cpu_quota%\%}" -lt 100 ] 2>/dev/null && cpu_quota=100%
slice="user-$HACKER_UID.slice"
dropin=$(mktemp)
cat > "$dropin" <<EOF
# hack-box attendee limits, generated by setup/80-lockdown.sh from 30-lockdown.conf.
# Machine: $(( mem_kb / 1024 ))M RAM, $threads hardware threads.
[Slice]
MemoryHigh=$mem_high
MemoryMax=$mem_max
MemorySwapMax=${HACKER_SWAP_MAX:-2G}
CPUQuota=$cpu_quota
CPUWeight=80
IOWeight=80
TasksMax=${HACKER_TASKS_MAX:-4096}
EOF
sudo install -d -m 0755 "/etc/systemd/system/$slice.d"
sudo install -m 0644 "$dropin" "/etc/systemd/system/$slice.d/50-hackbox.conf"
rm -f "$dropin"
sudo systemctl daemon-reload
# Apply to a slice that is already running (mid-session), without a restart.
if systemctl is-active --quiet "$slice"; then
  sudo systemctl set-property --runtime "$slice" \
    MemoryHigh="$mem_high" MemoryMax="$mem_max" MemorySwapMax="${HACKER_SWAP_MAX:-2G}" \
    CPUQuota="$cpu_quota" CPUWeight=80 IOWeight=80 TasksMax="${HACKER_TASKS_MAX:-4096}"
fi
# Second ceiling outside systemd accounting (pam_limits, at login).
echo "$HACKER_USER hard nproc ${HACKER_NPROC:-4096}" | sudo tee /etc/security/limits.d/90-hackbox.conf >/dev/null

# Network: table inet hackbox
# Own table and own unit; never /etc/nftables.conf and never `flush ruleset` (that would
# wipe Tailscale's tables). See files/lockdown/hackbox-nft-render for the rules.
# Guard: when this runs over ssh from outside the tailnet (the VM lab, a LAN install),
# keep tcp/22 open inbound so the operator is not locked out, and say so.
inbound=" ${NFT_INBOUND_TCP:-} "
if [ -n "${SSH_CONNECTION:-}" ]; then
  client=${SSH_CONNECTION%% *}
  case "$client" in
    127.*|::1|100.*|fd7a:115c:a1e0:*) ;;
    *)
      if [[ "$inbound" != *" 22 "* ]]; then
        echo "warn: installing over ssh from $client (not the tailnet): keeping tcp/22 open inbound." >&2
        echo "warn: set NFT_INBOUND_TCP in /etc/hackbox/conf.d/39-lockdown-local.conf to make this explicit." >&2
        inbound="$inbound 22 "
      fi
      ;;
  esac
fi
sudo apt-get install -y -qq nftables >/dev/null
if systemctl is-enabled --quiet nftables.service 2>/dev/null; then
  echo "warn: nftables.service is enabled; its /etc/nftables.conf flushes Tailscale's rules at boot. Disable it: sudo systemctl disable nftables.service" >&2
fi
nft_tmp=$(mktemp)
HACKER_UID=$HACKER_UID NFT_INBOUND_TCP="$inbound" bash "$L/hackbox-nft-render" > "$nft_tmp"
sudo install -m 0644 "$nft_tmp" /etc/hackbox/hackbox.nft
rm -f "$nft_tmp"
sudo install -m 0644 "$L/hackbox-nft.service" /etc/systemd/system/hackbox-nft.service
sudo install -d -m 0755 /etc/NetworkManager/dispatcher.d
sudo install -m 0755 "$L/90-hackbox-gw" /etc/NetworkManager/dispatcher.d/90-hackbox-gw
sudo systemctl daemon-reload
sudo systemctl enable hackbox-nft.service >/dev/null 2>&1
if systemctl is-active --quiet hackbox-nft.service; then
  sudo systemctl reload hackbox-nft.service
else
  sudo systemctl start hackbox-nft.service
fi
sudo /etc/NetworkManager/dispatcher.d/90-hackbox-gw any up

# Desktop: system dconf database with locks
# Moves 50-desktop.sh's settings into a system database so they apply to the attendee,
# and locks the ones a session must not change (lock screen, idle, power, logout, user
# switching, auto-open). Theme keys are defaults, not locked. 50-desktop.sh keeps running
# for the admin; its writes to locked keys now warn and carry on.
# dconf locks are UX, not security: the wall is polkit, cgroups, nftables and the wipe.
sudo install -d -m 0755 /etc/dconf/profile /etc/dconf/db/local.d/locks
if [ ! -f /etc/dconf/profile/user ]; then
  printf 'user-db:user\nsystem-db:local\n' | sudo tee /etc/dconf/profile/user >/dev/null
elif ! grep -qx 'system-db:local' /etc/dconf/profile/user; then
  echo 'system-db:local' | sudo tee -a /etc/dconf/profile/user >/dev/null
fi
sudo install -m 0644 "$L/dconf/00-hackbox" /etc/dconf/db/local.d/00-hackbox
sudo install -m 0644 "$L/dconf/locks-00-hackbox" /etc/dconf/db/local.d/locks/00-hackbox
sudo dconf update

# Desktop: a lock screen can never strand the attendee
# Cinnamon 6.6 locks the session from the menu's lock button and from
# cinnamon-screensaver-command even with disable-lock-screen=true (seen on Mint 22.3),
# and the attendee's password is locked, so a locked kiosk needed staff. The attendee has
# nothing to protect with a lock screen, so its unlock succeeds for that user only, in
# cinnamon-screensaver's own PAM service (login, su, sudo and sshd are untouched).
pam_ss=/etc/pam.d/cinnamon-screensaver
if [ -f "$pam_ss" ] && ! grep -q "pam_succeed_if.so quiet user = $HACKER_USER" "$pam_ss"; then
  sudo sed -i "1i auth sufficient pam_succeed_if.so quiet user = $HACKER_USER" "$pam_ss"
fi

# Chromium managed policy
# No sign-in, sync, password manager or autofill; DevTools allowed (attendees build web
# apps); extensions blocked except the allowlist (MetaMask by default). No
# ForceEphemeralProfiles: the home wipe already gives every session a fresh profile, and
# ephemeral profiles would drop logins if Chromium restarts mid-session. Merged by
# Chromium with 30-tools.sh's hack-box.json (same values where they overlap).
pol_tmp=$(mktemp)
HOME_URL="${CHROMIUM_HOME_URL:-https://docs.0g.ai/}" EXT_ALLOW="${CHROMIUM_EXTENSION_ALLOWLIST:-}" python3 - > "$pol_tmp" <<'PY'
import json, os
home = os.environ["HOME_URL"]
print(json.dumps({
    "BrowserSignin": 0,
    "SyncDisabled": True,
    "BrowserGuestModeEnabled": False,
    "BrowserAddPersonEnabled": False,
    "DefaultBrowserSettingEnabled": False,
    "PasswordManagerEnabled": False,
    "PasswordLeakDetectionEnabled": False,
    "AutofillAddressEnabled": False,
    "AutofillCreditCardEnabled": False,
    "PaymentMethodQueryEnabled": False,
    "ImportSavedPasswords": False,
    "MetricsReportingEnabled": False,
    "BackgroundModeEnabled": False,
    "PromptForDownloadLocation": False,
    "DownloadDirectory": "${home}/Downloads",
    "PrintingEnabled": False,
    "DeveloperToolsAvailability": 1,
    "RestoreOnStartup": 4,
    "RestoreOnStartupURLs": [home],
    "HomepageLocation": home,
    "ExtensionInstallBlocklist": ["*"],
    "ExtensionInstallAllowlist": os.environ["EXT_ALLOW"].split(),
    "DefaultGeolocationSetting": 2,
    "SafeBrowsingProtectionLevel": 1,
}, indent=2))
PY
sudo install -d -m 0755 /etc/chromium/policies/managed
sudo install -m 0644 "$pol_tmp" /etc/chromium/policies/managed/hackbox-lockdown.json
rm -f "$pol_tmp"

# USB storage
# allow (default): sticks mount under /media (udisks, nosuid,nodev) so attendees can copy
# work out; the dconf keys above stop auto-open and autorun. Mounting as the attendee also
# needs the session engine's polkit rule to allow the udisks2 mount actions.
# block: kernel modules blacklisted (also stops the organisers' own sticks).
usb_conf=/etc/modprobe.d/hackbox-usb-storage.conf
case "${USB_STORAGE:-allow}" in
  allow)
    if [ -f "$usb_conf" ]; then sudo rm -f "$usb_conf"; sudo update-initramfs -u; fi ;;
  block)
    want=$'blacklist usb_storage\nblacklist uas\ninstall usb_storage /bin/false\ninstall uas /bin/false'
    if [ "$(cat "$usb_conf" 2>/dev/null)" != "$want" ]; then
      printf '%s\n' "$want" | sudo tee "$usb_conf" >/dev/null
      sudo update-initramfs -u
    fi ;;
  *) echo "USB_STORAGE must be allow or block" >&2; exit 1 ;;
esac

# Report
bash "$HERE/lib/hackbox.d/lock" status || true
