#!/bin/bash
# Headless installer, late part: runs in the live session as root from seed/hackbox-late.sh
# (ubiquity/success_command) after the base late step, with the installed system at /target.
# Prepares the installed box to finish by itself on its first boot:
#   - final hostname, admin password hash, the owner's ssh keys, passwordless sudo for the admin
#   - the repo tree on /opt/hack-box (from the stick), Wi-Fi conf + profile, first-boot unit
#   - openssh-server now if the network is up (else the first boot installs it)
#   - a marker on the ESP (EFI/hackbox/installed) that makes the stick refuse to reinstall
#   - UEFI boot order: the installed system first, so the stick left in does not loop
# Never fails the install: every step logs and carries on.
set -u
T=/target
S=/cdrom/hackbox
export HB_PROGRESS=/var/log/hackbox-install.log
set -a; . "$S/headless.env"; set +a
. "$S/headless/hb-common.sh"
exec >>"$T/var/log/installer/hackbox-headless-late.log" 2>&1
set -x
hb_line "INSTALLER late step started"
in_target() { chroot "$T" "$@"; }

ADMIN=${ADMIN_USER:-hackadmin}
host=$(bash "$S/headless/hostname.sh" "${HB_HOSTNAME:-}")
echo "$host" > "$T/etc/hostname"
if grep -q '^127\.0\.1\.1' "$T/etc/hosts"; then
  sed -i "s/^127\.0\.1\.1.*/127.0.1.1\t$host/" "$T/etc/hosts"
else
  printf '127.0.1.1\t%s\n' "$host" >> "$T/etc/hosts"
fi

# Admin password: the hash from the config replaces the seed's placeholder.
set +x
if [ -n "${ADMIN_PASSWORD_HASH:-}" ]; then
  in_target usermod -p "$ADMIN_PASSWORD_HASH" "$ADMIN" && echo "admin password hash set"
fi
set -x

# Settings for the first boot (root only).
install -d -m 0755 "$T/etc/hackbox"
install -m 0600 "$S/headless.env" "$T/etc/hackbox/headless.env"
sed -i '/^ADMIN_PASSWORD_HASH=/d' "$T/etc/hackbox/headless.env"
[ -f "$S/wifi.conf" ] && install -m 0600 "$S/wifi.conf" "$T/etc/hackbox/wifi.conf"
[ -f "$S/ts-authkey" ] && install -m 0600 "$S/ts-authkey" "$T/etc/hackbox/ts-authkey"
# Session hub: what `hackbox hub set` writes; 70-session.sh starts the agent when it sees it.
if [ -f "$S/hub.conf" ]; then
  install -m 0600 "$S/hub.conf" "$T/etc/hackbox/hub.conf"
  install -d -m 0755 "$T/etc/hackbox/conf.d"
  printf 'HUB_ENABLED=1\nEND_GRACE_SECONDS=180\n' > "$T/etc/hackbox/conf.d/38-hub.conf"
fi
install -m 0644 "$S/authorized_keys" "$T/etc/hackbox/headless-authorized_keys"

# Repo tree, owned by the admin (bootstrap's layout: /opt/hack-box).
uid=$(in_target id -u "$ADMIN"); gid=$(in_target id -g "$ADMIN")
rm -rf "$T/opt/hack-box"
mkdir -p "$T/opt/hack-box"
cp -a "$S/repo/." "$T/opt/hack-box/"
chown -R "$uid:$gid" "$T/opt/hack-box"
chmod -R u+w "$T/opt/hack-box"

# ssh keys and passwordless sudo for the admin (40-ssh.sh writes the same later).
install -d -m 700 -o "$uid" -g "$gid" "$T/home/$ADMIN/.ssh"
install -m 600 -o "$uid" -g "$gid" "$S/authorized_keys" "$T/home/$ADMIN/.ssh/authorized_keys"
echo "$ADMIN ALL=(ALL) NOPASSWD: ALL" > "$T/etc/sudoers.d/$ADMIN"
chmod 0440 "$T/etc/sudoers.d/$ADMIN"
install -d "$T/etc/ssh/sshd_config.d"
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\nDenyUsers hacker\n' > "$T/etc/ssh/sshd_config.d/hack-box.conf"

# Wi-Fi profile in the installed system (file only, NetworkManager loads it at boot).
if [ -f "$T/etc/hackbox/wifi.conf" ]; then
  HB_WIFI_OFFLINE=1 HACKBOX_WIFI_CONF="$T/etc/hackbox/wifi.conf" HACKBOX_NM_DIR="$T/etc/NetworkManager/system-connections" \
    HACKBOX_MODPROBE_DIR="$T/etc/modprobe.d" bash "$S/repo/setup/03-wifi.sh" || true
fi

