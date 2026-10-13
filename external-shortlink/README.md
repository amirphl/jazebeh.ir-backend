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
  bounded concurrent cache. A cache hit avoids a mapping lookup, but click
  persistence remains fail-closed when PostgreSQL is unavailable.
- **Click durability:** every click has a UUID idempotency key and is committed
  transactionally to PostgreSQL before a redirect is returned. PostgreSQL is
  the only durable store; an unavailable database yields `503`, never an
  untracked redirect.
- **Partition safety net:** `clicks_v2` keeps a default partition, so a click is
  never rejected because the partition for its UTC day is missing. Day
  partitions in a rolling window are what retention drops; the default partition
  is drained, never dropped, and holds clicks only while the window is behind.
- **Click retention:** one setting, `EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS`,
  governs the whole click store: the service purges legacy `clicks` rows with
  it, and the daily maintenance unit uses the same value for day partitions and
  `click_event_dedup` rows.
- **Click import:** PostgreSQL assigns the increasing `click_id`. Production
  fetches pages with `after_id`, commits its own import cursor, then
  acknowledges the highest committed ID. Acknowledgements are idempotent.
  Pages are served strictly increasing and de-duplicated. A page that repeats,
  lowers, or omits an ID cannot stall the cursor: the affected rows are
  quarantined with their payload in `external_short_link_click_dead_letters`
  and the cursor advances past everything the page accounted for. A click ID
  held by both the legacy and partitioned tables is served from the partitioned
  table only and logged as a collision, because one click ID identifies one
  click.
- **Operations:** `/healthz`, `/readyz`, and Prometheus `/metrics` are exposed
  only through the proxy configuration. Structured service logs go to journald.

The process starts when PostgreSQL is down so cached mappings can be resolved
after recovery. It returns `503` until PostgreSQL can durably record the click;
it never emits an untracked redirect or invents a destination.

## API contract

The Rust rewrite keeps the API consumed by Yamata unchanged.

| Endpoint | Authentication | Purpose |
| --- | --- | --- |
| `GET /{code}` | public | `302` redirect only after its click is durably committed |
| `HEAD /{code}` | public | the same response as GET without recording a click |
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

`HEAD /{code}` resolves the destination and applies the same bot suppression as
GET but never records a click: probes, link unfurlers and prefetchers send HEAD, and they are
not clicks. It also needs no durable write, so a cached mapping keeps answering
HEAD while the click store is unavailable.

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
- `EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS` — acknowledged click retention in
  days (default `7`). Partitions, deduplication rows, and legacy click rows all
  follow this single value; the deployment refuses to start the maintenance unit
  with anything but a positive integer.

`EXTERNAL_SHORTLINK_BIND_ADDR` defaults to `127.0.0.1:8081`; do not bind it
publicly.

For an existing installation, add
`EXTERNAL_SHORTLINK_DB_LOCK_TIMEOUT_SECONDS=10` to
`/etc/external-shortlink.env` before deploying this version. This is separate
from the command timeout so transient PostgreSQL lock holders get a bounded
chance to clear. Also make sure `EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS` is set
there: the deployment now refuses to deploy without it, and the partition
maintenance unit refuses to run without it, because it is the one setting that
governs click retention for partitions, deduplication rows, and legacy rows.

Acknowledged clicks are retained for `EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS`
days. The installed disk guard writes a warning at 70% filesystem use, an error
(page threshold) at 80%, and stops redirects at 90% before PostgreSQL/WAL
exhaustion. Configure host monitoring to alert on `external-shortlink-disk-guard`
journald errors and expand storage; the guard never deletes retained click
events.

## PostgreSQL maintenance

The deployment applies the idempotent `schema.sql` file on every release. Its
retention index is built with `CREATE INDEX CONCURRENTLY`, so deploying the
retention-query improvement does not block redirect traffic. Do not wrap that
file in a transaction.

`maintain-click-partitions.sql` runs daily through
`external-shortlink-partitions.timer` as the PostgreSQL administrator, because
the runtime role deliberately has no DDL privileges. It keeps the default
partition present, creates the rolling day-partition window, relocates clicks
the default partition already holds for those days (PostgreSQL refuses to attach
a range while its default holds rows in that range), drops only day partitions
whose rows production has acknowledged and whose day is outside the retention
window, prunes deduplication rows on the same retention, and warns when clicks
remain in the default partition. One day failing to create never stops the rest
of the window, because the default partition serves those clicks meanwhile.

