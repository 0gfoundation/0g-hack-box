#!/bin/bash
# Remaster the Linux Mint 22.3 Cinnamon ISO into a zero-touch installer for the hackbox machines.
#   make-usb-iso.sh [source.iso] [output.iso]
#   make-usb-iso.sh --headless --config <headless.conf> [--repo <dir>] [--keys <authorized_keys>] [source.iso] [output.iso]
#
# Default mode (unchanged): adds /casper/initrd-hackbox.lz (stock initrd + preseed segment, see
# ../build-initrd.sh) and GRUB entries (UEFI) at the top of the menu:
#   Automated install (ERASES DISK)                  default, starts after 10 s; hostname hackbox-<MAC suffix>
#   Automated install as hackbox1/2/3 (ERASES DISK)
#   ...the original Mint entries (live session, OEM install) follow unchanged.
# The installed box: user hackadmin/hackadmin, Asia/Singapore, en_US.UTF-8, US keyboard,
# whole-disk layout on the first internal NVMe/SATA disk. It powers off when done
# (so it cannot loop back into the installer); remove the stick and power on.
# No lab ssh keys, no passwordless sudo: on real hardware the repo's 40-ssh.sh does that.
#
# --headless: a stick for a box with no screen, keyboard or mouse (see README.md in this
# directory). The config (headless.conf.example) gives hostname, admin user and password,
# Wi-Fi, ssh keys, optional Tailscale key and report URL. The stick installs, brings up the
# network and an ssh server in the installer, reboots into the installed system (boot order
# set, and the default entry refuses to reinstall a disk that has a completed hack-box
# install), and provisions the box on its first boot from a copy of the repo (--repo, the
# 0g-hack-box tree) carried on the stick. Output default: hackbox-mint223-headless.iso, mode
# 0600: it contains the Wi-Fi password, the Tailscale key and the admin password hash.
#
# Write to a stick:  sudo dd if=hackbox-mint223-auto.iso of=/dev/sdX bs=4M status=progress conv=fsync
# (macOS: see README.md)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
LAB="${HACKBOX_LAB:-$HOME/hackbox-lab}"
HEADLESS=0 CONFIG="" REPO="" KEYS=""
pos=()
while [ $# -gt 0 ]; do
  case "$1" in
    --headless) HEADLESS=1; shift ;;
    --config) CONFIG="${2:?}"; shift 2 ;;
    --repo) REPO="${2:?}"; shift 2 ;;
    --keys) KEYS="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) pos+=("$1"); shift ;;
  esac
done
SRC="${pos[0]:-$LAB/iso/linuxmint-22.3-cinnamon-64bit.iso}"
if [ $HEADLESS = 1 ]; then OUT="${pos[1]:-$LAB/iso/hackbox-mint223-headless.iso}"
else OUT="${pos[1]:-$LAB/iso/hackbox-mint223-auto.iso}"; fi
SEED="$(cd "$HERE/seed" && pwd -P)"
BUILD_INITRD="$HERE/build-initrd.sh"
TIMEOUT="${GRUB_TIMEOUT:-10}"

for t in xorriso cpio gzip python3; do command -v $t >/dev/null || { echo "missing $t" >&2; exit 1; }; done
[ -f "$SRC" ] || { echo "source ISO missing: $SRC" >&2; exit 1; }
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
chmod 700 "$W"

xorriso -osirrox on -indev "$SRC" \
  -extract /casper/initrd.lz "$W/initrd.lz" \
  -extract /boot/grub/grub.cfg "$W/grub.cfg.orig" \
  -extract /.disk/casper-uuid-generic "$W/casper-uuid" >/dev/null 2>&1
