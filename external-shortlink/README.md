# External short-link service

`external-shortlink` is a standalone Rust service for public `GET /{code}`
redirects. It deliberately runs on a server separate from the production
application: production publishes immutable mappings through a bearer-authenticated
API and imports click events through an ordered cursor API.

The complete deployment guide for the standalone Debian 12 host is in
[docs/standalone-debian-12.md](docs/standalone-debian-12.md).

For `jzbe.ir`, use the guarded deployment script from the checked-out source:

```sh
sudo ./scripts/deploy-debian-12.sh \
  --api-token-file /root/external-shortlink-api-token \
  --acme-email 'ops@example.com' \
  --configure-ufw
```

`--production-ip 'THE_PRODUCTION_EGRESS_IP'` is optional. Supply it when the
production host has a fixed egress IP to restrict `/api/` at Nginx; omit it
when that address changes, in which case `/api/` relies on bearer-token
authentication alone.

The token file is mode `0600`, contains the exact 32+-character URL-safe token
configured on the production host, and is never printed by the script. The script verifies the
Debian release, `debian` deployment account, checkout location, and host
capacity before making deployment changes.

## Design

- **HTTP server:** Axum/Tokio. One multi-threaded Rust process listens only on
  `127.0.0.1:8081`; Nginx owns public TLS and client IP forwarding.
- **Mappings:** PostgreSQL is the source of truth. Its unique code index plus
  a post-insert conflict check make immutable code-to-destination writes
  race-safe without serializing unrelated batches, and mappings are held in a
  bounded concurrent cache. A cache hit remains
  redirectable while PostgreSQL is unavailable.
- **Click durability:** every click has a UUID idempotency key. Fast
  PostgreSQL writes fall back to a local SQLite WAL spool when they fail or
  exceed the strict timeout. A worker inserts the batch transactionally before
  deleting it from the spool, so replay cannot duplicate events. Capacity is
  tracked from queued payload bytes rather than SQLite file allocation; corrupt
  records are quarantined instead of blocking later replay.
- **Click import:** PostgreSQL assigns the increasing `BIGSERIAL click_id`.
  Production fetches pages with `after_id`, commits its own import cursor, then
  acknowledges the highest committed ID. Acknowledgements are idempotent.
- **Operations:** `/healthz`, `/readyz`, and Prometheus `/metrics` are exposed
  only through the proxy configuration. Structured JSON service logs go to
  journald; Nginx writes matching JSON access records and PostgreSQL emits a
  stable identifying prefix to Docker logs.

## Audit logging

Every Rust log record has `component="external_shortlink"` and an `event`
identifier. Request failures additionally include the Nginx-generated
`request_id`; use it to join `/var/log/nginx/external-shortlink.access.log`
to `journalctl -u external-shortlink`. Nginx records method, path, status and
timings but deliberately excludes credentials, request bodies, client IPs and
user-agent data. The service follows the same privacy boundary: it never logs
tokens, destinations, click payloads, phone numbers, client IPs, referers or
user agents.

Investigate warnings and errors with:

```sh
sudo journalctl -u external-shortlink --since '1 hour ago' -o json-pretty
sudo tail -f /var/log/nginx/external-shortlink.access.log
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml logs -f postgres
```

The service logs state transitions and actionable failures rather than every
successful redirect. Repeated spool-write failures are rate-limited to one
event per minute; their exact volume is retained in Prometheus counters.

## Operations command reference

Run the commands in this section on the dedicated Debian 12 host. The supported
deployment checkout is `/home/debian/Yamata-no-Orochi/external-shortlink`; the
installed release and its Compose file are under `/opt/external-shortlink`.
Do not put API or database secrets on a command line, in shell history, or in
this README.

### First deployment and upgrades

Create the root-only shared-token file once. Its value must exactly match
`EXTERNAL_SHORTLINK_API_TOKEN` on the production application.

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink
sudo install -o root -g root -m 0600 /dev/null /root/external-shortlink-api-token
sudoedit /root/external-shortlink-api-token

