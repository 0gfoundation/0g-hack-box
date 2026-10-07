#!/bin/bash
# Headless installer, live session part (root, from hackbox-live.service), while Ubiquity
# installs. Makes the box findable and watchable as early as possible:
#   1. hostname = the final hostname, so mDNS answers <hostname>.local during the install
#   2. Wi-Fi profile from the stick when there is no wired link (setup/03-wifi.sh, same code
#      as the installed box)
#   3. waits for the network (never blocks the install: Ubiquity needs no network)
#   4. installs openssh-server into the LIVE session only and authorises the owner's keys for
#      the live user "mint", so `ssh mint@<hostname>.local` shows the install progress
#   5. writes /var/log/hackbox-install.log (stage lines plus Ubiquity's progress), reports
#      each stage to REPORT_URL and beeps.
set -u
S=/cdrom/hackbox
export HB_PROGRESS=/var/log/hackbox-install.log
touch "$HB_PROGRESS"; chmod 0644 "$HB_PROGRESS"
set -a; . "$S/headless.env"; set +a
. "$S/headless/hb-common.sh"
# stdout/stderr go to the journal (journalctl -u hackbox-live); late.sh keeps a copy.

host=$(bash "$S/headless/hostname.sh" "${HB_HOSTNAME:-}")
hostnamectl set-hostname "$host" 2>/dev/null || hostname "$host"
sed -i "s/^127\.0\.1\.1.*/127.0.1.1\t$host/" /etc/hosts
systemctl try-restart avahi-daemon 2>/dev/null
hb_line "INSTALLER started hostname=$host"

# Wi-Fi (only matters when there is no cable; wired stays preferred by route metric).
if [ -f "$S/wifi.conf" ]; then
  install -d -m 0755 /etc/hackbox
  install -m 0600 "$S/wifi.conf" /etc/hackbox/wifi.conf
  # Give a cable a few seconds to get DHCP first, so a wired install does not wait on Wi-Fi.
  for i in $(seq 10); do ip -4 route show default | grep -q . && break; sleep 1; done
  if bash "$S/repo/setup/03-wifi.sh"; then hb_line "INSTALLER wifi-profile ok"; else hb_line "INSTALLER wifi-profile failed"; fi
fi

# Network: up to 10 minutes, in the background of the install.
net=0
for i in $(seq 120); do
  if ip -4 route show default | grep -q . && getent ahosts archive.ubuntu.com >/dev/null 2>&1; then net=1; break; fi
  sleep 5
done
if [ $net = 0 ]; then
  hb_line "INSTALLER network none (install continues, ssh and setup wait for the first boot)"
  exit 0
fi
hb_line "INSTALLER network up ips=$(hb_ips)"
hb_report installer-network
hb_beep s

# ssh into the live session (never copied to the installed system: Ubiquity copies the
# read-only squashfs, not this session's changes).
if [ -f "$S/authorized_keys" ]; then
  install -d -m 700 -o mint -g mint /home/mint/.ssh
  install -m 600 -o mint -g mint "$S/authorized_keys" /home/mint/.ssh/authorized_keys
  # Ubiquity holds the live debconf database for the whole install, so openssh-server's
  # postinst cannot run here. Instead: download the package, unpack it under /var/lib/hackbox-live-ssh and run
  # that sshd directly (key only, no PAM). Nothing is installed with dpkg.
  # The live session's apt sources include the stick itself as a cdrom: entry that has no
  # Release file, so `apt-get update` always fails there. Drop it (live session only).
  sed -i -E 's/^(deb[[:space:]]+cdrom:)/# \1/' /etc/apt/sources.list /etc/apt/sources.list.d/*.list 2>/dev/null
  D=/var/lib/hackbox-live-ssh; mkdir -p "$D/root"; chmod 700 "$D"   # not /run: noexec
  ok=0
  for i in $(seq 16); do
    if timeout 300 apt-get -o Acquire::Retries=3 update -qq &&
       (cd "$D" && timeout 300 apt-get -o Acquire::Retries=3 download openssh-server); then
      ok=1; break
    fi
    echo "apt attempt $i failed, retrying in 20 s"
    sleep 20
  done
  if [ $ok = 1 ]; then
    ok=0
    dpkg-deb -x "$D"/openssh-server_*.deb "$D/root" &&
    ssh-keygen -q -t ed25519 -N '' -f "$D/host_ed25519" &&
    { id sshd >/dev/null 2>&1 || useradd -r -M -d /run/sshd -s /usr/sbin/nologin sshd; } &&
    install -d -m 0755 /run/sshd &&
    printf '%s\n' "Port 22" "HostKey $D/host_ed25519" "UsePAM no" "PasswordAuthentication no" \
      "KbdInteractiveAuthentication no" "PermitRootLogin no" "AllowUsers mint" \
      "AuthorizedKeysFile .ssh/authorized_keys" "PidFile $D/sshd.pid" > "$D/sshd_config" &&
    { ldd "$D/root/usr/sbin/sshd" | grep 'not found' && echo "sshd is missing libraries"; true; } &&
    "$D/root/usr/sbin/sshd" -t -f "$D/sshd_config" &&
    systemd-run --unit=hackbox-live-sshd "$D/root/usr/sbin/sshd" -D -e -f "$D/sshd_config" &&
    sleep 1 && systemctl is-active --quiet hackbox-live-sshd && ok=1
  fi
  if [ $ok = 1 ]; then
    hb_line "INSTALLER ssh ready: ssh mint@$host.local (or mint@<ip>) ips=$(hb_ips)"
    hb_report installer-ssh
  else
    hb_line "INSTALLER ssh unavailable (journalctl -u hackbox-live); watch again after the reboot"
  fi
fi

# Ubiquity's progress, one line per change, until the late step takes over.
last=""
while :; do
  p=$(grep -a 'PROGRESS INFO' /var/log/installer/debug 2>/dev/null | tail -n 1 | sed 's/.*PROGRESS INFO //')
  if [ -n "$p" ] && [ "$p" != "$last" ]; then hb_line "INSTALLER progress $p"; last=$p; fi
  sleep 5
done
