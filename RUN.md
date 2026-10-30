# Yamata no Orochi Fresh-Server Runbook

This document is for a brand-new Ubuntu server with Docker and Docker Compose already installed.

This is not a migration guide.

You should use this flow when the target server starts empty:

- empty PostgreSQL data
- empty Redis data
- empty uploads volume
- no copied `.env.beta`
- no copied certificates

## What This Repo Actually Requires

Before you start, be aware of these repo-specific constraints:

1. The compose stack expects locally built images named `yamata-no-orochi` and `yamata-cert-monitor-beta`.
2. The compose stack also expects a `yamata-frontend-beta` image, but this repository does not include the frontend source or Dockerfile. You must either:
   - load or pull that image from somewhere else, or
   - change the nginx template and compose file before deployment.
3. The nginx template includes hardcoded HTTPS vhosts for `jaazebeh.ir`, `jo1n.ir`, and `joinsahel.ir`. If you are not serving those domains on this server, remove those server blocks before generating the final nginx config. Otherwise nginx will require valid certs for them too.
4. The app is served behind nginx on `https://$DOMAIN`. The current nginx config does not expose a separate `api.$DOMAIN` vhost.
5. The app will not start unless required system/tax env vars are present. On first boot, the app creates those records automatically from config.

## 1. Install Helper Packages

```bash
sudo apt update
sudo apt install -y git curl openssl gettext-base apache2-utils postgresql-client
```

`gettext-base` provides `envsubst`, which is required to render the nginx template.

## 2. Clone The Repo

```bash
sudo mkdir -p /srv
sudo chown "$USER":"$USER" /srv
cd /srv
git clone <your-repo-url> yamata-no-orochi
cd yamata-no-orochi
```

## 3. Prepare `.env.beta`

The deployment scripts and helper scripts expect `.env.beta`.

```bash
cp env.template .env.beta
chmod 600 .env.beta
```

Fill at least these values before starting anything:

- `APP_ENV=production`
- `DB_NAME`, `DB_USER`, `DB_PASSWORD`
- `JWT_SECRET_KEY`
- `DOMAIN`, `MONITORING_DOMAIN`, `SENTRY_UI_DOMAIN`, `CERTBOT_EMAIL`
- `GRAFANA_ADMIN_PASSWORD`
- `SENTRY_POSTGRES_DB`, `SENTRY_POSTGRES_USER`, `SENTRY_POSTGRES_PASSWORD`
- `SENTRY_GLITCHTIP_SECRET_KEY`
- `SENTRY_SUPERUSER_USERNAME`, `SENTRY_SUPERUSER_PASSWORD`, `SENTRY_SUPERUSER_EMAIL`
- `SYSTEM_USER_UUID`, `TAX_USER_UUID`, `SYSTEM_USER_MOBILE`, `TAX_USER_MOBILE`
- `SYSTEM_USER_EMAIL`, `TAX_USER_EMAIL`
- `SYSTEM_WALLET_UUID`, `TAX_WALLET_UUID`
- `SYSTEM_SHEBA_NUMBER`
- `BOT_USERNAME`, `BOT_PASSWORD`
- real provider settings for SMS, payment, and crypto if you plan to use them

Recommended one-liners for generating values:

```bash
uuidgen
openssl rand -hex 32
openssl rand -hex 24
```

Recommended first-boot settings:

- `TLS_ENABLED="false"` because nginx terminates TLS
- `CACHE_REDIS_URL="redis://redis-beta:6379"`
- `CAMPAIGN_EXECUTION_ENABLED="false"` until bot credentials and provider credentials are verified

## 4. Decide What To Do About Extra Hardcoded Domains

Open [docker/nginx/sites-available/yamata-beta.conf](/home/amirphl/Downloads/Yamata-no-Orochi/docker/nginx/sites-available/yamata-beta.conf) and decide:

- If this server must also serve `jaazebeh.ir`, `jo1n.ir`, and `joinsahel.ir`, keep those blocks and provision certs for them.
- If this server is only for your own domain, remove those hardcoded server blocks before rendering the generated config.

Also note:

- `API_DOMAIN` exists in env/config, but the current nginx template does not create an `api.$DOMAIN` vhost.

## 5. Prepare Required Local Directories

```bash
mkdir -p docker/nginx/sites-available/generated/beta
mkdir -p docker/prometheus/rules
```

## 6. Obtain TLS Certificates

The compose stack mounts `/etc/letsencrypt` from the host. Certificates must exist before nginx starts.

If you use `acme.sh`:

```bash
curl https://get.acme.sh | sh
source ~/.bashrc 2>/dev/null || true
```

Issue a certificate for your main deployment domain:

```bash
~/.acme.sh/acme.sh --issue \
  -d "$DOMAIN" \
  -d "www.$DOMAIN" \
  -d "$MONITORING_DOMAIN" \
  -d "$SENTRY_UI_DOMAIN" \
  --standalone
```

