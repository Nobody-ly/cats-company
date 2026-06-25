# CatsCompany Database Migrations

This directory is the public, versioned home for database schema migrations.

Current state:

- `000001_baseline` marks the schema that is still created by `server/db/*/schema.go`.
- New production schema changes should be added as new numbered SQL migrations.
- Do not edit an already-applied migration. Add a new one.

Sensitive values never belong here:

- real database URLs or passwords
- `.pgpass`, `.my.cnf`, private keys, or service tokens
- production backup dumps
- command output that contains a full DSN

Use `scripts/db-migrate.sh` with a server-local environment file for real runs.