chmod u+w "$W"/*
UUID="$(tr -d '[:space:]' < "$W/casper-uuid")"

MAPS=()
if [ $HEADLESS = 1 ]; then
  [ -n "$CONFIG" ] && [ -f "$CONFIG" ] || { echo "--headless needs --config <file> (see headless.conf.example)" >&2; exit 1; }
  # The config is the owner's own shell file: read it in a subshell, keep only known keys.
  vars=$(bash -c 'set -a; . "$1"; set +a
    for v in HB_HOSTNAME ADMIN_USER ADMIN_FULLNAME ADMIN_PASSWORD ADMIN_PASSWORD_HASH WIFI_SSID WIFI_PASSWORD WIFI_USERNAME WIFI_HIDDEN WIFI_COUNTRY WIFI_KEY_MGMT WIFI_EAP_DOMAIN WIFI_ANON_IDENTITY WIFI_EAP_NO_CA_CHECK SSH_KEY_FILES TS_AUTHKEY TS_TAG REPORT_URL LAN_SSH BEEP REPO_DIR HB_EXTRA_CMDLINE; do
      printf "%s=%q\n" "$v" "${!v:-}"
    done' bash "$CONFIG")
  eval "$vars"
  REPO="${REPO:-$REPO_DIR}"
  [ -n "$REPO" ] && [ -f "$REPO/setup/03-wifi.sh" ] || { echo "--repo (or REPO_DIR) must point at the 0g-hack-box tree" >&2; exit 1; }
  ADMIN_USER="${ADMIN_USER:-hackadmin}"
  [[ "$ADMIN_USER" =~ ^[a-z][a-z0-9_-]{0,30}$ ]] || { echo "bad ADMIN_USER" >&2; exit 1; }
  [ "$ADMIN_USER" != hacker ] || { echo "ADMIN_USER cannot be the attendee account name" >&2; exit 1; }
  if [ -n "$HB_HOSTNAME" ] && ! [[ "${HB_HOSTNAME//\{mac4\}/abcd}" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
    echo "bad HB_HOSTNAME '$HB_HOSTNAME' (lowercase letters, digits, -, optional {mac4})" >&2; exit 1
  fi
  if [ -z "$ADMIN_PASSWORD_HASH" ]; then
    [ -n "$ADMIN_PASSWORD" ] || { echo "set ADMIN_PASSWORD or ADMIN_PASSWORD_HASH (no public default password on a headless box)" >&2; exit 1; }
    ADMIN_PASSWORD_HASH=$(printf '%s' "$ADMIN_PASSWORD" | openssl passwd -6 -stdin)
  fi
  unset ADMIN_PASSWORD
  case "$ADMIN_PASSWORD_HASH" in '$6$'*|'$y$'*|'$5$'*) ;; *) echo "ADMIN_PASSWORD_HASH is not a crypt hash (\$6\$..., \$y\$...)" >&2; exit 1 ;; esac

  # Seed copy: admin user name (the password there is a placeholder; the late step sets the hash).
  mkdir -p "$W/seed"; cp "$SEED"/* "$W/seed/"
  sed -i -e "s/^d-i passwd\/username string .*/d-i passwd\/username string $ADMIN_USER/" \
         -e "s/^d-i passwd\/user-fullname string .*/d-i passwd\/user-fullname string ${ADMIN_FULLNAME:-Hack Admin}/" "$W/seed/hackbox.seed"
  SEED="$W/seed"

  # /hackbox on the stick: repo tree, headless scripts, settings
  H="$W/hackbox"; mkdir -p "$H/headless" "$H/repo"
  (cd "$REPO" && tar --exclude=./.git --exclude=./results -cf - .) | (cd "$H/repo" && tar -xf -)
  cp "$HERE/headless/"* "$H/headless/"
  q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
  {
    echo "# hack-box headless settings (generated by make-usb-iso.sh). Contains the admin password hash."
    echo "HB_HOSTNAME=$(q "$HB_HOSTNAME")"
    echo "ADMIN_USER=$(q "$ADMIN_USER")"
    echo "ADMIN_PASSWORD_HASH=$(q "$ADMIN_PASSWORD_HASH")"
    echo "TS_TAG=$(q "$TS_TAG")"
    echo "REPORT_URL=$(q "$REPORT_URL")"
    echo "LAN_SSH=$(q "${LAN_SSH:-auto}")"
    echo "BEEP=$(q "${BEEP:-1}")"
  } > "$H/headless.env"
  if [ -n "$WIFI_SSID" ]; then
    {
      echo "WIFI_SSID=$(q "$WIFI_SSID")"
      echo "WIFI_PASSWORD=$(q "$WIFI_PASSWORD")"
      [ -n "$WIFI_USERNAME" ] && echo "WIFI_USERNAME=$(q "$WIFI_USERNAME")"
      [ "$WIFI_HIDDEN" = 1 ] && echo "WIFI_HIDDEN=1"
      [ -n "$WIFI_COUNTRY" ] && echo "WIFI_COUNTRY=$(q "$WIFI_COUNTRY")"
      [ -n "$WIFI_KEY_MGMT" ] && echo "WIFI_KEY_MGMT=$(q "$WIFI_KEY_MGMT")"
      [ -n "$WIFI_EAP_DOMAIN" ] && echo "WIFI_EAP_DOMAIN=$(q "$WIFI_EAP_DOMAIN")"
      [ -n "$WIFI_ANON_IDENTITY" ] && echo "WIFI_ANON_IDENTITY=$(q "$WIFI_ANON_IDENTITY")"
      [ "$WIFI_EAP_NO_CA_CHECK" = 1 ] && echo "WIFI_EAP_NO_CA_CHECK=1"
      true
    } > "$H/wifi.conf"
    # Same checks the box will run, so a bad value fails the build, not the install.
    mkdir -p "$W/nmcheck"
    HB_WIFI_OFFLINE=1 HB_WIFI_NOSUDO=1 HACKBOX_WIFI_CONF="$H/wifi.conf" HACKBOX_NM_DIR="$W/nmcheck/nm" \
      HACKBOX_MODPROBE_DIR="$W/nmcheck/modprobe" bash "$REPO/setup/03-wifi.sh" \
      || { echo "the Wi-Fi settings were rejected (see above)" >&2; exit 1; }
    rm -rf "$W/nmcheck" "$H/wifi.applied"
  fi
  [ -n "$TS_AUTHKEY" ] && printf '%s\n' "$TS_AUTHKEY" > "$H/ts-authkey"
  # ssh keys: --keys file, else SSH_KEY_FILES, else the repo's keys/*.pub plus the lab copy of jarvis's key
  if [ -n "$KEYS" ]; then cp "$KEYS" "$H/authorized_keys"
  else
    : > "$H/authorized_keys"
    for k in ${SSH_KEY_FILES:-"$REPO"/keys/*.pub "$LAB/keys/jarvis.pub"}; do [ -f "$k" ] && cat "$k" >> "$H/authorized_keys"; done
  fi
  grep -Eq '^(ssh-ed25519|ssh-rsa|ecdsa-sha2-[a-z0-9-]+|sk-[a-z0-9@.-]+) ' "$H/authorized_keys" || { echo "no ssh public key to authorise" >&2; exit 1; }
  echo "headless: admin $ADMIN_USER, host ${HB_HOSTNAME:-hackbox-{mac4\}}, keys $(grep -c . "$H/authorized_keys"), wifi $([ -n "$WIFI_SSID" ] && echo "'$WIFI_SSID'${WIFI_USERNAME:+ (enterprise)}" || echo none), tailscale key $([ -n "$TS_AUTHKEY" ] && echo yes || echo no), report ${REPORT_URL:-none}"
  # Readable for the installer; the repo keeps its own exec bits (bin/hackbox, tests/run.sh, ...).
  chmod -R u+rwX,go+rX,go-w "$H"
  chmod 0755 "$H/headless/"*.sh "$H/headless/hackbox-firstboot"
  MAPS+=(-map "$H" /hackbox)
fi

bash "$BUILD_INITRD" "$W/initrd.lz" "$W/initrd-hackbox.lz" "$SEED"

if [ $HEADLESS = 1 ]; then
  host_arg=""; live_host=mint
  if [ -n "$HB_HOSTNAME" ] && [[ "$HB_HOSTNAME" != *"{mac4}"* ]]; then host_arg=" hb_host=$HB_HOSTNAME"; live_host=$HB_HOSTNAME; fi
  COMMON="boot=casper uuid=$UUID username=mint hostname=$live_host iso-scan/filename=\${iso_path} automatic-ubiquity noprompt quiet splash debian-installer/locale=en_US.UTF-8 keyboard-configuration/layoutcode=us console-setup/ask_detect=false ubiquity/reboot=true ubiquity/poweroff=false hb_mode=headless$host_arg${HB_EXTRA_CMDLINE:+ $HB_EXTRA_CMDLINE}"
else
  COMMON="boot=casper uuid=$UUID username=mint hostname=mint iso-scan/filename=\${iso_path} automatic-ubiquity noprompt quiet splash debian-installer/locale=en_US.UTF-8 keyboard-configuration/layoutcode=us console-setup/ask_detect=false ubiquity/reboot=false ubiquity/poweroff=true hb_mode=usb"
fi
python3 - "$W/grub.cfg.orig" "$W/grub.cfg" "$COMMON" "$TIMEOUT" "$HEADLESS" <<'PY'
import sys
src, dst, common, timeout, headless = sys.argv[1:6]
orig = open(src).read()
def entry(title, extra="", guard=False):
    g = ""
    if guard:
        # A disk that already carries a completed hack-box install is booted, never reinstalled.
        g = ('\tsearch --no-floppy --file --set=hb_esp /EFI/hackbox/installed\n'
             '\tif [ -n "$hb_esp" ]; then\n'
             '\t\techo "hack-box is already installed on ($hb_esp): booting it, not reinstalling."\n'
             '\t\techo "To reinstall, choose the Force reinstall entry."\n'
             '\t\tsleep 3\n'
             '\t\tif [ -f ($hb_esp)/EFI/ubuntu/shimx64.efi ]; then chainloader ($hb_esp)/EFI/ubuntu/shimx64.efi; boot; fi\n'
             '\t\tif [ -f ($hb_esp)/EFI/BOOT/BOOTX64.EFI ]; then chainloader ($hb_esp)/EFI/BOOT/BOOTX64.EFI; boot; fi\n'
             '\t\tif [ -f ($hb_esp)/EFI/ubuntu/grub.cfg ]; then configfile ($hb_esp)/EFI/ubuntu/grub.cfg; fi\n'
             '\t\techo "could not start the installed system; stopping here (nothing was erased)"\n'
             '\t\tsleep 3600\n'
             '\t\treboot\n'
             '\tfi\n')
    return (f'menuentry "{title}" --class linuxmint {{\n'
            f'{g}'
            f'\tset gfxpayload=keep\n'
            f'\tlinux\t/casper/vmlinuz {common}{extra} --\n'
            f'\tinitrd\t/casper/initrd-hackbox.lz\n}}\n')
head = f"set default=0\nset timeout={timeout}\n\n"
if headless == "1":
    entries = entry("Headless install (ERASES DISK, skipped if hack-box is installed)", guard=True)
    entries += entry("Force reinstall, headless (ERASES DISK)")
else:
    entries = entry("Automated install (ERASES DISK)")
    for n in (1, 2, 3):
        entries += entry(f"Automated install as hackbox{n} (ERASES DISK)", f" hb_host=hackbox{n}")
lines = orig.split("\n")
# insert after the font/colour preamble, before the first menuentry
idx = next(i for i, l in enumerate(lines) if l.startswith("menuentry"))
out = "\n".join(lines[:idx]) + "\n" + head + entries + "\n" + "\n".join(lines[idx:])
open(dst, "w").write(out)
PY

rm -f "$OUT"
# A headless ISO holds secrets: create it 0600.
( [ $HEADLESS = 1 ] && umask 077
  xorriso -indev "$SRC" -outdev "$OUT" -boot_image any replay \
    -map "$W/grub.cfg" /boot/grub/grub.cfg \
    -map "$W/initrd-hackbox.lz" /casper/initrd-hackbox.lz \
    -map "$SEED" /preseed/hackbox \
    "${MAPS[@]}" \
    2>&1 | grep -E "^xorriso : (UPDATE|NOTE|WARNING|FAILURE|SORRY)" | tail -5 || true )
[ -s "$OUT" ] || { echo "ISO build failed" >&2; exit 1; }
echo "built $OUT ($(du -h "$OUT" | cut -f1), mode $(stat -c %a "$OUT"))"
sha256sum "$OUT" | tee "$OUT.sha256"