Install it into the path nginx expects:

```bash
sudo mkdir -p "/etc/letsencrypt/live/$DOMAIN"
~/.acme.sh/acme.sh --install-cert -d "$DOMAIN" \
  --cert-file "/etc/letsencrypt/live/$DOMAIN/cert.pem" \
  --key-file "/etc/letsencrypt/live/$DOMAIN/privkey.pem" \
  --fullchain-file "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" \
  --chain-file "/etc/letsencrypt/live/$DOMAIN/chain.pem"
```

If you kept the hardcoded `jaazebeh.ir`, `jo1n.ir`, or `joinsahel.ir` nginx blocks, provision those certs too.

## 7. Generate Repo-Side Runtime Files

Load `.env.beta` into the shell first:

```bash
set -a
source .env.beta
set +a
```

Generate the Postgres init files expected by compose:

```bash
bash docker/postgres/process-init-beta.sh
```

Render the nginx config used by the container:

```bash
envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN $HSTS_MAX_AGE $GLOBAL_RATE_LIMIT $AUTH_RATE_LIMIT' \
  < docker/nginx/sites-available/yamata-beta.conf \
  > docker/nginx/sites-available/generated/beta/yamata.conf
```

Normalize permissions for Docker bind-mounted configs. Keep `.env.beta` private, but files and directories under `docker/` must be readable/traversable by container users such as `postgres`, `nginx`, `nobody`, and `grafana`.

```bash
find docker -type d -exec chmod 755 {} +
find docker -type f -exec chmod 644 {} +
chmod 755 docker/postgres/process-init-beta.sh docker/redis/start-redis.sh
```

Verify the generated file exists:

```bash
ls -l docker/nginx/sites-available/generated/beta/yamata.conf
ls -l docker/postgres/postgresql.conf docker/postgres/pg_hba.conf
ls -l docker/postgres/init-beta-processed.sql docker/postgres/init-database-beta-processed.sql
```

Postgres is configured for non-SSL traffic only on the private Docker bridge network. Keep `DB_SSL_MODE="disable"` in `.env.beta`; `docker/postgres/pg_hba.conf` allows the `172.30.0.0/24` compose subnet with SCRAM authentication.

## 8. Build Or Load Docker Images

Build the backend and cert-monitor images from this repo:

```bash
docker build -f docker/Dockerfile.production -t yamata-no-orochi .
docker build -f docker/cert-monitor/Dockerfile -t yamata-cert-monitor-beta docker/cert-monitor
```

For the frontend image, because this repo does not contain its source:

- either `docker pull <registry>/yamata-frontend-beta:<tag>`
- or `docker load -i yamata-frontend-beta.tar`

Confirm all required images exist:

```bash
docker image ls | grep -E 'yamata-no-orochi|yamata-cert-monitor-beta|yamata-frontend-beta'
```

## 9. Start Infrastructure First

Bring up databases, Redis, and monitoring first:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d \
  postgres-beta \
  redis-beta \
  sentry-postgres-beta \
  sentry-redis-beta \
  sentry-beta \
  prometheus-beta \
  grafana-beta \
  postgres-backup-beta \
  postgres-exporter-beta \
  node-exporter-beta \
  cadvisor-beta
```

Check status:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml ps
```

Optional Postgres auth sanity check:

```bash
docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" -tAc "show hba_file;"
docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" -tAc "show ssl;"
```

The first command should point at `/etc/postgresql/pg_hba.conf`; the second should print `off`.

## 10. Initialize The Empty Database

For a fresh server, run migrations.

```bash
./scripts/init-beta-database.sh
```

This script uses `.env.beta`, waits for Postgres, and applies the SQL migrations in `migrations/`.

If it fails with `Database '$DB_NAME' does not exist`, the Postgres data volume was probably initialized before the final `.env.beta` values or processed init files were ready. Create the configured database inside the running Postgres container, apply the database-specific bootstrap SQL once, then rerun the initializer:

```bash
set -a
source .env.beta
set +a

docker exec -e PGPASSWORD="$DB_PASSWORD" yamata-postgres-beta \
  createdb -U "$DB_USER" -O "$DB_USER" "$DB_NAME"

docker exec -i -e PGPASSWORD="$DB_PASSWORD" yamata-postgres-beta \
  psql -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" \
  < docker/postgres/init-database-beta-processed.sql

./scripts/init-beta-database.sh
```

For a truly disposable fresh server, recreating the Postgres data volume also works, but that deletes all existing beta database data. Prefer the manual `createdb` path above unless you are certain the volume contains nothing you need.

## 11. Start The App And Proxy Layer

Start the app first:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d app-beta
```

Check app health inside the container:

```bash
docker exec yamata-app-beta curl -f http://localhost:8080/api/v1/health
```

Then start frontend, nginx, and the log/cert sidecars:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d \
  frontend-beta \
  nginx-beta \
  cert-monitor-beta \
  nginx-sentry-forwarder-beta
```

