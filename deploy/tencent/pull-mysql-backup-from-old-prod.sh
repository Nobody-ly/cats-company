#!/usr/bin/env bash
set -euo pipefail

old_host="${CATS_OLD_PROD_SSH:-root@101.47.73.222}"
key="${CATS_OLD_PROD_KEY:-/opt/catscompany/secrets/cats-prod-sync_ed25519}"
stamp="${1:-$(date +%Y%m%d_%H%M%S)}"
remote_backup="/srv/cats-backups/mysql/openchat_${stamp}.sql.gz"
local_dir="${CATS_BACKUP_DIR:-/srv/cats-backups/mysql}"
local_backup="$local_dir/openchat_${stamp}.sql.gz"
partial_backup="$local_backup.partial"

mkdir -p "$local_dir"

remote_script=$(cat <<REMOTE
set -e
mkdir -p /srv/cats-backups/mysql
docker exec catscompany-mysql sh -lc 'mysqldump -uroot -p"\$MYSQL_ROOT_PASSWORD" --single-transaction --quick --hex-blob openchat' | gzip -c > "$remote_backup"
gzip -t "$remote_backup"
sha256sum "$remote_backup"
ls -lh "$remote_backup"
REMOTE
)

ssh -i "$key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "$old_host" "$remote_script"
remote_sha=$(ssh -i "$key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "$old_host" "sha256sum '$remote_backup' | cut -d ' ' -f1")

rm -f "$partial_backup"
ssh -i "$key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "$old_host" "cat '$remote_backup'" > "$partial_backup"
gzip -t "$partial_backup"
local_sha=$(sha256sum "$partial_backup" | awk '{print $1}')
if [ "$local_sha" != "$remote_sha" ]; then
  echo "backup checksum mismatch: local=$local_sha remote=$remote_sha" >&2
  exit 1
fi

mv "$partial_backup" "$local_backup"
ls -lh "$local_backup"