The schema import itself tolerates an uncreatable day partition for the same
reason: it warns, keeps the rows in the default partition, and lets the
maintenance unit relocate them rather than failing a deployment.

Partition privileges need no per-day attention. Every statement names the
`clicks_v2` parent, and PostgreSQL checks privileges on the table named in the
statement, so a day partition created by any role - including a manual
`CREATE TABLE ... PARTITION OF` run as the bootstrap superuser - is immediately
usable by the runtime role as long as the parent grant exists. The deployment
grants are applied by `deploy/postgres-init/20-runtime-role.sh`, and the
maintenance SQL re-asserts the same click-store grants (the partitioned parent,
the legacy table, the deduplication table, the acknowledgement row, `links`, and
the click-ID sequence) on every run, so a revoked parent grant is repaired
without a deployment. Should a privilege problem still break the click store,
the service reports it as a named `permission_denied` fault with the action to
take instead of a generic persistence failure.

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

The production server needs egress to the external host on `443`. Nginx allows
`/api/` only from the configured fixed egress address. Client-certificate
settings on the production client require a separately configured Nginx mTLS
policy and are not enabled by this deployment script.
Never expose the external PostgreSQL port or its API token to the public
internet.

## Failure drills and monitoring

Before a real cutover, use a non-production node to test a cache-hot redirect,
then stop PostgreSQL. Confirm the redirect returns `503`, no untracked redirect
is emitted, and normal redirects resume after PostgreSQL restarts. Also test a
mapping conflict and multi-page click import.

Alert on redirect 5xx/unknown-code rates and latency; PostgreSQL failures;
mapping upload failure; click import staleness; PostgreSQL disk/inodes; TLS
expiry; and endpoint availability from the actual target networks. Also alert on
`external_shortlink_default_partition_backlog` (clicks are landing in the default
partition, so the partition window is behind and retention cannot drop them) and
on any increase of `external_shortlink_click_store_fault_total`, whose `reason`
label names a deployment fault rather than a transient database problem:
`missing_partition` (apply the deployment schema) or `permission_denied` (run the
partition maintenance unit or deploy to reconcile grants). Both are logged with
the action to take. A single standalone VM remains a single failure domain;
introduce a second node only with separate click sources or click-ID namespaces.

The processor writes structured journald events for every unexpected redirect
outcome: invalid or missing mapping, lookup timeout/failure, every failed click
write attempt, final withheld redirect, and an invalid redirect header after a
click commit. Persisted successful redirects are debug-level records to avoid
turning click volume into a log-volume outage. Each failure after event creation
includes its `event_id` and short code; logs deliberately omit destinations,
tokens, phone numbers, client IPs, user agents, and referrers. Mapping/API
validation failures, persistence failures, dead-letter batches, scheduler
retries, partition maintenance, and disk guard events are logged as well.

Use `sudo ./scripts/deploy-debian-12.sh logs --since "2 hours ago"` to inspect
the service plus partition and disk-guard units. For PostgreSQL internals, use
`sudo docker compose -f /opt/external-shortlink/app/deploy/postgres.compose.yml logs postgres`.

## Automated short-link visits

Public short-link GET requests still resolve mappings and record clicks. Explicit
bot/tool/preview User-Agents (the same tokens as Go reporting), or client IPv4
addresses in `66.249.0.0/16` and `74.125.0.0/16`, receive HTTP 200 with an empty
body and no `Location`. This prevents matched preview clients from fetching the
destination. Other clients receive the usual HTTP 302. Desktop Linux Chrome
alone and test-link status do not suppress redirects. All public short-link
responses use `Cache-Control: no-store`; unknown mappings and failures retain
their existing status codes. Classification uses the existing forwarded-client
IP handling and is heuristic, not verified bot identity.

Monitor `external_shortlink_suppressed_bot_redirects_total` alongside redirect
status metrics. For deployment smoke tests, use an explicit browser User-Agent
for human redirects: the default curl User-Agent is intentionally suppressed.
Deploy with the existing standalone deployment workflow; roll back the binary
if ordinary browser redirects regress. Bot clicks remain available to the
existing synchronization and reporting pipeline.