# First-boot unit.
install -d "$T/usr/local/sbin" "$T/usr/local/lib/hackbox"
install -m 0755 "$S/headless/hackbox-firstboot" "$T/usr/local/sbin/hackbox-firstboot"
install -m 0644 "$S/headless/hb-common.sh" "$T/usr/local/lib/hackbox/hb-common.sh"
install -m 0644 "$S/headless/hackbox-firstboot.service" "$T/etc/systemd/system/hackbox-firstboot.service"
ln -sf /etc/systemd/system/hackbox-firstboot.service "$T/etc/systemd/system/multi-user.target.wants/hackbox-firstboot.service"
# Keep the installer's own log with the box.
cp "$HB_PROGRESS" "$T/var/log/installer/hackbox-install.log" 2>/dev/null || true

# openssh-server now, if the live session has a network (same method as the lab mode).
if ip -4 route show default | grep -q . && getent ahosts archive.ubuntu.com >/dev/null 2>&1; then
  mounted=""
  for m in dev dev/pts proc sys; do
    if ! mountpoint -q "$T/$m"; then mount --bind "/$m" "$T/$m" && mounted="$T/$m $mounted"; fi
  done
  if [ -L "$T/etc/resolv.conf" ] || [ -e "$T/etc/resolv.conf" ]; then mv "$T/etc/resolv.conf" "$T/etc/resolv.conf.hackbox"; fi
  cp -L /etc/resolv.conf "$T/etc/resolv.conf"
  printf '#!/bin/sh\nexit 101\n' > "$T/usr/sbin/policy-rc.d"; chmod 755 "$T/usr/sbin/policy-rc.d"
  ok=0
  for i in 1 2; do
    DEBIAN_FRONTEND=noninteractive timeout 300 chroot "$T" apt-get update -qq &&
    DEBIAN_FRONTEND=noninteractive timeout 300 chroot "$T" apt-get install -y -qq openssh-server && { ok=1; break; }
    sleep 10
  done
  [ $ok = 1 ] && chroot "$T" systemctl enable ssh
  rm -f "$T/usr/sbin/policy-rc.d" "$T/etc/resolv.conf"
  { [ -e "$T/etc/resolv.conf.hackbox" ] || [ -L "$T/etc/resolv.conf.hackbox" ]; } && mv "$T/etc/resolv.conf.hackbox" "$T/etc/resolv.conf"
  for m in $mounted; do umount "$m" || umount -l "$m"; done
  hb_line "INSTALLER openssh-server in the installed system ok=$ok"
else
  hb_line "INSTALLER no network: openssh-server is installed at the first boot"
fi

# Marker: the stick's headless entry boots the disk instead of reinstalling when it finds this.
esp="$T/boot/efi"
if mountpoint -q "$esp"; then
  mkdir -p "$esp/EFI/hackbox"
  printf 'hack-box installed %s host=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$host" > "$esp/EFI/hackbox/installed"
  hb_line "INSTALLER marker written: EFI/hackbox/installed"
fi

# UEFI boot order: the entry for this disk's shim first.
if [ -d /sys/firmware/efi/efivars ] && command -v efibootmgr >/dev/null; then
  efibootmgr -v || true
  espdev=$(findmnt -n -o SOURCE "$esp")
  partuuid=$(blkid -s PARTUUID -o value "$espdev" 2>/dev/null | tr 'A-Z' 'a-z')
  entry=""
  if [ -n "$partuuid" ]; then
    entry=$(efibootmgr -v | tr 'A-Z' 'a-z' | awk -v p="$partuuid" '/^boot[0-9a-f][0-9a-f][0-9a-f][0-9a-f]/ && index($0, p) && index($0, "\\efi\\ubuntu\\shimx64.efi") { print substr($1, 5, 4); exit }')
  fi
  if [ -z "$entry" ] && [ -n "$espdev" ]; then
    pk=$(lsblk -n -o PKNAME "$espdev" | head -n 1); pn=$(cat "/sys/class/block/$(basename "$espdev")/partition" 2>/dev/null)
    efibootmgr -c -d "/dev/$pk" -p "${pn:-1}" -L "hack-box" -l '\EFI\ubuntu\shimx64.efi' >/dev/null && \
      entry=$(efibootmgr | awk '/^BootOrder:/ {split($2, a, ","); print a[1]}')
  fi
  if [ -n "$entry" ]; then
    entry=$(echo "$entry" | tr 'a-z' 'A-Z')
    rest=$(efibootmgr | awk '/^BootOrder:/ {print $2}' | tr ',' '\n' | grep -vix "$entry" | paste -sd, -)
    efibootmgr -o "$entry${rest:+,$rest}" >/dev/null && hb_line "INSTALLER boot order: Boot$entry (installed system) first"
  else
    hb_line "INSTALLER boot order: no entry for the installed system found (the stick's marker check still protects the disk)"
  fi
  efibootmgr -v || true
fi

journalctl -b -u hackbox-live --no-pager > "$T/var/log/installer/hackbox-live.journal" 2>&1 || true
hb_line "INSTALLER finished, rebooting into the installed system (host $host)"
hb_report installed
hb_beep l
cp "$HB_PROGRESS" "$T/var/log/installer/hackbox-install.log" 2>/dev/null || true
exit 0
