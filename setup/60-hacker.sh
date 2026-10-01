#!/usr/bin/env bash
# The attendee account and how the box logs into it (SPEC 1, 3.1).
#   - `hacker`, UID/GID 2000, locked password, no sudo or admin groups.
#   - LightDM autologins it into Cinnamon; no guest, no user list, no user
#     switching; the admin account is hidden from the greeter (organisers
#     come in over SSH).
#   - /etc/hackbox/conf.d/10-session.conf and the home skeleton
#     /etc/hackbox/skel, assembled from files/skel (every owner's part).
# Takes effect at the next boot. Never restarts LightDM: bootstrap may be
# running inside the admin's own desktop session.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
HACKER=hacker
HID=2000
ADMIN=$USER

if [ "$ADMIN" = "$HACKER" ] || [ "$(id -u)" -eq 0 ]; then
  echo "run as the admin user, not root or $HACKER" >&2
  exit 1
fi

# --- account -------------------------------------------------------------
if ! getent group "$HACKER" >/dev/null; then
  if getent group "$HID" >/dev/null; then
    echo "gid $HID is taken by $(getent group "$HID" | cut -d: -f1)" >&2; exit 1
  fi
  sudo groupadd -g "$HID" "$HACKER"
fi
if ! getent passwd "$HACKER" >/dev/null; then
  if getent passwd "$HID" >/dev/null; then
    echo "uid $HID is taken by $(getent passwd "$HID" | cut -d: -f1)" >&2; exit 1
  fi
  # -M: no home here. /home/hacker is a mount point for the disposable
  # image, set up by 70-session.sh.
  sudo useradd -u "$HID" -g "$HID" -M -d "/home/$HACKER" -s /bin/bash \
    -c "Hack Box attendee" "$HACKER"
fi
if [ "$(id -u "$HACKER")" != "$HID" ] || [ "$(id -g "$HACKER")" != "$HID" ]; then
  echo "$HACKER exists with uid/gid $(id -u "$HACKER")/$(id -g "$HACKER"), expected $HID/$HID" >&2
  exit 1
fi
# usermod refuses some changes while the user is logged in, so only when needed.
if [ "$(getent passwd "$HACKER" | cut -d: -f6,7)" != "/home/$HACKER:/bin/bash" ]; then
  sudo usermod -s /bin/bash -d "/home/$HACKER" "$HACKER"
fi
sudo usermod -L "$HACKER"            # '!' in /etc/shadow: no password login anywhere

# Only its own group. Seat devices (audio, video, input) come from logind.
for g in $(id -nG "$HACKER"); do
  [ "$g" = "$HACKER" ] || sudo gpasswd -d "$HACKER" "$g" >/dev/null
done
sudo rm -f "/etc/sudoers.d/$HACKER"

# --- config and skeleton -------------------------------------------------
sudo install -d -m 0755 -o root -g root /etc/hackbox /etc/hackbox/conf.d /etc/hackbox/reset.d
sudo install -m 0644 -o root -g root "$HERE/files/conf/10-session.conf" /etc/hackbox/conf.d/10-session.conf

# Build the new skeleton next to the live one and swap, so a reset running
# at the same moment copies either the old or the new tree, never half.
sudo rm -rf /etc/hackbox/skel.new /etc/hackbox/skel.old
sudo install -d -m 0755 -o root -g root /etc/hackbox/skel.new
sudo cp -a "$HERE/files/skel/." /etc/hackbox/skel.new/
# Tray apps and dialogs Mint autostarts from /etc/xdg/autostart (welcome
# screen, update manager, reports, bluetooth, print applet). 20-services.sh
# hides them for the admin only; the attendee gets the same per-user
# overrides. A full copy plus Hidden=true: cinnamon-session rejects a stub.
sudo install -d -m 0755 /etc/hackbox/skel.new/.config/autostart
for app in blueman mintupdate mintreport mintwelcome print-applet; do
  if [ -f "/etc/xdg/autostart/$app.desktop" ]; then
    { cat "/etc/xdg/autostart/$app.desktop"; echo "Hidden=true"; } |
      sudo tee "/etc/hackbox/skel.new/.config/autostart/$app.desktop" >/dev/null
  fi