# Optional: generate a suitable value before configuring both systems.
openssl rand -hex 32
```

Deploy for the first time. Add `--production-ip 'THE_PRODUCTION_EGRESS_IP'`
when the production host has a stable egress address and `/api/` should be
source-IP restricted.

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink
sudo ./scripts/deploy-debian-12.sh deploy \
  --api-token-file /root/external-shortlink-api-token \
  --acme-email 'ops@example.com' \
  --configure-ufw
```

For an upgrade, first obtain the reviewed source revision as the `debian`
user, confirm the service subtree is clean, then rerun the same guarded deploy
command. The script builds before replacing the running binary and validates
Nginx before reload.

```sh
cd /home/debian/Yamata-no-Orochi
git status --short external-shortlink
git pull --ff-only
cd external-shortlink
sudo ./scripts/deploy-debian-12.sh deploy \
  --api-token-file /root/external-shortlink-api-token
```

### Service lifecycle and health

Use the deployment script for normal lifecycle actions. `stop` stops only the
Rust redirect process; the private PostgreSQL container remains running.

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink
sudo ./scripts/deploy-debian-12.sh start
sudo ./scripts/deploy-debian-12.sh stop
sudo ./scripts/deploy-debian-12.sh restart
sudo ./scripts/deploy-debian-12.sh status
```

Check liveness, PostgreSQL readiness, the local metrics endpoint, and the
public proxy/TLS path. `curl` is intentionally classified as an automated
visitor for short-code redirects, so use these health endpoints for smoke
tests.

```sh
curl -fsS http://127.0.0.1:8081/healthz
curl -fsS http://127.0.0.1:8081/readyz
curl -fsS http://127.0.0.1:8081/metrics
curl -fsSI --resolve jzbe.ir:443:127.0.0.1 https://jzbe.ir/healthz
sudo systemctl status external-shortlink --no-pager --full
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml ps
```

### Logs and incident investigation

The supported service-log command reads journald. Use JSON output when you
need to filter the Rust `event` or correlate the Nginx `request_id`.

```sh
cd /home/debian/Yamata-no-Orochi/external-shortlink
sudo ./scripts/deploy-debian-12.sh logs
sudo ./scripts/deploy-debian-12.sh logs --lines 500 --since '2 hours ago'
sudo ./scripts/deploy-debian-12.sh logs --follow

sudo journalctl -u external-shortlink --since '1 hour ago' -o json-pretty
sudo journalctl -u external-shortlink -p warning --since today --no-pager
sudo tail -F /var/log/nginx/external-shortlink.access.log
sudo tail -F /var/log/nginx/external-shortlink.error.log
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml logs --tail 200 -f postgres
sudo tail -F /var/log/external-shortlink/deploy.log
```

To investigate one failed proxied request, copy its `request_id` from the
Nginx JSON access log and search the application journal:

```sh
sudo grep -F 'REQUEST_ID_FROM_NGINX' /var/log/nginx/external-shortlink.access.log
sudo journalctl -u external-shortlink --since '2 hours ago' -o cat | grep -F 'REQUEST_ID_FROM_NGINX'
```

### Monitoring and capacity

Prometheus should scrape `/metrics` from an address allowed by Nginx. For a
local investigation, save the response and inspect redirect failures,
PostgreSQL failures, spool pressure, and pool timeouts.

```sh
curl -fsS http://127.0.0.1:8081/metrics | tee /tmp/external-shortlink.metrics
rg 'external_shortlink_(redirect_requests_total|postgres_errors_total|postgres_pool_timeouts_total|spool_|mapping_uploads_total)' /tmp/external-shortlink.metrics

df -h / /var/lib/external-shortlink
df -i / /var/lib/external-shortlink
free -h
uptime
sudo du -sh /var/lib/external-shortlink
sudo docker stats --no-stream
```

#### Connect the central Prometheus and Grafana stack

On the `jzbe.ir` host, run the setup script with the **public egress IP of the
server that runs `docker-compose.beta.yml`**. The IP was not included in the
request, so it is intentionally a required argument rather than a hard-coded
value. This installs Debian's node exporter locally and allows that one IP to
scrape the app and host metrics through Nginx/TLS.

```sh
ssh debian@jzbe.ir
cd /home/debian/Yamata-no-Orochi/external-shortlink
sudo ./scripts/deploy-debian-12.sh deploy --api-token-file /root/external-shortlink-api-token
sudo ./scripts/setup-monitoring-debian-12.sh --monitoring-ip 'APP_BETA_PUBLIC_IP'

