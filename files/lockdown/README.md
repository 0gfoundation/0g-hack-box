# Lockdown on a hack-box

Owner: AI+LOCKDOWN. Applied by `setup/80-lockdown.sh`; check with `hackbox lock status`
(one line per control, OK or MISSING, with evidence). Settings in
`/etc/hackbox/conf.d/30-lockdown.conf`, overrides in `39-lockdown-local.conf`.

| Control | Where | Takes effect |
|---|---|---|
| no sudo, su only for group sudo, cron/at deny, no linger | groups, `/etc/pam.d/su`, `/etc/cron.deny`, `/etc/at.deny` | now |
| sshd `DenyUsers hacker` | `setup/40-ssh.sh` | now |
| VT switching off | `/etc/systemd/logind.conf.d/80-hackbox-vt.conf`, `/etc/X11/xorg.conf.d/10-hackbox.conf` | next boot / next X start |
| slice limits | `/etc/systemd/system/user-2000.slice.d/50-hackbox.conf`, `/etc/security/limits.d/90-hackbox.conf` | now (runtime) and every login |
| attendee firewall | `/etc/hackbox/hackbox.nft`, `hackbox-nft.service`, NM dispatcher `90-hackbox-gw` | now |
| desktop locks | `/etc/dconf/profile/user`, `/etc/dconf/db/local.d/` | next login |
| Chromium policy | `/etc/chromium/policies/managed/hackbox-lockdown.json` | next Chromium start |
| USB storage | allow (no auto-open) or `USB_STORAGE=block` | now / next boot |

The VM lab reaches the guest's sshd over the virtual LAN: set `NFT_INBOUND_TCP="22"` there.
80-lockdown.sh also keeps tcp/22 open by itself when it runs over an ssh session from
outside the tailnet, and says so.

## Tailscale

Join kiosks with `TS_TAG=tag:hackbox` (`setup/05-tailscale.sh` then adds
`--advertise-tags=tag:hackbox --accept-routes=false`), ideally with a tagged, reusable,
pre-approved auth key. Merge `tailscale-acl.hujson` into the tailnet policy: organisers
may reach `tag:hackbox` on tcp:22, and there is no grant with `tag:hackbox` as source, so
a kiosk can reach nothing on the tailnet, even as root. That is the wall behind the on-box
firewall (the attendee can still ask tailscaled for things over its local socket).

## Physical checklist (cannot be scripted)

- [ ] BIOS/UEFI supervisor password set (same on all boxes, kept by the organisers).
- [ ] Boot order: internal NVMe first; USB, network (PXE) and optical boot disabled after install.
- [ ] Firmware boot menu hotkey disabled if the setup allows it.
- [ ] Secure Boot left on if the firmware has the Microsoft UEFI CA (Mint's shim is signed).
- [ ] "Power on after AC loss" set to on (a boot resets the session safely).
- [ ] Wake-on-LAN on if the organisers want remote power-on.
- [ ] Cable lock or tie-down; tamper sticker over the case screws.
- [ ] Label with hostname and the help desk contact.
- [ ] No text console to recover from (VTs are off): make sure ssh over the tailnet works
      before the event.
