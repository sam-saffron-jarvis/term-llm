#!/bin/bash
# Boot hook — runs every container start via entrypoint.
# Installs runit services from /seed/services/ and enables them.

SEED_SERVICES="/seed/services"
SV_DIR="/etc/sv"
RUNSVDIR="/etc/runit/runsvdir"

if [ ! -d "$SEED_SERVICES" ]; then
  exit 0
fi

mkdir -p "$SV_DIR" "$RUNSVDIR"

for svc_dir in "$SEED_SERVICES"/*/; do
  svc_name="$(basename "$svc_dir")"
  target="$SV_DIR/$svc_name"

  # Always refresh service definitions from seed
  rm -rf "$target"
  cp -r "$svc_dir" "$target"
  chmod +x "$target"/run "$target"/finish 2>/dev/null || true

  # Enable if not already linked
  if [ ! -L "$RUNSVDIR/$svc_name" ]; then
    ln -sf "$target" "$RUNSVDIR/$svc_name"
  fi
done
