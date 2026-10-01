#!/usr/bin/env bash
# Install Tailscale and bring it up. `tailscale up` prints a login URL the
# first time. Open it in the box's browser to add the machine to the tailnet.
#
# Kiosk boxes: set TS_TAG (for example TS_TAG=tag:hackbox, in the environment or in
# /etc/hackbox/conf.d/) to join as a tagged device that does not accept subnet routes.
# The tailnet policy then decides the rest: staff may reach the tag on tcp:22, the tag
# may reach nothing (see files/lockdown/tailscale-acl.hujson). Unset: unchanged behaviour.
# An already joined box is not re-tagged automatically (that re-authenticates it and can
# drop the ssh session running this); the command to do it is printed instead, or set
# TS_RETAG=1 to run it.
#
# Unattended join (the headless USB installer): set TS_AUTHKEY to a Tailscale auth key, or
# better to "file:/path/to/key" (root-readable file) so the key is not on a command line.
# `tailscale up` then joins without a browser login. Unset: unchanged behaviour (login URL).
set -euo pipefail

if [ -z "${TS_TAG:-}" ] && [ -d /etc/hackbox/conf.d ]; then
  TS_TAG=$(for f in /etc/hackbox/conf.d/*.conf; do [ -r "$f" ] && . "$f"; done; echo "${TS_TAG:-}")
fi

if ! command -v tailscale >/dev/null; then
  curl -fsSL https://tailscale.com/install.sh | sh
fi

args=(--hostname "$(hostname)")
if [ -n "${TS_TAG:-}" ]; then
  case "$TS_TAG" in tag:*) ;; *) TS_TAG="tag:$TS_TAG" ;; esac
  args+=(--advertise-tags="$TS_TAG" --accept-routes=false)
fi

if ! tailscale status >/dev/null 2>&1; then
  if [ -n "${TS_AUTHKEY:-}" ]; then
    sudo tailscale up "${args[@]}" --auth-key="$TS_AUTHKEY"
  else
    sudo tailscale up "${args[@]}"
  fi
elif [ -n "${TS_TAG:-}" ]; then
  tags=$(tailscale status --json 2>/dev/null | python3 -c 'import json,sys; print(" ".join(json.load(sys.stdin).get("Self",{}).get("Tags") or []))' || true)
  case " $tags " in
    *" $TS_TAG "*) ;;
    *)
      if [ "${TS_RETAG:-0}" = 1 ]; then
        sudo tailscale up --reset "${args[@]}"
      else
        echo "warn: joined without $TS_TAG. To re-tag: sudo tailscale up --reset ${args[*]}" >&2
      fi
      ;;
  esac
fi

echo "tailscale ip: $(tailscale ip -4)"
