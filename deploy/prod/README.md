# Production Docker Deploy

This stack is the production-side Docker deployment scaffold intended for a
server path such as `/srv/catscompany-prod`.

It is designed to deploy the exact same GHCR image tag that has already passed
the test deployment workflow.

This production scaffold can run against the legacy MySQL database or a
PostgreSQL database for the Tencent Cloud cutover. Keep the stack root as
`/srv/catscompany-prod` so the existing GitHub Actions deployment workflow can
continue to upload compose, env, and release files to the expected location.

Default ports are intentionally non-conflicting so the first rollout can run as
an isolated shadow stack. They bind to `127.0.0.1` by default and should be
published through the host nginx instead of exposed directly to the internet:

- API: `26061`
- gRPC: `26062`
- Web: `28080`

The main repository can later change these values in `prod.env` or move traffic
through the host nginx once the production cutover plan is confirmed.

The default `OC_DB_DSN` example points to `host.docker.internal:3306`, which is
appropriate when the existing production MySQL is already published on the host.
For the shadow-prod phase, use a dedicated DB user such as `openchat_shadow`
instead of reusing the legacy `openchat` account.

For PostgreSQL, set `OC_DB_DRIVER=postgres` and use a URL DSN, for example:

```env
OC_DB_DRIVER=postgres
OC_DB_DSN=postgres://catsco:***@172.16.16.14:5432/catsco?sslmode=prefer
```

## Required server files

Before enabling automatic production deploys:

1. Run `deploy/prod/bootstrap-server.sh` on the server, or let the workflow
   create the directories automatically.
2. Create `<prod-stack-root>/env/prod.env`
3. Copy values from `deploy/prod/env.prod.example`
4. Keep `PROD_STACK_ROOT=<prod-stack-root>`
5. Fill real secrets in `prod.env`
6. Point `OC_DB_DSN` at the active database and set `OC_DB_DRIVER`
7. For the shadow rollout, do not reuse the legacy live-app DB user unless the
   traffic has fully cut over

## Manual start

```bash
cd /srv/catscompany-prod/compose
/usr/local/bin/docker-compose --env-file /srv/catscompany-prod/env/prod.env pull
/usr/local/bin/docker-compose --env-file /srv/catscompany-prod/env/prod.env up -d
```

## Manual rollback

```bash
bash deploy/prod/remote-rollback.sh /srv/catscompany-prod
```
