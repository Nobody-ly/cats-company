#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/db-migrate.sh [version|up|down|steps|force|goto] [args...]

Environment:
  CATS_DB_DRIVER                 postgres|mysql, default: postgres
  CATS_MIGRATION_DATABASE_URL    required database URL/DSN
  CATS_DB_MIGRATIONS_DIR         optional migration directory override

Examples:
  CATS_MIGRATION_DATABASE_URL='postgres://user:pass@host:5432/db?sslmode=require' scripts/db-migrate.sh version
  CATS_DB_DRIVER=postgres scripts/db-migrate.sh up
  CATS_DB_DRIVER=postgres scripts/db-migrate.sh force 1

The real database URL should live in a server-local env file, not in git.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
driver="${CATS_DB_DRIVER:-postgres}"
driver="${driver,,}"
case "$driver" in
  postgres|postgresql|pg)
    migration_driver="postgres"
    migrations_dir="${CATS_DB_MIGRATIONS_DIR:-$repo_root/server/db/migrations/postgres}"
    ;;
  mysql)
    migration_driver="mysql"
    migrations_dir="${CATS_DB_MIGRATIONS_DIR:-$repo_root/server/db/migrations/mysql}"
    ;;
  *)
    echo "unsupported CATS_DB_DRIVER: $driver" >&2
    exit 2
    ;;
esac

database_url="${CATS_MIGRATION_DATABASE_URL:-}"
if [[ -z "$database_url" ]]; then
  echo "CATS_MIGRATION_DATABASE_URL is required; do not commit it to git." >&2
  exit 2
fi

if [[ ! -d "$migrations_dir" ]]; then
  echo "migration directory not found: $migrations_dir" >&2
  exit 2
fi

command="${1:-version}"
shift || true

echo "Running database migration: driver=$migration_driver dir=$migrations_dir command=$command"
if command -v migrate >/dev/null 2>&1; then
  migrate -path "$migrations_dir" -database "$database_url" "$command" "$@"
elif command -v docker >/dev/null 2>&1; then
  docker run --rm --network host \
    -v "$migrations_dir:/migrations:ro" \
    migrate/migrate \
    -path /migrations \
    -database "$database_url" \
    "$command" "$@"
else
  echo "migrate CLI not found and docker is unavailable." >&2
  echo "Install github.com/golang-migrate/migrate/v4/cmd/migrate or run on a host with Docker." >&2
  exit 127
fi
