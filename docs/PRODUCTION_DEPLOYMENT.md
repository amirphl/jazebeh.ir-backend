# Production deployment

The supported deployment is the checked-in beta Docker Compose stack. The
canonical release command starts the API-owned workers, recreates the isolated
campaign scheduler, and runs topology checks.

For migration to a new host, restoring Docker volumes, or importing the large
audience/scheduler dumps, follow
[`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md) instead. Its ordering
and downtime rules take precedence over this fresh-deployment guide.

## Deployed topology

`docker-compose.beta.yml` defines:

- PostgreSQL 15 and Redis 8 for the application;
- a PostgreSQL backup container;
- the Go API and nginx edge proxy;
- pgAdmin behind a dedicated hardened Nginx proxy, with HTTP Basic Auth and
  pgAdmin's own login as separate authentication layers;
- GlitchTip with a separate PostgreSQL and Redis;
- an nginx-to-Sentry forwarder and certificate monitor;
- Prometheus, Grafana, PostgreSQL exporter, node exporter, and cAdvisor;
- the frontend image.

The Nginx edge containers publish host ports: 80 and 443 for the public
application, plus 14433 bound exclusively to the selected host interface for
the dedicated pgAdmin proxy. The API, databases, Redis,
monitoring services, pgAdmin, and standalone campaign scheduler stay on private
Docker networks. The checked-in networks use `172.28.0.0/28` for the dedicated
pgAdmin proxy edge, `172.29.0.0/28` for the pgAdmin-to-PostgreSQL link,
`172.30.0.0/24` for the application, and `172.31.0.0/28` for the
proxy-to-pgAdmin link, so all four subnets must be available on the host. See
[PGADMIN_DEPLOYMENT.md](PGADMIN_DEPLOYMENT.md) before the first deployment.

## Prerequisites

- A Linux host with Docker Engine and Docker Compose v2.
- Git, Python 3, `envsubst` (usually from `gettext-base`), OpenSSL, `sed`,
  `find`, and `mktemp`.
- DNS records for the main, API, monitoring, and Sentry hostnames.
- A DNS record for `pg.<domain>`, an existing main-domain certificate that
  covers it (SAN or wildcard), and the selected host-interface IPv4 address for
  `PGADMIN_LISTEN_BIND_IP` in `.env.beta`.
- Existing, valid certificates at the paths referenced by the Nginx templates.
  Deployment validates certificates but never issues or renews them.
- Enough memory and disk for both PostgreSQL services, Redis services,
  observability services, images, uploads, logs, backups, and WAL. Size these
  from measured data; the repository does not claim a universal minimum.

On Debian/Ubuntu, the non-Docker utilities can be installed with:

```bash
sudo apt-get update
sudo apt-get install -y git python3 gettext-base openssl
docker info
docker compose version
```

## Prepare the repository

Clone or update the repository on the server and create a protected
`.env.beta`:

```bash
cd /srv/yamata
cp env.template .env.beta
chmod 600 .env.beta
```

Replace all placeholders and review the complete variable groups in
[`PRODUCTION_CONFIGURATION.md`](PRODUCTION_CONFIGURATION.md). The deployment
loader rejects unresolved provisioning placeholders and symlinked environment
files.

Production requires this worker split in `.env.beta`:

```env
CAMPAIGN_EXECUTION_ENABLED=false
SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true
SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED=true
TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED=true
SMART_TAG_EVALUATION_ENABLED=true
SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true
SMART_TAG_EVALUATION_DAILY_LIMIT_PER_CUSTOMER=2
CAMPAIGN_MESSAGE_SEND_MOCK_ENABLED=false
```

Also provide both root-level smart-tag prompt files. The deployment script
checks them and makes them readable by the non-root container user.

## Fresh deployment

For an empty/new application database, run:

```bash
cd /srv/yamata
./scripts/deploy-production-beta.sh --domain example.com
```

The wrapper performs these operations:

1. validates `.env.beta` and the required worker ownership;
2. validates pre-provisioned TLS certificates and renders nginx configuration;
3. builds/starts the Compose dependencies;
4. stops application writers and applies migrations through schema head `0128`;
5. starts and health-checks `yamata-app-beta`;
6. recreates `yamata-campaign-scheduler-beta` from the API image and effective
   environment, with campaign-only worker overrides;
7. runs `scripts/check-yamata-production.sh`.

Do not use that fresh-database path to restore a migrated production database.
The migration runbook deliberately restores and imports data before enabling
the scheduler.

## Verify the deployment

```bash
docker compose --env-file .env.beta -f docker-compose.beta.yml ps
./scripts/check-yamata-production.sh /srv/yamata
curl --fail https://example.com/api/v1/health
curl --fail https://api.example.com/api/v1/health
curl -I https://pg.example.com:14433 # expected: 401 HTTP Basic challenge
docker logs --tail 100 yamata-app-beta
docker logs --tail 100 yamata-campaign-scheduler-beta
```

Expected worker settings:

| Container | Campaign execution | Exact-capacity scheduler | Tag Test report scheduler | Smart-tag scheduler |
|---|---:|---:|---:|---:|
| `yamata-app-beta` | `false` | `true` | `true` | `true` |
| `yamata-campaign-scheduler-beta` | `true` | `false` | `false` | `false` |

The scheduler has no published port. Its bot callback base is the internal
`http://app-beta:8080`; port 443 belongs to nginx, not the API container.

## Updates and environment changes

Always use the production wrapper after changing application code, images, or
`.env.beta`:

```bash
cd /srv/yamata
git pull --ff-only
./scripts/deploy-production-beta.sh --domain example.com
```

A container restart does not reload environment variables. The wrapper
recreates the isolated scheduler so it receives the API’s new effective
configuration while retaining the required worker overrides.

Run database migrations only while both writer containers are stopped. See
[`../migrations/README.md`](../migrations/README.md) and prefer the deployment
or required-migrations scripts over ad hoc `psql` execution.

## Operations

Install the maintained host helpers when provisioning a server:

```bash
chmod +x scripts/*.sh
./scripts/install-yamata-operations.sh
```

The helper inventory and safety rules are documented in
[`../scripts/README.md`](../scripts/README.md). Important policies are:

- use `deploy-production-beta.sh` for releases;
- do not run audience and scheduler-runtime imports concurrently;
- stop the API and scheduler for selective imports;
- treat plain database dumps as untrusted input;
- keep certificate renewal separate—the deployed monitor only checks and
  alerts;
- keep backups encrypted and test restoration regularly.

## Troubleshooting

- If certificate validation fails, check the rendered hostnames and the
  `fullchain.pem`, `privkey.pem`, and chain files under `/etc/letsencrypt`.
- If the API is unhealthy, inspect PostgreSQL/Redis health first, then
  `yamata-app-beta` logs and startup configuration validation errors.
- If an environment change is absent from the scheduler, rerun the production
  wrapper; do not only restart the container.
- If a migration or import failed, leave both writers stopped, diagnose the
  first error, and follow the recovery instructions in the migration runbook.
- If HTTPS is accidentally directed to `172.30.0.20`, remove that mapping. The
  API listens on HTTP port 8080 internally; nginx is `172.30.0.30` in the
  checked-in stack.
