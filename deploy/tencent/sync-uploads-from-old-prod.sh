#!/usr/bin/env bash
set -euo pipefail

old_host="${CATS_OLD_PROD_SSH:-root@101.47.73.222}"
key="${CATS_OLD_PROD_KEY:-/opt/catscompany/secrets/cats-prod-sync_ed25519}"
source="${CATS_OLD_UPLOADS_DIR:-/srv/catscompany-prod/data/uploads/}"
dest="${1:-/srv/catscompany-shadow/data/uploads/}"

mkdir -p "$dest"
rsync -az --delete --stats \
  -e "ssh -i $key -o BatchMode=yes -o StrictHostKeyChecking=accept-new" \
  "$old_host:$source" \
  "$dest"

chown -R "${CATS_UPLOADS_OWNER:-ubuntu:ubuntu}" "$dest" 2>/dev/null || true
