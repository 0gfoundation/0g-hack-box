#!/bin/sh
# Headless installer, initramfs part. Sourced by seed/hackbox-early.sh (busybox sh) when the
# kernel command line says hb_mode=headless. /root is the live root filesystem, /root/cdrom the
# stick. Installs hackbox-live.service into the live session: it brings the network up (wired
# DHCP, else the Wi-Fi profile), starts an ssh server for watching the install, and reports.
mkdir -p /root/etc/systemd/system/multi-user.target.wants
cat > /root/etc/systemd/system/hackbox-live.service <<'UNIT'
[Unit]
Description=hack-box headless installer: network, ssh, progress
Wants=NetworkManager.service
After=NetworkManager.service avahi-daemon.service

[Service]
Type=simple
ExecStart=/bin/bash /cdrom/hackbox/headless/live.sh
Restart=no

[Install]
WantedBy=multi-user.target
UNIT
ln -sf /etc/systemd/system/hackbox-live.service /root/etc/systemd/system/multi-user.target.wants/hackbox-live.service
echo "headless: hackbox-live.service installed in the live session"
