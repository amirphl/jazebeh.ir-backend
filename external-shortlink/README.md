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
  only through the proxy configuration. Structured service logs go to journald.

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
