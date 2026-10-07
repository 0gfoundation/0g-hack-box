#!/bin/bash
# Build an initrd = stock Mint initrd + one extra cpio segment holding the hackbox preseed.
#   build-initrd.sh <stock-initrd> <out-initrd> <seed-dir> [authorized_keys]
# The kernel unpacks concatenated cpio archives in order, so /preseed.cfg and the hook
# scripts appear in the initramfs root, where casper's 24preseed picks them up.
set -euo pipefail
stock="$1"; out="$2"; seed="$3"; keys="${4:-}"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
cp "$seed/hackbox.seed" "$stage/preseed.cfg"
cp "$seed/hackbox-early.sh" "$stage/hackbox-early.sh"
cp "$seed/hackbox-late.sh" "$stage/hackbox-late.sh"
[ -n "$keys" ] && cp "$keys" "$stage/hackbox-authorized_keys"
chmod 755 "$stage"/*.sh
( cd "$stage" && find . -mindepth 1 | cpio -o -H newc --quiet | gzip -9 ) > "$stage.cpio.gz"
cat "$stock" "$stage.cpio.gz" > "$out"
rm -f "$stage.cpio.gz"
echo "built $out ($(stat -c %s "$out") bytes)"
