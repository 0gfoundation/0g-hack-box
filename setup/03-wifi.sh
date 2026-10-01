#!/usr/bin/env bash
# Wi-Fi: one system-wide NetworkManager profile, "hackbox-wifi", from /etc/hackbox/wifi.conf.
# Runs before 05-tailscale.sh so a box without a cable is online for the rest of setup.
# Wired boxes: no conf file, nothing happens, exit 0.
#
# /etc/hackbox/wifi.conf (root:root 0600, shell syntax, written by `hackbox wifi set` or by the
# headless USB installer):
#   WIFI_SSID=...              network name (required)
#   WIFI_PASSWORD=...          passphrase, or the account password on an enterprise network
#   WIFI_USERNAME=...          set: WPA2-Enterprise (802.1X, PEAP + MSCHAPv2). unset: WPA2/WPA3 personal
#   WIFI_HIDDEN=1              the network does not broadcast its name
#   WIFI_COUNTRY=SG            regulatory domain (two letters), for the right channels and power
#   WIFI_KEY_MGMT=auto         personal only: auto | wpa-psk | sae. auto uses wpa-psk (WPA2, and
#                              WPA2/WPA3 transition networks) unless a scan shows the network is
#                              WPA3 only, then sae.
#   WIFI_EAP_DOMAIN=...        enterprise only, optional: the RADIUS server certificate must match
#                              this domain suffix (strongly recommended with the system CAs).
#   WIFI_ANON_IDENTITY=...     enterprise only, optional: outer identity.
#   WIFI_EAP_NO_CA_CHECK=1     enterprise only, OFF by default. Skips validation of the RADIUS
#                              server certificate, for venue networks whose server uses a private
#                              CA. RISK: anyone nearby can run a fake access point with the same
#                              name and collect the username and the MSCHAPv2 exchange, which can
#                              be cracked offline to recover the password. Use it only with a
#                              throwaway account the venue gave for these boxes.
#
# The profile: /etc/NetworkManager/system-connections/hackbox-wifi.nmconnection, root 0600,
# written directly as a keyfile so the password never appears on a command line or in output.
# The secret is a system secret stored in that file (psk-flags / password-flags 0), which
# only root can read. permissions= is empty, so the connection exists for every user and
# survives the attendee home wipe; changing it or reading its secrets needs polkit's
# org.freedesktop.NetworkManager.settings.modify.system, which files/session/polkit denies to
# the attendee. Route metric 600 (NetworkManager's Wi-Fi default, set explicitly): a wired
# connection (metric 100) keeps the default route when both are up.
#
# Runs as the admin user (uses sudo) or as root. HB_WIFI_OFFLINE=1: only write the file (for a
# chroot without a running NetworkManager). Idempotent: an unchanged profile is left alone.
# Paths can be redirected for an offline target or a build-time check: HACKBOX_WIFI_CONF,
# HACKBOX_NM_DIR, HACKBOX_MODPROBE_DIR; HB_WIFI_NOSUDO=1 then runs without sudo.
set -euo pipefail

