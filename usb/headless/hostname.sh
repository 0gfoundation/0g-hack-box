#!/bin/bash
# Print the box's hostname from HB_HOSTNAME ($1): a literal name, or a pattern where {mac4}
# becomes the last 4 hex digits of the first wired network card's MAC. Empty: hackbox-{mac4}.
pat=${1:-hackbox-{mac4\}}
nic=""
for n in /sys/class/net/*; do
  [ -e "$n/device" ] || continue
  [ -e "$n/wireless" ] && continue
  nic=$(basename "$n"); break
done
[ -n "$nic" ] || for n in /sys/class/net/*; do [ -e "$n/device" ] && { nic=$(basename "$n"); break; }; done
mac=$( { tr -d ":" < "/sys/class/net/$nic/address"; } 2>/dev/null)
mac4=$(printf '%s' "$mac" | tail -c 4)
echo "${pat//\{mac4\}/$mac4}"