done
sudo chown -R root:root /etc/hackbox/skel.new
sudo chmod -R u+rwX,go+rX,go-w /etc/hackbox/skel.new
if [ -d /etc/hackbox/skel ]; then sudo mv /etc/hackbox/skel /etc/hackbox/skel.old; fi
sudo mv /etc/hackbox/skel.new /etc/hackbox/skel
sudo rm -rf /etc/hackbox/skel.old

# --- LightDM -------------------------------------------------------------
sudo install -d -m 0755 /etc/lightdm/lightdm.conf.d
sudo install -m 0644 "$HERE/files/session/lightdm/90-hackbox.conf" /etc/lightdm/lightdm.conf.d/90-hackbox.conf

# /etc/lightdm/lightdm.conf is read after conf.d and wins. Mint's Login
# Window tool writes it; comment out anything there that fights ours.
KEYS='autologin-user|autologin-user-timeout|autologin-session|autologin-guest|user-session|allow-guest|greeter-allow-guest|greeter-hide-users|allow-user-switching|session-cleanup-script'
if [ -f /etc/lightdm/lightdm.conf ] && sudo grep -qE "^[[:space:]]*($KEYS)[[:space:]]*=" /etc/lightdm/lightdm.conf; then
  [ -f /etc/lightdm/lightdm.conf.hackbox.bak ] || sudo cp -a /etc/lightdm/lightdm.conf /etc/lightdm/lightdm.conf.hackbox.bak
  sudo sed -i -E "s/^[[:space:]]*($KEYS)[[:space:]]*=/#hackbox# &/" /etc/lightdm/lightdm.conf
  echo "commented out conflicting keys in /etc/lightdm/lightdm.conf (backup: lightdm.conf.hackbox.bak)"
fi

# Hide the admin: AccountsService SystemAccount keeps it out of the greeter
# and Cinnamon's user lists. Organisers log in over SSH.
sudo install -d -m 0755 /var/lib/AccountsService/users
acct=/var/lib/AccountsService/users/$ADMIN
if ! sudo test -f "$acct"; then
  printf '[User]\nSystemAccount=true\n' | sudo tee "$acct" >/dev/null
elif sudo grep -q '^SystemAccount=' "$acct"; then
  sudo sed -i 's/^SystemAccount=.*/SystemAccount=true/' "$acct"
elif sudo grep -q '^\[User\]' "$acct"; then
  sudo sed -i '/^\[User\]/a SystemAccount=true' "$acct"
else
  printf '[User]\nSystemAccount=true\n' | sudo tee -a "$acct" >/dev/null
fi
printf '[User]\nSession=cinnamon\nXSession=cinnamon\nSystemAccount=false\n' |
  sudo tee "/var/lib/AccountsService/users/$HACKER" >/dev/null

# slick-greeter's own hidden list, only if nobody configured it yet.
SG=/etc/lightdm/slick-greeter.conf
if [ ! -f "$SG" ]; then
  printf '[Greeter]\nhidden-users=%s\n' "$ADMIN" | sudo tee "$SG" >/dev/null
elif ! grep -q '^hidden-users=' "$SG"; then
  if grep -q '^\[Greeter\]' "$SG"; then
    sudo sed -i "/^\[Greeter\]/a hidden-users=$ADMIN" "$SG"
  else
    printf '[Greeter]\nhidden-users=%s\n' "$ADMIN" | sudo tee -a "$SG" >/dev/null
  fi
fi

echo "attendee account: $(id "$HACKER")"
echo "autologin as $HACKER takes effect at the next boot"
