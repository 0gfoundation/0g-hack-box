#!/bin/sh
# Runs in the live session as root via ubiquity/success_command, target mounted at /target.
T=/target
mkdir -p "$T/var/log/installer"
exec >>"$T/var/log/installer/hackbox-late.log" 2>&1
set -x

getarg() { for x in $(cat /proc/cmdline); do case "$x" in "$1"=*) echo "${x#*=}";; esac; done; }
MODE="$(getarg hb_mode)"; MODE="${MODE:-usb}"
HOST="$(getarg hb_host)"
if [ -z "$HOST" ]; then
  nic=""
  for n in /sys/class/net/*; do [ -e "$n/device" ] && { nic="$(basename "$n")"; break; }; done
  mac="$(cat "/sys/class/net/$nic/address" 2>/dev/null | tr -d ':')"
  HOST="hackbox-$(echo "$mac" | tail -c 5)"
fi

# Hostname
echo "$HOST" > "$T/etc/hostname"
if grep -q '^127\.0\.1\.1' "$T/etc/hosts"; then
  sed -i "s/^127\.0\.1\.1.*/127.0.1.1\t$HOST/" "$T/etc/hosts"
else
  printf '127.0.1.1\t%s\n' "$HOST" >> "$T/etc/hosts"
fi

# UEFI removable fallback path (what grub-install --removable would add)
esp="$T/boot/efi/EFI"
src=""
for d in "$esp"/*; do [ -f "$d/shimx64.efi" ] && [ -f "$d/grubx64.efi" ] && { src="$d"; break; }; done
if [ -n "$src" ]; then
  mkdir -p "$esp/BOOT"
  cp "$src/shimx64.efi" "$esp/BOOT/BOOTX64.EFI"
  cp "$src/grubx64.efi" "$esp/BOOT/grubx64.efi"
  [ -f "$src/mmx64.efi" ] && cp "$src/mmx64.efi" "$esp/BOOT/mmx64.efi"
  [ -f "$src/grub.cfg" ] && cp "$src/grub.cfg" "$esp/BOOT/grub.cfg"
fi
ls -lR "$T/boot/efi" || true

# Timezone and locale (enforce even if the installer used geoip)
ln -sf /usr/share/zoneinfo/Asia/Singapore "$T/etc/localtime"
echo Asia/Singapore > "$T/etc/timezone"
# Ubiquity derives the locale from the timezone's country (Asia/Singapore -> en_SG), so force en_US.
chroot "$T" locale-gen en_US.UTF-8 || true
printf 'LANG="en_US.UTF-8"\nLANGUAGE="en_US:en"\n' > "$T/etc/default/locale"

if [ "$MODE" = lab ]; then
  # LAB ONLY: passwordless sudo, ssh keys and sshd so test runs are scriptable.
  # On real hardware the repo's 40-ssh.sh sets up ssh access instead.
  echo 'hackadmin ALL=(ALL) NOPASSWD: ALL' > "$T/etc/sudoers.d/90-hackadmin-lab"
  chmod 0440 "$T/etc/sudoers.d/90-hackadmin-lab"
  install -d -m 700 "$T/home/hackadmin/.ssh"
  cp /usr/local/share/hackbox/authorized_keys "$T/home/hackadmin/.ssh/authorized_keys"
  chmod 600 "$T/home/hackadmin/.ssh/authorized_keys"
  chroot "$T" chown -R hackadmin:hackadmin /home/hackadmin/.ssh

  mounted=""
  for m in dev dev/pts proc sys; do
    if ! mountpoint -q "$T/$m"; then mount --bind "/$m" "$T/$m" && mounted="$T/$m $mounted"; fi
  done
  # DNS inside the chroot: the chroot shares the live network namespace, so reuse live resolv.conf
  if [ -L "$T/etc/resolv.conf" ] || [ -e "$T/etc/resolv.conf" ]; then mv "$T/etc/resolv.conf" "$T/etc/resolv.conf.hackbox"; fi
  cp -L /etc/resolv.conf "$T/etc/resolv.conf"
  printf '#!/bin/sh\nexit 101\n' > "$T/usr/sbin/policy-rc.d"; chmod 755 "$T/usr/sbin/policy-rc.d"
  ok=0
  for i in 1 2 3; do
    chroot "$T" apt-get update && \
    DEBIAN_FRONTEND=noninteractive chroot "$T" apt-get install -y openssh-server && { ok=1; break; }
    sleep 10
  done
  echo "openssh-server install ok=$ok"
  chroot "$T" systemctl enable ssh || true
  rm -f "$T/usr/sbin/policy-rc.d" "$T/etc/resolv.conf"
  [ -e "$T/etc/resolv.conf.hackbox" ] || [ -L "$T/etc/resolv.conf.hackbox" ] && mv "$T/etc/resolv.conf.hackbox" "$T/etc/resolv.conf"
  for m in $mounted; do umount "$m" || umount -l "$m"; done
fi

cp /var/log/hackbox-early.log "$T/var/log/installer/" 2>/dev/null || true
# Headless USB installer: first-boot provisioning, keys, Wi-Fi, marker, boot order.
if [ "$MODE" = headless ] && [ -f /cdrom/hackbox/headless/late.sh ]; then
  bash /cdrom/hackbox/headless/late.sh || echo "headless late step rc=$?"
fi
echo "hackbox-late done: mode=$MODE host=$HOST"
# marker for the VM lab (install-base.sh checks the serial log for it)
echo "HACKBOX-LATE-DONE mode=$MODE host=$HOST" > /dev/ttyS0 2>/dev/null || true
exit 0
