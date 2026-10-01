#!/usr/bin/env bash
# The session engine (SPEC 3): disposable loop-mounted home, root-owned
# timer, reset, work archive, overlay, polkit rule, admin CLI.
#   helpers    files/session/sbin/*        -> /usr/local/sbin/
#   overlay    files/session/overlay/*     -> /usr/local/lib/hackbox/
#   units      files/session/units/*       -> /etc/systemd/system/
#   LightDM    session-cleanup hook (91-hackbox-session.conf) and a drop-in
#              so LightDM never starts before the home is built
#   polkit     files/session/polkit/*      -> /etc/polkit-1/rules.d/
#   tmpfs      /tmp, /var/tmp, /dev/shm capped in /etc/fstab (next boot)
#   CLI        /usr/local/bin/hackbox -> bin/hackbox in this repo
# Safe mid-session: files are replaced with `install` (new inode, so running
# scripts keep their old copy), nothing that holds the attendee's session is
# restarted, and the home is only built here when nobody is using it.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
S="$HERE/files/session"

if ! getent passwd hacker >/dev/null; then
  echo "user hacker missing, run setup/60-hacker.sh first" >&2
  exit 1
fi
HACKER_USER=hacker HACKER_UID=2000 TMP_SIZE=2G VAR_TMP_SIZE=1G SHM_SIZE=2G
for f in /etc/hackbox/conf.d/*.conf; do [ -r "$f" ] && . "$f"; done
H=/home/$HACKER_USER

sudo apt-get install -y -qq e2fsprogs util-linux python3-gi gir1.2-gtk-3.0 >/dev/null

# --- programs ------------------------------------------------------------
for f in "$S"/sbin/*; do
  sudo install -m 0755 -o root -g root "$f" "/usr/local/sbin/$(basename "$f")"
done
sudo install -d -m 0755 /usr/local/lib/hackbox
sudo install -m 0755 -o root -g root "$S/overlay/hackbox-overlay" /usr/local/lib/hackbox/hackbox-overlay
sudo ln -sfn "$HERE/bin/hackbox" /usr/local/bin/hackbox

# --- state and runtime dirs ----------------------------------------------
sudo install -d -m 0755 -o root -g root /var/lib/hackbox
sudo install -d -m 0700 -o root -g root /var/lib/hackbox/archive
sudo install -d -m 0755 /etc/tmpfiles.d /etc/systemd/system
sudo install -m 0644 "$S/units/tmpfiles-hackbox.conf" /etc/tmpfiles.d/hackbox.conf
sudo systemd-tmpfiles --create /etc/tmpfiles.d/hackbox.conf

# --- systemd units -------------------------------------------------------
for u in hackbox-home.service hackbox-reset.service hackbox-session.service \
         hackbox-guard.service hackbox-archive-prune.service hackbox-archive-prune.timer; do
  sudo install -m 0644 "$S/units/$u" "/etc/systemd/system/$u"
done
sudo install -d -m 0755 /etc/systemd/system/lightdm.service.d
sudo install -m 0644 "$S/units/lightdm-hackbox.conf" /etc/systemd/system/lightdm.service.d/hackbox.conf

# --- LightDM cleanup hook and polkit ---------------------------------------
sudo install -d -m 0755 /etc/lightdm/lightdm.conf.d
sudo install -m 0644 "$S/lightdm/91-hackbox-session.conf" /etc/lightdm/lightdm.conf.d/91-hackbox-session.conf
sudo install -d -m 0755 /etc/polkit-1/rules.d
sudo install -m 0644 "$S/polkit/10-hackbox.rules" /etc/polkit-1/rules.d/10-hackbox.rules

# --- tmpfs caps (fstab, active from the next boot) --------------------------
# Mounting a fresh tmpfs over a live /tmp would hide the X server's socket,
# so /tmp and /var/tmp only change at boot. /dev/shm is resized in place.
BEGIN='# BEGIN hack-box tmpfs, managed by setup 70-session'
END='# END hack-box tmpfs'
block="$BEGIN"
for spec in "/tmp:$TMP_SIZE" "/var/tmp:$VAR_TMP_SIZE" "/dev/shm:$SHM_SIZE"; do
  mnt=${spec%%:*}; size=${spec#*:}
  if sed "/^$BEGIN\$/,/^$END\$/d" /etc/fstab | awk '$1 !~ /^#/ {print $2}' | grep -qx "$mnt"; then
    echo "warn: /etc/fstab already has an entry for $mnt, leaving it"
    continue
  fi
  block+=$'\n'"tmpfs $mnt tmpfs mode=1777,nosuid,nodev,size=$size 0 0"
done
block+=$'\n'"$END"
new=$(mktemp)
sed "/^$BEGIN\$/,/^$END\$/d" /etc/fstab > "$new"
printf '%s\n' "$block" >> "$new"
if ! cmp -s "$new" /etc/fstab; then
  if sudo findmnt --verify --tab-file /etc/fstab >/dev/null 2>&1 &&
     ! sudo findmnt --verify --tab-file "$new" >/dev/null 2>&1; then
    echo "new /etc/fstab does not verify, leaving the old one:" >&2
    sudo findmnt --verify --tab-file "$new" >&2 || true
    rm -f "$new"; exit 1
  fi
  [ -f /etc/fstab.hackbox.bak ] || sudo cp -a /etc/fstab /etc/fstab.hackbox.bak
  sudo install -m 0644 -o root -g root "$new" /etc/fstab
  echo "tmpfs caps written to /etc/fstab (active after reboot)"
fi
rm -f "$new"
if grep -q "tmpfs /dev/shm tmpfs .*size=$SHM_SIZE" /etc/fstab; then
  sudo mount -o remount,size="$SHM_SIZE" /dev/shm 2>/dev/null ||
    echo "warn: /dev/shm is using more than $SHM_SIZE, resize happens at reboot"
fi

sudo systemctl daemon-reload
sudo systemctl enable hackbox-home.service hackbox-guard.service >/dev/null
sudo systemctl enable --now hackbox-archive-prune.timer >/dev/null

# --- the home ------------------------------------------------------------
if mountpoint -q "$H"; then
  echo "attendee home is mounted, leaving the live session alone"
elif pgrep -u "$HACKER_UID" >/dev/null; then
  echo "warn: $HACKER_USER has processes but no mounted home; reboot to fix" >&2
else
  # First run: turn the plain directory into a bare, immutable mount point,
  # then build the first image now (at every boot hackbox-home.service does).
  if [ -d "$H" ] && [ ! -L "$H" ]; then
    sudo chattr -i "$H" 2>/dev/null || true
    sudo find "$H" -mindepth 1 -delete
  fi
  sudo install -d -m 0755 -o root -g root "$H"
  sudo chattr +i "$H"
  echo "building the attendee home image..."
  sudo /usr/local/sbin/hackbox-reset --boot
fi

# The guard only watches and relaunches, so (re)starting it is always safe.
sudo systemctl restart hackbox-guard.service

if ! python3 -c 'import gi; gi.require_version("Gtk", "3.0"); from gi.repository import Gtk' 2>/dev/null; then
  echo "warn: python3 GTK bindings missing, the overlay will not run" >&2
fi
echo "home: $(findmnt -n -o SOURCE,SIZE "$H" 2>/dev/null || echo 'not mounted')"
echo "state: $(cat /run/hackbox/state 2>/dev/null || echo unknown)"
echo "session engine installed; reboot to switch on autologin and the tmpfs caps"
