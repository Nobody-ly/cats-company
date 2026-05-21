# Tencent migration helper scripts

These scripts are intended for the Tencent Cloud migration host. They are
checked in so the final cutover process is reproducible, but they do not contain
secrets.

Recommended server layout:

- scripts: `/opt/catscompany/bin`
- migration SSH key: `/opt/catscompany/secrets/cats-prod-sync_ed25519`
- backups: `/srv/cats-backups/mysql`
- shadow stack: `/srv/catscompany-shadow`
- test stack: `/srv/catscompany-test`
- prod stack: `/srv/catscompany-prod`

Install on the Tencent CVM:

```bash
sudo install -o root -g root -m 750 deploy/tencent/pull-mysql-backup-from-old-prod.sh /opt/catscompany/bin/pull-mysql-backup-from-old-prod
sudo install -o root -g root -m 750 deploy/tencent/refresh-shadow-from-backup.sh /opt/catscompany/bin/refresh-shadow-from-backup
sudo install -o root -g root -m 750 deploy/tencent/sync-uploads-from-old-prod.sh /opt/catscompany/bin/sync-uploads-from-old-prod
```

Final refresh rehearsal:

```bash
sudo /opt/catscompany/bin/pull-mysql-backup-from-old-prod
sudo CATS_MIGRATION_ALLOW_LOSSY_CLEANUP=1 \
  /opt/catscompany/bin/refresh-shadow-from-backup \
  /srv/cats-backups/mysql/openchat_YYYYMMDD_HHMMSS.sql.gz \
  cats_shadow_YYYYMMDD_HHMMSS
sudo /opt/catscompany/bin/sync-uploads-from-old-prod /srv/catscompany-shadow/data/uploads/
```

`CATS_MIGRATION_ALLOW_LOSSY_CLEANUP=1` must be set only after reviewing the
preflight report. It allows the migration tool to clean known legacy dirty data
such as NUL bytes and invalid JSON samples.

For the final cutover import, use a `cats_prod_YYYYMMDD_HHMMSS` schema and then
point `OC_DB_DSN` at that schema through the `search_path` query parameter.