CONF=${HACKBOX_WIFI_CONF:-/etc/hackbox/wifi.conf}
NAME=hackbox-wifi
DIR=${HACKBOX_NM_DIR:-/etc/NetworkManager/system-connections}
FILE="$DIR/$NAME.nmconnection"
MODPROBE_DIR=${HACKBOX_MODPROBE_DIR:-/etc/modprobe.d}
if [ "$(id -u)" -eq 0 ] || [ "${HB_WIFI_NOSUDO:-0}" = 1 ]; then SUDO=(); else SUDO=(sudo); fi
# root:root ownership (skipped only in a no-sudo build-time check)
own() { if [ "$(id -u)" -eq 0 ] || [ ${#SUDO[@]} -gt 0 ]; then "${SUDO[@]}" chown root:root "$@"; fi; }

if ! "${SUDO[@]}" test -f "$CONF"; then
  echo "03-wifi: no $CONF, nothing to do (wired box)"
  exit 0
fi
own "$CONF"
"${SUDO[@]}" chmod 0600 "$CONF"

# Non-secret settings, read as root without executing the file.
conf_get() {
  "${SUDO[@]}" python3 - "$CONF" "$1" <<'PY'
import shlex, sys
path, key = sys.argv[1:3]
val = ""
for line in open(path, encoding="utf-8"):
    try:
        toks = shlex.split(line, comments=True)
    except ValueError:
        continue
    for t in toks:
        if "=" in t:
            k, v = t.split("=", 1)
            if k == key:
                val = v
print(val)
PY
}
ssid=$(conf_get WIFI_SSID)
username=$(conf_get WIFI_USERNAME)
country=$(conf_get WIFI_COUNTRY)
key_mgmt=$(conf_get WIFI_KEY_MGMT)
if [ -z "$ssid" ]; then
  echo "03-wifi: WIFI_SSID is empty in $CONF" >&2
  exit 1
fi

have_nm=0
if [ "${HB_WIFI_OFFLINE:-0}" != 1 ] && command -v nmcli >/dev/null && nmcli -t general status >/dev/null 2>&1; then
  have_nm=1
fi

# Regulatory domain: now (iw, if present) and at every boot (cfg80211 option).
if [ -n "$country" ]; then
  case "$country" in [A-Z][A-Z]) ;; *) echo "03-wifi: WIFI_COUNTRY must be two capital letters" >&2; exit 1 ;; esac
  want="options cfg80211 ieee80211_regdom=$country"
  if [ "$("${SUDO[@]}" cat "$MODPROBE_DIR/hackbox-wifi.conf" 2>/dev/null || true)" != "$want" ]; then
    "${SUDO[@]}" mkdir -p "$MODPROBE_DIR"
    echo "$want" | "${SUDO[@]}" tee "$MODPROBE_DIR/hackbox-wifi.conf" >/dev/null
  fi
  if [ $have_nm = 1 ] && command -v iw >/dev/null; then "${SUDO[@]}" iw reg set "$country" 2>/dev/null || true; fi
fi

# Personal network, key management "auto": ask a scan whether the network is WPA3 only.
scan_security=""
if [ -z "$username" ] && [ "${key_mgmt:-auto}" = auto ] && [ $have_nm = 1 ] &&
   nmcli -t -f TYPE device 2>/dev/null | grep -qx wifi; then
  scan_security=$(nmcli -t -f SSID,SECURITY device wifi list --rescan auto 2>/dev/null |
    awk -v s="$ssid" -F: '{ n=$1; gsub(/\\:/, ":", n) } n==s { print $NF; exit }' || true)
fi

# Ubuntu 24.04's network-manager package migrates keyfiles from system-connections to
# /etc/netplan/90-NM-<uuid>.yaml (root 0600) on every package upgrade ("Netplan Everywhere"),
# so the profile may live in either place. What was applied is recorded in STAMP (content hash
# and UUID, root 0600); the profile is rewritten only when the wanted content changed or the
# profile is missing or duplicated, and then every connection named hackbox-wifi is replaced.
STAMP="$(dirname "$CONF")/wifi.applied"
existing=""
if [ $have_nm = 1 ]; then
  existing=$(nmcli -t -f UUID,NAME connection show 2>/dev/null | awk -F: -v n="$NAME" '$2==n {print $1}')
fi
stamp_sha="" stamp_uuid=""
read -r stamp_sha stamp_uuid < <("${SUDO[@]}" cat "$STAMP" 2>/dev/null || true) || true

"${SUDO[@]}" install -d -m 0700 "$DIR"
own "$DIR"
tmp=$("${SUDO[@]}" mktemp "$DIR/.$NAME.XXXXXX")
# Everything secret stays inside this root python process: read from the conf, written to
# the keyfile, never printed and never on a command line.
if ! res=$("${SUDO[@]}" env SCAN_SECURITY="$scan_security" KEEP_UUID="${stamp_uuid:-$(echo "$existing" | head -n 1)}" \
    python3 - "$CONF" "$FILE" "$tmp" <<'PY'
import hashlib, os, re, shlex, sys, uuid
conf, final, tmp = sys.argv[1:4]
c = {}
for line in open(conf, encoding="utf-8"):
    try:
        toks = shlex.split(line, comments=True)
    except ValueError:
        sys.exit("03-wifi: cannot parse a line in the conf (check the quoting)")
    for t in toks:
        if "=" in t:
            k, v = t.split("=", 1)
            c[k] = v

def fail(msg):
    sys.exit("03-wifi: " + msg)

ssid = c.get("WIFI_SSID", "")
pw = c.get("WIFI_PASSWORD", "")
user = c.get("WIFI_USERNAME", "")
hidden = c.get("WIFI_HIDDEN", "0") in ("1", "yes", "true")
if not 1 <= len(ssid.encode()) <= 32:
    fail("WIFI_SSID must be 1 to 32 bytes")
if "\n" in pw or "\r" in pw:
    fail("WIFI_PASSWORD must be one line")

def esc(v):
    # GKeyFile string escaping (what NetworkManager's keyfile reader expects).
    v = v.replace("\\", "\\\\").replace("\t", "\\t")
    if v.startswith(" "):
        v = "\\s" + v[1:]
    return v

def ssid_value(s):
    b = s.encode()
    # Plain text when that is unambiguous, otherwise the byte list form the keyfile reader
    # also accepts (covers ';', leading/trailing spaces, non-printable bytes).
    if re.fullmatch(r"[A-Za-z0-9_\-.@+!#$%&()*,/:=?\[\]^{}|~' ]+", s) and not s.startswith(" ") \
            and not s.endswith(" ") and not re.fullmatch(r"[0-9;]+", s):
        return esc(s)
    return ";".join(str(x) for x in b) + ";"

# Keep the UUID of the applied profile (from the stamp or NetworkManager), or of a keyfile.
cid = os.environ.get("KEEP_UUID", "")
if not re.fullmatch(r"[0-9a-f-]{36}", cid) and os.path.exists(final):
    for line in open(final, encoding="utf-8", errors="replace"):
        if line.startswith("uuid="):
            cid = line.strip().split("=", 1)[1]
if not re.fullmatch(r"[0-9a-f-]{36}", cid):
    cid = str(uuid.uuid4())

out = ["[connection]", "id=hackbox-wifi", "uuid=" + cid, "type=wifi",
       "autoconnect=true", "autoconnect-priority=0", "autoconnect-retries=0", "permissions=", "",
       "[wifi]", "mode=infrastructure", "ssid=" + ssid_value(ssid)]
if hidden:
    out.append("hidden=true")
out.append("")
if user:
    if not pw:
        fail("WIFI_PASSWORD is required with WIFI_USERNAME")
    out += ["[wifi-security]", "key-mgmt=wpa-eap", "", "[802-1x]", "eap=peap;",
            "identity=" + esc(user), "password=" + esc(pw), "password-flags=0",
            "phase2-auth=mschapv2"]
    anon = c.get("WIFI_ANON_IDENTITY", "")
    if anon:
        out.append("anonymous-identity=" + esc(anon))
    if c.get("WIFI_EAP_NO_CA_CHECK", "0") in ("1", "yes", "true"):
        # Deliberately no CA: the server certificate is not checked (see the header).
        pass
    else:
        out.append("system-ca-certs=true")
        dom = c.get("WIFI_EAP_DOMAIN", "")
        if dom:
            out.append("domain-suffix-match=" + esc(dom))
    out.append("")
elif pw:
    km = c.get("WIFI_KEY_MGMT", "auto") or "auto"
    if km not in ("auto", "wpa-psk", "sae"):
        fail("WIFI_KEY_MGMT must be auto, wpa-psk or sae")
    if km == "auto":
        sec = os.environ.get("SCAN_SECURITY", "")
        km = "sae" if ("WPA3" in sec and "WPA2" not in sec and "WPA1" not in sec) else "wpa-psk"
    if not (8 <= len(pw) <= 63 or re.fullmatch(r"[0-9a-fA-F]{64}", pw)):
        fail("a WPA passphrase is 8 to 63 characters (or 64 hex digits)")
    out += ["[wifi-security]", "key-mgmt=" + km, "psk=" + esc(pw), "psk-flags=0", ""]
else:
    fail("WIFI_PASSWORD is empty (open networks are not supported: they need no profile)")
out += ["[ipv4]", "method=auto", "route-metric=600", "",
        "[ipv6]", "addr-gen-mode=default", "method=auto", "route-metric=600", "", "[proxy]", ""]
new = "\n".join(out)
with open(tmp, "w", encoding="utf-8") as f:
    f.write(new)
os.chmod(tmp, 0o600)
print(hashlib.sha256(new.encode()).hexdigest(), cid)
PY
); then
  "${SUDO[@]}" rm -f "$tmp"
  exit 1
fi
read -r sha uuid <<< "$res"

present=0
if [ $have_nm = 1 ]; then
  [ "$existing" = "$uuid" ] && present=1          # exactly one profile, the applied one
else
  "${SUDO[@]}" test -f "$FILE" && present=1
  "${SUDO[@]}" test -f "$DIR/../../netplan/90-NM-$uuid.yaml" && present=1
fi
kind=personal; [ -n "$username" ] && kind=enterprise
if [ "$sha" = "$stamp_sha" ] && [ $present = 1 ]; then
  "${SUDO[@]}" rm -f "$tmp"
  echo "03-wifi: profile $NAME for '$ssid' ($kind) unchanged"
else
  if [ $have_nm = 1 ]; then
    for u in $existing; do "${SUDO[@]}" nmcli connection delete uuid "$u" >/dev/null 2>&1 || true; done
  fi
  "${SUDO[@]}" mv -f "$tmp" "$FILE"
  own "$FILE"
  "${SUDO[@]}" chmod 0600 "$FILE"
  echo "$sha $uuid" | "${SUDO[@]}" tee "$STAMP.tmp" >/dev/null
  "${SUDO[@]}" chmod 0600 "$STAMP.tmp"; own "$STAMP.tmp"; "${SUDO[@]}" mv -f "$STAMP.tmp" "$STAMP"
  if [ $have_nm = 1 ]; then "${SUDO[@]}" nmcli connection load "$FILE" >/dev/null; fi
  echo "03-wifi: profile $NAME for '$ssid' ($kind) written to $FILE"
fi

if [ $have_nm = 0 ]; then
  echo "03-wifi: NetworkManager not running here, it loads the profile at its next start"
  exit 0
fi
if ! nmcli -t -f TYPE device 2>/dev/null | grep -qx wifi; then
  echo "03-wifi: no Wi-Fi device found, the profile waits for one"
  exit 0
fi
# Already on this network: nothing to do. Otherwise try once now (autoconnect retries later).
active=$(nmcli -t -f NAME connection show --active 2>/dev/null | grep -x "$NAME" || true)
if [ -z "$active" ]; then
  "${SUDO[@]}" nmcli radio wifi on 2>/dev/null || true
  if "${SUDO[@]}" nmcli --wait 45 connection up "$NAME" >/dev/null 2>&1; then
    echo "03-wifi: connected to '$ssid'"
  else
    echo "03-wifi: warn: could not connect to '$ssid' now; NetworkManager keeps trying. Check with: hackbox wifi status" >&2
  fi
else
  echo "03-wifi: already connected to '$ssid'"
fi