curl -fsS http://127.0.0.1:9100/metrics | head
sudo nginx -t
```

The central Prometheus configuration includes the `external_shortlink` and
`external_shortlink_node` HTTPS jobs, while
`docker/prometheus/rules/external-shortlink.yml` provides baseline availability,
5xx, spool-age, and click-loss alerts. Apply the configuration on the server
that hosts the beta Compose stack:

```sh
cd /path/to/Yamata-no-Orochi
docker compose -f docker-compose.beta.yml exec prometheus-beta \
  promtool check config /etc/prometheus/prometheus.yml
docker compose -f docker-compose.beta.yml exec prometheus-beta \
  wget -qO- --post-data='' http://127.0.0.1:9090/-/reload
```

Useful Grafana/Prometheus expressions include:

```promql
# Redirect p99
histogram_quantile(0.99, sum by (le) (rate(external_shortlink_redirect_latency_seconds_bucket[5m])))

# Redirect 5xx percentage
100 * sum(rate(external_shortlink_redirect_requests_total{status=~"5.."}[5m])) / clamp_min(sum(rate(external_shortlink_redirect_requests_total[5m])), 0.001)

# Spool pressure and click-loss rate
external_shortlink_spool_oldest_event_age_seconds
sum by (reason) (rate(external_shortlink_spool_rejected_total[5m]))

# Remote host CPU and available memory
100 - (avg by (instance) (rate(node_cpu_seconds_total{job="external_shortlink_node",mode="idle"}[5m])) * 100)
node_memory_MemAvailable_bytes{job="external_shortlink_node"}
```

The spool is a live SQLite database. Do not edit, delete, copy only its main
file, or run `VACUUM` against it while the service is active. Monitor it via
metrics; recovery backups must include the SQLite database plus `-wal` and
`-shm` sidecars.

### PostgreSQL and Nginx maintenance

Check the effective deployment configuration before a manual reload. Let the
deployment script manage service files; use these commands only for diagnosis
or an explicitly reviewed configuration change.

```sh
sudo nginx -t
sudo systemctl reload nginx
sudo systemctl status nginx --no-pager --full
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml exec -T postgres \
  pg_isready --host 127.0.0.1 --port 5432 --username external_shortlink_admin --dbname external_shortlink
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml exec -T postgres \
  sh -ceu 'PGPASSWORD="$POSTGRES_PASSWORD" psql --host 127.0.0.1 --port 5432 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "SELECT now(), pg_database_size(current_database());"'
```

For a large existing `links` table, refresh planner statistics and reclaim
reusable space without taking an exclusive table lock:

```sh
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml \
  exec -T postgres sh -ceu 'PGPASSWORD="$POSTGRES_PASSWORD" psql --host 127.0.0.1 --port 5432 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "VACUUM (ANALYZE, VERBOSE) links"'
```

Do not use `VACUUM FULL` during normal operation: it takes an exclusive lock.

### Backups

Take PostgreSQL dumps to storage outside this VM and include the SQLite spool.
To obtain a consistent spool backup, briefly stop the redirect service before
archiving its database and WAL sidecars, then start it again. Verify that the
backup destination has sufficient space before running these commands.

```sh
sudo install -d -o root -g root -m 0700 /root/external-shortlink-backups
sudo sh -ceu 'docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml exec -T postgres sh -ceu '\''PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --host 127.0.0.1 --port 5432 --username "$POSTGRES_USER" --format=custom --file=- "$POSTGRES_DB"'\'' > /root/external-shortlink-backups/postgres-$(date -u +%Y%m%dT%H%M%SZ).dump'

