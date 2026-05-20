# CatsCompany MySQL -> PostgreSQL migration helper

This command is for migration rehearsal before cutting production traffic.

It has two modes:

- `report`: connect to both databases and print source/target row counts. It does not write data.
- `dry-run-copy`: create a temporary PostgreSQL schema, create CatsCompany tables there, copy rows from MySQL, verify row counts, then drop the temporary schema unless `-keep-schema` is set.

Example with a local SSH tunnel to Tencent PostgreSQL:

```bash
ssh -L 15432:172.16.16.14:5432 tenxuncats
```

```bash
go run ./server/cmd/dbmigrate \
  -mode=dry-run-copy \
  -mysql-dsn "$CATS_MYSQL_DSN" \
  -postgres-dsn "postgres://catsco:***@127.0.0.1:15432/catsco?sslmode=disable"
```

For inspection:

```bash
go run ./server/cmd/dbmigrate \
  -mode=dry-run-copy \
  -keep-schema \
  -schema cats_migration_check \
  -confirm-drop-schema cats_migration_check \
  -mysql-dsn "$CATS_MYSQL_DSN" \
  -postgres-dsn "$CATS_POSTGRES_DSN"
```

Do not run this against the public PostgreSQL schema for rehearsal. Use the
temporary schema flow first. Migration schemas must start with `cats_migration_`
or `cats_shadow_`; existing schemas are not dropped unless
`-confirm-drop-schema <name>` matches exactly.

The helper fails by default when it detects source rows that would be skipped or
cleaned during migration, such as orphaned foreign keys, invalid JSON, NUL bytes,
or duplicate values that would violate PostgreSQL unique indexes. Use
`-allow-lossy-cleanup` only after reviewing the printed preflight sample IDs.

To compare row counts after a kept dry-run schema:

```bash
go run ./server/cmd/dbmigrate \
  -mode=report \
  -schema cats_migration_check \
  -mysql-dsn "$CATS_MYSQL_DSN" \
  -postgres-dsn "$CATS_POSTGRES_DSN"
```
