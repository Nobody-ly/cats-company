#!/usr/bin/env bash
set -euo pipefail

backup="${1:-}"
schema="${2:-cats_shadow_$(date +%Y%m%d_%H%M%S)}"
container="${CATS_MIGRATION_MYSQL_CONTAINER:-cats-mysql-shadow-load}"
volume="${CATS_MIGRATION_MYSQL_VOLUME:-cats_mysql_shadow_load_data}"
port="${CATS_MIGRATION_MYSQL_PORT:-13307}"
shadow_env="${CATS_SHADOW_ENV:-/srv/catscompany-shadow/env/shadow.env}"
dbmigrate="${CATS_DBMIGRATE_BIN:-/opt/catscompany/bin/dbmigrate}"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [ -z "$backup" ]; then
  echo "usage: $0 /srv/cats-backups/mysql/openchat_YYYYMMDD_HHMMSS.sql.gz cats_shadow_YYYYMMDD_HHMMSS" >&2
  exit 2
fi
if [ ! -f "$backup" ]; then
  echo "backup not found: $backup" >&2
  exit 1
fi
gzip -t "$backup"

case "$schema" in
  cats_shadow_*|cats_migration_*) ;;
  *) echo "schema must start with cats_shadow_ or cats_migration_: $schema" >&2; exit 1 ;;
esac

cleanup
pw="$(openssl rand -hex 16)"
echo "starting temporary MySQL import container"
docker run -d \
  --name "$container" \
  -e MYSQL_ROOT_PASSWORD="$pw" \
  -e MYSQL_DATABASE=openchat \
  -p "127.0.0.1:${port}:3306" \
  -v "${volume}:/var/lib/mysql" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_unicode_ci \
  --default-authentication-plugin=mysql_native_password >/dev/null

for i in $(seq 1 90); do
  if docker exec -e MYSQL_PWD="$pw" "$container" mysql -uroot -e 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 2
  if [ "$i" = "90" ]; then
    echo "temporary MySQL did not become query-ready" >&2
    docker logs --tail=80 "$container" >&2 || true
    exit 1
  fi
done

echo "importing backup: $backup"
gzip -dc "$backup" | docker exec -i -e MYSQL_PWD="$pw" "$container" mysql --binary-mode=1 -uroot openchat

echo "running migration into PostgreSQL schema: $schema"
pg_dsn="$(sed -n 's/^OC_DB_DSN=//p' "$shadow_env" | tail -n 1 | tr -d '\r')"
if [ -z "$pg_dsn" ]; then
  echo "missing OC_DB_DSN in $shadow_env" >&2
  exit 1
fi

mysql_dsn="root:${pw}@tcp(127.0.0.1:${port})/openchat?parseTime=true&charset=utf8mb4"
args=(
  -mode=dry-run-copy
  -schema "$schema"
  -keep-schema
  -confirm-drop-schema "$schema"
)
if [ "${CATS_MIGRATION_ALLOW_LOSSY_CLEANUP:-0}" = "1" ]; then
  args+=( -allow-lossy-cleanup )
else
  echo "CATS_MIGRATION_ALLOW_LOSSY_CLEANUP is not 1; migration will fail if source has rows requiring cleanup." >&2
fi

CATS_MYSQL_DSN="$mysql_dsn" CATS_POSTGRES_DSN="$pg_dsn" "$dbmigrate" "${args[@]}"

echo "verifying PostgreSQL schema counts"
schema_quoted="${schema//\"/\"\"}"
/opt/catscompany/bin/psql-catscompany -At <<SQL
SELECT 'users=' || count(*) FROM "${schema_quoted}".users;
SELECT 'messages=' || count(*) FROM "${schema_quoted}".messages;
SELECT 'feedback_reports=' || count(*) FROM "${schema_quoted}".feedback_reports;
SQL

echo "cleaning temporary MySQL"
cleanup
trap - EXIT