sudo systemctl stop external-shortlink
sudo tar -C /var/lib/external-shortlink -czf /root/external-shortlink-backups/spool-$(date -u +%Y%m%dT%H%M%SZ).tar.gz \
  click-spool.sqlite3*
sudo systemctl start external-shortlink
sudo systemctl is-active --quiet external-shortlink
```

Copy the resulting backup files to encrypted off-host storage and test restore
procedures on a non-production host. Do not overwrite a live PostgreSQL volume
or spool as an ad-hoc recovery action.

The process starts when PostgreSQL is down so that existing cached mappings can
continue to redirect after a transient outage. On initial cold start without a
database connection, unknown mappings return `503` until PostgreSQL recovers;
it never invents a destination.

## API contract

The Rust rewrite keeps the API consumed by Yamata unchanged.

| Endpoint | Authentication | Purpose |
| --- | --- | --- |
| `GET /{code}` | public | `302` redirect and an asynchronous durable click write |
| `GET /healthz` | public through Nginx | process liveness |
| `GET /readyz` | public through Nginx | PostgreSQL readiness |
| `GET /metrics` | monitoring IP allowlist | Prometheus metrics |
| `POST /api/v1/links/batch` | Bearer token | create or refresh an immutable mapping batch |
| `GET /api/v1/clicks?after_id=N&limit=N` | Bearer token | ordered click page |
| `POST /api/v1/clicks/ack` | Bearer token | acknowledge a committed click cursor |

`POST /api/v1/links/batch` rejects a code whose destination changes with `409`.
It accepts metadata refreshes for the same destination and returns
`{"persisted", "created", "existing"}`. Admin request bodies are capped by
`EXTERNAL_SHORTLINK_MAX_ADMIN_BODY_BYTES`; batches are capped by
`EXTERNAL_SHORTLINK_ADMIN_BATCH_MAX_LINKS`.

## Development

Use Rust 1.85 or newer. The repository includes a lockfile, so builds should
normally use `--locked`.

```sh
cd external-shortlink
cargo test --locked
cargo clippy --all-targets -- -D warnings
cargo build --release --locked
```

Unit tests do not require PostgreSQL. The Debian runbook explains how to start
the service database for manual/integration validation. The PostgreSQL test is
deliberately ignored unless explicitly requested, so a missing database can
never look like a passing integration test:

```sh
EXTERNAL_SHORTLINK_TEST_DATABASE_URL='postgresql://external_shortlink_runtime:password@127.0.0.1:5433/external_shortlink' \
  cargo test --locked --test postgres -- --ignored
```

## Configuration

Copy [deploy/external-shortlink.env.example](deploy/external-shortlink.env.example)
to `/etc/external-shortlink.env` on the external host. The required values are:

- `EXTERNAL_SHORTLINK_DATABASE_URL` — local, private PostgreSQL URL.
- `EXTERNAL_SHORTLINK_API_TOKEN` — at least 32 URL-safe characters
  (`A-Z`, `a-z`, `0-9`, `.`, `_`, `~`, `-`); this must exactly match the
  production application's token.

All remaining variables in the example retain the former service’s names and
defaults. `EXTERNAL_SHORTLINK_BIND_ADDR` defaults to `127.0.0.1:8081`; do not
bind it publicly. The spool path must be writable by the `external-shortlink`
service account and live on durable local storage.

For an existing installation, add
`EXTERNAL_SHORTLINK_DB_LOCK_TIMEOUT_SECONDS=10` to
`/etc/external-shortlink.env` before deploying this version. This is separate
from the command timeout so transient PostgreSQL lock holders get a bounded
chance to clear. The default spool-operation wait is now 30 seconds; set
`EXTERNAL_SHORTLINK_SPOOL_OPERATION_TIMEOUT_SECONDS` only when a different
durability-versus-redirect-latency trade-off is required.

The production example reserves up to 48 GiB for the click spool and caps it
at 20 million events, leaving roughly 27 GB on a 75 GB host for PostgreSQL,
the operating system, and growth. Alert before the spool reaches either limit;
no finite local disk allocation can guarantee retention during an indefinitely
long PostgreSQL outage.

## PostgreSQL maintenance

The deployment applies the idempotent `schema.sql` file on every release. Its
retention index is built with `CREATE INDEX CONCURRENTLY`, so deploying the
retention-query improvement does not block redirect traffic. Do not wrap that
file in a transaction.

After deploying this version to an existing installation with a large `links`
table, run the following once during normal operation to reclaim reusable
space and refresh planner statistics:

```sh
sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml \
  exec -T postgres sh -ceu 'PGPASSWORD="$POSTGRES_PASSWORD" psql --host 127.0.0.1 --port 5432 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "VACUUM (ANALYZE, VERBOSE) links"'
