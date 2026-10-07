#!/bin/sh
# Runs inside the casper initramfs (busybox sh) via preseed/early_command.
# /root is the live root filesystem, /root/cdrom the install media.
LOG=/root/var/log/hackbox-early.log
mkdir -p /root/var/log
exec >>"$LOG" 2>&1
echo "hackbox-early: $(cat /proc/cmdline)"

getarg() { for x in $(cat /proc/cmdline); do case "$x" in "$1"=*) echo "${x#*=}";; esac; done; }

disk="$(getarg hb_disk)"
disk="${disk#/dev/}"
if [ -z "$disk" ]; then
  # block device that holds the boot media (to exclude it)
  # (busybox in the initramfs has no head/tail: keep this to awk)
  grep -E ' /root/cdrom | /cdrom ' /proc/mounts || true
  media_dev="$(awk '$2=="/root/cdrom" || $2=="/cdrom" {print $1; exit}' /proc/mounts)"
  media_dev="${media_dev#/dev/}"
  media_parent=""
  if [ -n "$media_dev" ] && [ -e "/sys/class/block/$media_dev" ]; then
    if [ -e "/sys/class/block/$media_dev/partition" ]; then
      media_parent="$(basename "$(readlink -f "/sys/class/block/$media_dev/..")")"
    else
      media_parent="$media_dev"
    fi
  fi
  echo "boot media: dev=$media_dev parent=$media_parent"
  for pat in 'nvme*n*' 'sd*' 'vd*' 'hd*' 'mmcblk*'; do
    for p in /sys/block/$pat; do
      [ -e "$p" ] || continue
      d="$(basename "$p")"
      case "$d" in mmcblk*boot*|mmcblk*rpmb) continue;; esac
      [ "$d" = "$media_parent" ] && { echo "skip $d (boot media)"; continue; }
      [ "$(cat "$p/removable" 2>/dev/null)" = 1 ] && { echo "skip $d (removable)"; continue; }
      size="$(cat "$p/size" 2>/dev/null || echo 0)"
      # at least 16 GiB (512-byte sectors)
      [ "$size" -ge 33554432 ] 2>/dev/null || { echo "skip $d (size $size)"; continue; }
      disk="$d"; break 2
    done
  done
fi
if [ -n "$disk" ]; then
  echo "target disk: /dev/$disk"
  casper-preseed /root partman-auto/disk "/dev/$disk"
else
  echo "no target disk found, the installer will ask"
fi

# Optional: stream the live session journal to the serial port (hb_serial_log=1) so installer
# problems can be read from the VM host without a screen.
if [ "$(getarg hb_serial_log)" = 1 ]; then
  mkdir -p /root/etc/systemd/system/multi-user.target.wants
  cat > /root/etc/systemd/system/hackbox-seriallog.service <<UNIT
[Unit]
Description=hackbox: copy journal to ttyS0
[Service]
ExecStart=/bin/sh -c 'journalctl -f -o short-precise > /dev/ttyS0'
Restart=always
UNIT
  ln -sf /etc/systemd/system/hackbox-seriallog.service /root/etc/systemd/system/multi-user.target.wants/hackbox-seriallog.service
fi

# Headless USB installer (hb_mode=headless): network, ssh and progress reporting in the live
# session. The helper lives on the stick (usb/headless/early-live.sh).
if [ "$(getarg hb_mode)" = headless ] && [ -f /root/cdrom/hackbox/headless/early-live.sh ]; then
  . /root/cdrom/hackbox/headless/early-live.sh
fi

mkdir -p /root/usr/local/sbin /root/usr/local/share/hackbox
cp /hackbox-late.sh /root/usr/local/sbin/hackbox-late.sh
chmod 755 /root/usr/local/sbin/hackbox-late.sh
[ -f /hackbox-authorized_keys ] && cp /hackbox-authorized_keys /root/usr/local/share/hackbox/authorized_keys
echo "$disk" > /root/usr/local/share/hackbox/target-disk
exit 0