## 12. First-Boot Bootstrap Data

The app will auto-create the configured system user, tax user, system wallet, and tax wallet on first successful app boot.

You still need to create at least:

- one admin user
- one bot user
- admin role assignment
- line numbers
- pricing/config tables needed by campaign flows

Generate a bcrypt hash for the admin and bot password:

```bash
htpasswd -bnBC 12 "" 'replace-with-a-strong-password' | tr -d ':\n'
```

Open a Postgres shell:

```bash
docker exec -it yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME"
```

Example bootstrap SQL:

```sql
insert into admins (uuid, username, password_hash)
values ('11111111-1111-1111-1111-111111111111', 'admin', '<bcrypt-hash>');

update admins
set roles = array['superadmin']::text[]
where username = 'admin';

insert into bots (uuid, username, password_hash)
values ('22222222-2222-2222-2222-222222222222', 'bot', '<bcrypt-hash>');

insert into line_numbers(name, line_number, price_factor, priority)
values ('default line', '2000023', 1.12, 1);
```

After that, use the admin APIs or direct SQL to populate:

- `platform_base_prices`
- `segment_price_factors`
- `platform_settings`

Without those, the app may be healthy but campaign creation and pricing flows will not be usable.

## 13. Smoke Tests

Container status:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml ps
```

Health endpoints:

```bash
curl -f "https://$DOMAIN/health"
curl -f "https://$DOMAIN/api/v1/health"
curl -I "https://$MONITORING_DOMAIN/grafana/"
curl -I "https://$SENTRY_UI_DOMAIN/"
```

Basic login tests:

```bash
curl -X POST "https://$DOMAIN/api/v1/admin/auth/captcha"
curl -X POST "https://$DOMAIN/api/v1/bot/auth/login" \
  -H "Content-Type: application/json" \
  -H "User-Agent: BotClient/1.0" \
  -d '{"username":"'"$BOT_USERNAME"'","password":"'"$BOT_PASSWORD"'"}'
```

## 14. Recreate A Container With `.env.beta`

Always include `--env-file .env.beta` when recreating beta services. A plain `docker compose -f docker-compose.beta.yml ...` may start containers with blank or shell-default values instead of the deployment env file.

For most config or env changes, use Compose to recreate the service in place:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d --force-recreate <service-name>
```

Examples:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d --force-recreate app-beta
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d --force-recreate postgres-beta app-beta
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d --force-recreate nginx-beta
```

### Large Smart Targeting reservation capacity

Prior versions took a transaction-scoped PostgreSQL advisory lock for every
selected audience. Migration `0149` instead takes one lock per Bundle, which
keeps the cross-table claim guard while avoiding lock-table exhaustion and
opposite-order audience lock waits. `out of shared memory (SQLSTATE 53200)`
from an older deployment means PostgreSQL's shared lock table is full; it does
**not** mean that the host has run out of RAM. The production defaults provide
a 24 GiB PostgreSQL `/dev/shm` mount, 12 GiB shared buffers, and
`max_locks_per_transaction = 4096`.

Deploy migration `0149` through the normal deployment process. For an existing
database that was initialized with the former lock-table setting, also apply
the new value before recreating PostgreSQL (this does not alter data):

```bash
docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" \
  -c "ALTER SYSTEM SET max_locks_per_transaction = '4096';"
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d --force-recreate postgres-beta
docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" \
  -c "SHOW max_locks_per_transaction;"
```

Ensure `.env.beta` contains `POSTGRES_SHM_SIZE="24gb"` before recreating the
container. Do not remove the PostgreSQL volume for this change.

If you specifically need to remove the old container object first, stop and remove only that service container, then start it again through Compose with `.env.beta`:

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml stop <service-name>
docker compose --env-file .env.beta -f docker-compose.beta.yml rm -f <service-name>
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d <service-name>
```

For container-name commands, use the explicit container name:

```bash
docker stop yamata-app-beta
docker rm yamata-app-beta
docker compose --env-file .env.beta -f docker-compose.beta.yml up -d app-beta
```

Do not remove volumes unless you intentionally want to delete persisted data. For example, removing `postgres_data_beta` deletes the beta database.

## 15. Important Notes

- Do not use `migration-plan.md` for this scenario. That file is for copying an existing live environment.
- Do not run `scripts/init-beta-database.sh` on a migrated database restore unless you intentionally want to apply new migrations.
- If `frontend-beta` is unavailable, full nginx startup will fail unless you change the nginx template and compose file accordingly.
- If nginx fails immediately, missing certificates for hardcoded vhosts are the first thing to check.
- If the app logs `no pg_hba.conf entry ... no encryption`, make sure the current compose file mounts `docker/postgres/pg_hba.conf`, then recreate or restart `postgres-beta` and `app-beta`.