```

`VACUUM FULL` is deliberately not part of the procedure because it takes an
exclusive table lock. If disk space must be returned to the host, schedule
`pg_repack` separately.

## Production application configuration

Apply production migration `0135_external_short_link_sync.sql`, then configure
the production host with the external service origin and the same API token:

```dotenv
EXTERNAL_SHORTLINK_ENABLED=true
EXTERNAL_SHORTLINK_BASE_URL=https://jzbe.ir
EXTERNAL_SHORTLINK_API_TOKEN=the_same_strong_token
EXTERNAL_SHORTLINK_MAPPING_SYNC_INTERVAL=1m
EXTERNAL_SHORTLINK_CLICK_SYNC_INTERVAL=5m
EXTERNAL_SHORTLINK_MAPPING_BATCH_SIZE=500
EXTERNAL_SHORTLINK_MAPPING_PARALLELISM=4
EXTERNAL_SHORTLINK_CLICK_PAGE_SIZE=1000
```

The production server needs egress to the external host on `443`. When
`--production-ip` is supplied, the Nginx configuration allows `/api/` only
from that fixed egress address. Without it, the bearer token is the API's only
access control. Client-certificate settings on the production client require a
separately configured Nginx mTLS policy and are not enabled by this deployment
script.
Never expose the external PostgreSQL port, its API token, or its SQLite spool
to the public internet.

## Failure drills and monitoring

Before a real cutover, use a non-production node to test a cache-hot redirect,
then stop PostgreSQL. Confirm the redirect stays `302`, spool metrics increase,
and events become fetchable after PostgreSQL restarts. Also test a mapping
conflict, restart with a non-empty spool, a full-spool alert, and multi-page
click import.

Alert on redirect 5xx/unknown-code rates and latency; PostgreSQL failures;
spool event count, bytes, oldest age, and rejected writes; mapping upload
failure; click import staleness; VM disk/inodes; TLS expiry; and endpoint
availability from the actual target networks. A single standalone VM remains a
single failure domain; introduce a second node only with separate click sources
or click-ID namespaces.

The processor writes structured journald events for startup configuration,
cache preloads, mapping batches, replay deferral/drain, spool quarantine and
compaction, and click-cursor acknowledgements. It deliberately omits URLs,
tokens, client IPs, user agents, and phone numbers. Repeated spool-write
failures are rate-limited; use the Prometheus counters for their full volume.

## Automated short-link visits

Public short-link requests still resolve mappings and record clicks. Explicit
bot/tool/preview User-Agents (the same tokens as Go reporting), or client IPv4
addresses in `66.102.0.0/16`, `66.249.0.0/16`, `74.125.0.0/16`, or
`192.178.0.0/16`, receive HTTP 200 with an empty body and no `Location`. This
prevents matched preview clients from fetching the destination. Other clients
receive the usual HTTP 302. Desktop Linux Chrome alone and test-link status do
not suppress redirects. All public short-link responses use `Cache-Control:
no-store`; unknown mappings and failures retain their existing status codes.
Classification uses the existing forwarded-client IP handling and is heuristic,
not verified bot identity.

Monitor `external_shortlink_suppressed_bot_redirects_total` alongside redirect
status metrics. For deployment smoke tests, use an explicit browser User-Agent
for human redirects: the default curl User-Agent is intentionally suppressed.
Deploy with the existing standalone deployment workflow; roll back the binary
if ordinary browser redirects regress. Bot clicks remain available to the
existing synchronization and reporting pipeline.
