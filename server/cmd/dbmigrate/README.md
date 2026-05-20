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
  -mysql-dsn "$CATS_MYSQL_DSN" \
  -postgres-dsn "$CATS_POSTGRES_DSN"
```

Do not run this against the public PostgreSQL schema for rehearsal. Use the temporary schema flow first.
