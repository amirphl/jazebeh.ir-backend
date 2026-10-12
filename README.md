# Yamata no Orochi

Yamata no Orochi is a production-oriented Go backend for the Jazebeh messaging and campaign platform. It exposes customer, admin, and bot APIs for authentication, multi-platform campaign management, campaign bundles, AI-assisted bundle/tag scoring, wallet and payment operations, audience pricing, short links, support tickets, media uploads, and operational reporting.

The service is built with Fiber v3, GORM, PostgreSQL, Redis, Prometheus metrics, Sentry-compatible error reporting, SQL migrations, and Docker-based beta deployment assets.

## What It Does

- Customer signup, OTP verification, password login, OTP login, password reset, and profile lookup.
- Admin authentication with captcha-backed login and permission-gated admin APIs.
- Bot authentication and bot-only campaign, short-link, audience, and media endpoints.
- Multi-platform campaigns for SMS, Bale, Rubika, and Soroush Plus.
- Campaign lifecycle operations: create, update, clone, test-send, price/capacity calculation, approval, rejection, rescheduling, cancellation, execution, status polling, and export.
- Customer-owned bundles that group test and execution campaigns, enforce audience-selection rules, and expose bundle-level statistics.
- Asynchronous smart-tag evaluation for bundle personas, including OpenAI Responses API calls, immutable configuration snapshots, batched tag scoring, retries, status tracking, and paginated score results.
- Wallet, transaction history, Atipay payment callbacks, deposit receipts, proforma previews, and invoice attachment workflows.
- Crypto payment requests and provider callbacks through the configured provider layer.
- Agency reports, discounts, customer shares, line numbers, platform settings, base prices, segment price factors, and page prices.
- Short-link creation, allocation, redirect tracking, click exports, and scenario-based reporting.
- File-backed multimedia upload/download/preview flows for users, admins, and bots.
- Prometheus HTTP and database metrics, structured logs, request IDs, Sentry/GlitchTip reporting, and graceful shutdown.

## Architecture

The codebase follows a layered structure:

```text
app/
  dto/                 Request and response DTOs
  handlers/            Fiber HTTP handlers
  middleware/          Auth, authorization, metrics, and request middleware
  observability/       Sentry-compatible error reporting
  router/              Route and middleware registration
  scheduler/           Campaign execution, status, and smart-tag workers
  services/            Messaging, payment, token, OpenAI, and shared services
business_flow/         Use-case orchestration and domain rules
models/                GORM models
repository/            Data access layer
migrations/            Ordered SQL up/down migrations
docker/                Docker, nginx, Postgres, Redis, Prometheus, and Grafana assets
scripts/               Deployment and operational helper scripts
docs/                  Generated Swagger/OpenAPI files and production guides
templates/             Payment success/failure HTML templates
py-ai/                 Offline/auxiliary Python tooling for audiences and tags
```

`main.go` wires configuration, logging, Sentry, PostgreSQL, Redis, repositories, business flows, handlers, routes, campaign schedulers, and the metrics server.

## Requirements

- Go 1.26.x. The module declares `go 1.26.0` and `toolchain go1.26.2`.
- PostgreSQL 15+.
- Redis, when cache health checks and campaign execution are enabled.
- `psql` and `createdb` for migration Make targets.
- `swag` for Swagger regeneration. `make swag` installs it if it is missing.
- Docker and Docker Compose for beta-style deployment.
- Python 3 for the optional `py-ai/` and `scripts/` helpers.

## Configuration

Configuration is read from environment variables in `config/production.go`. Start from `env.template`:

```bash
cp env.template .env
```

Then replace placeholder values such as database credentials, JWT secret or RSA keys, domain names, provider tokens, admin mobile numbers, system wallet UUIDs, and payment provider credentials.

Smart-tag evaluation system prompts are not environment variables. Put their multiline text in these files at the repository root:

- `SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT`
- `SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT`

The files are read verbatim at startup. Both must exist when `SMART_TAG_EVALUATION_ENABLED=true`.

Common local values:

```bash
APP_ENV=development
DB_HOST=localhost
DB_PORT=5432
DB_NAME=yamata_no_orochi
DB_USER=postgres
DB_PASSWORD=postgres
DB_SSL_MODE=disable
SERVER_HOST=0.0.0.0
SERVER_PORT=8080
JWT_SECRET_KEY=replace-with-a-long-random-secret
CACHE_REDIS_URL=redis://localhost:6379
CAMPAIGN_EXECUTION_ENABLED=false
LOG_OUTPUT=stdout
```

Important groups:

- `DB_*`: PostgreSQL connection and pool settings.
- `SERVER_*`: bind address, body limit, timeouts, compression, and proxy settings.
- `JWT_*`: HS256 secret or RSA key configuration.
- `CORS_*`, `RATE_LIMIT_*`, `REQUIRE_API_KEY`, `ALLOWED_API_KEYS`: API security controls.
- `CACHE_*`: Redis connection and cache behavior.
- `METRICS_*`: Prometheus metrics server.
- `SENTRY_*`: Sentry or GlitchTip reporting, including the external URL via `SENTRY_URL_PREFIX` and the GlitchTip UI host allowlist via `SENTRY_ALLOWED_HOSTS`.
- `ATIPAY_*`, `CRYPTO_*`, `OXA_*`: fiat and crypto payment providers.
- `PAYAM_SMS_*`, `BALE_*`, `RUBIKA_*`, `SPLUS_*`: messaging providers.
- `BOT_*`, `CAMPAIGN_EXECUTION_*`: internal bot client and scheduler behavior.
- `SMART_TAG_EVALUATION_*`: smart-tag scheduler, OpenAI client, batching, and validation settings; prompt text remains in the two root files above.
- `ADMIN_*`, `SYSTEM_*`, `TAX_*`: privileged users and accounting identities.

## Local Development

Install dependencies:

```bash
go mod download
```

Create a local database:

```bash
createdb yamata_no_orochi
```

Then review the [migration README](migrations/README.md) before applying the schema. In this snapshot, `run_all_up.sql` has known stale entries for migrations `0052` and `0054` and omits `0104_create_splus_status_results.sql`; running it unchanged with `ON_ERROR_STOP=1` will fail.

Run the service with environment variables from `.env`:

```bash
set -a
source .env
set +a
go run main.go
```

The API listens on `http://localhost:8080` by default. The health endpoint is:

```bash
curl http://localhost:8080/api/v1/health
```

The Makefile also provides:

```bash
make run       # source .env and run main.go
make run-dev   # source .env and run with -race
make build     # build bin/yamata-no-orochi
make fmt
make vet
make lint
```

Note: `make run-dev-simple` currently expects `scripts/run-dev.sh`, which is not present in this repository snapshot.

## Database Migrations

Migrations live in `migrations/` and are numbered up to schema head `0119_convert_bundle_tag_evaluation_ids_to_bigserial.sql`. The intended aggregate files are:

- `migrations/run_all_up.sql`
- `migrations/run_all_down.sql`

Before using the aggregate scripts, reconcile the manifest issues listed in [migrations/README.md](migrations/README.md). Once reconciled, run all up migrations with:

```bash
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -f migrations/run_all_up.sql
```

The Makefile wraps the same aggregate up script after exporting `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, and `DB_NAME`:

```bash
make migrate
```

The schema includes customers, account types, sessions, audit logs, bundles, campaigns, bundle audience selections, audience scores, smart-tag evaluation runs/events/attempts/batches/results, processed campaign rows, platform-scoped message status jobs and results, short links, audience caches, wallets, transactions, payment requests, deposit receipts, crypto payments, tickets, media, admins, bots, line numbers, pricing tables, platform settings, and access-control requests.

## API Overview

All JSON APIs are under `/api/v1` unless noted. Most protected endpoints return the shared response envelope:

```json
{
  "success": true,
  "message": "ok",
  "data": {}
}
```

Error responses use:

```json
{
  "success": false,
  "message": "request failed",
  "error": {
    "code": "ERROR_CODE",
    "details": {}
  }
}
```

Main route groups:

- `GET /api/v1/health`
- `/api/v1/auth/*`: customer signup, OTP verification, login, OTP login, password reset.
- `/api/v1/admin/auth/*`: admin captcha and login.
- `/api/v1/bot/auth/*`: bot login.
- `/api/v1/campaigns/*`: customer campaign CRUD, clone, test-send, cost/capacity, reports, cancellation, audience spec, and approved/running summary.
- `/api/v1/bundles/*`: customer bundle CRUD plus asynchronous tag-evaluation requests, current status, and paginated tag scores.
- `/api/v1/admin/campaigns/*`: campaign moderation and admin reporting.
- `/api/v1/bot/campaigns/*`: ready campaign feed, audience spec updates, execution state, statistics, and target audience file download.
- `/api/v1/wallet/*`, `/api/v1/payments/*`, `/api/v1/admin/payments/*`: wallet, fiat payment, receipt, invoice, and transaction flows.
- `/api/v1/crypto/*` and `/api/v1/crypto/providers/:platform/callback`: crypto payment requests and callbacks.
- `/api/v1/reports/agency/*`: agency customer and discount reports.
- `/api/v1/line-numbers/*`, `/api/v1/admin/line-numbers/*`: line number selection and administration.
- `/api/v1/segment-price-factors/*`, `/api/v1/admin/segment-price-factors/*`: audience factor pricing.
- `/api/v1/platform-base-prices/*`, `/api/v1/admin/platform-base-prices/*`: base price configuration.
- `/api/v1/platform-settings/*`, `/api/v1/admin/platform-settings/*`: customer platform settings and admin review.
- `/api/v1/tickets/*`, `/api/v1/admin/tickets/*`: support tickets and replies.
- `/api/v1/media/*`, `/api/v1/admin/media/*`, `/api/v1/bot/media/*`: media upload, download, and preview.
- `/api/v1/admin/customer-management/*`: customer reports and active-status controls.
- `/api/v1/admin/short-links/*`, `/api/v1/bot/short-links/*`: short-link administration and bot allocation.
- `/api/v1/admin/access-control/*`: maker-checker access-control requests.
- `GET /s/:uid` and `GET /:uid`: public short-link redirects.

Development-only documentation routes are enabled when `APP_ENV` is `development` or `local`:

- `GET /api/v1/docs`
- `GET /api/v1/swagger.json`
- `GET /swagger`
- `GET /swagger-standalone`
- `GET /swagger-ui-assets/*`

Regenerate OpenAPI files with:

```bash
make swag
```

## Authentication Surfaces

The application has three separate authenticated contexts:

- Customer APIs use `Authorization: Bearer <customer_access_token>`.
- Admin APIs use admin authentication plus role/permission authorization middleware.
- Bot APIs use bot authentication and are intended for internal campaign execution clients.

Seed helpers and operational snippets for local admin/bot setup are kept in `RUN.md`.

## Campaign Execution

When `CAMPAIGN_EXECUTION_ENABLED=true`, the app starts scheduler workers for:

- SMS
- Bale
- Rubika
- Soroush Plus

Schedulers poll ready campaigns through the internal bot API, fetch audience data, send through the configured provider clients, create sent-message rows, enqueue status checks, update processed campaign statistics, and notify configured admins on notable failures.

For local API development, set `CAMPAIGN_EXECUTION_ENABLED=false` unless you intentionally want the workers to call provider and bot endpoints.

Smart-tag evaluation is independent of campaign execution. When both `SMART_TAG_EVALUATION_ENABLED=true` and `SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true`, a bounded-concurrency worker claims queued bundle evaluations and processes persona analysis and tag-score batches through the configured OpenAI-compatible Responses API.

`SMART_TAG_EVALUATION_DAILY_LIMIT_PER_CUSTOMER` limits each customer to newly queued evaluations per UTC calendar day (default: `2`). Failed runs still count, so retries cannot create unbounded provider cost.

## Observability

- Application HTTP metrics are registered through `app/middleware/metrics.go`.
- Database pool metrics are exported by `main.go`.
- Prometheus is served by a separate HTTP server when `METRICS_ENABLED=true` and `METRICS_ENABLE_PROMETHEUS=true`.
- The default metrics address is `:9090/metrics`.
- Request IDs are generated and exposed through `X-Request-ID`.
- Structured access logs are emitted by Fiber middleware.
- File logging uses lumberjack rotation when `LOG_OUTPUT=file` or `LOG_OUTPUT=both`.
- Sentry-compatible reporting is initialized from `SENTRY_*`; the beta compose stack includes GlitchTip services.

## Docker And Beta Deployment

Beta deployment assets are included for the full operational stack:

- `docker-compose.beta.yml`
- `docker/Dockerfile.production`
- `docker/nginx/*`
- `docker/postgres/*`
- `docker/redis/*`
- `docker/prometheus_config.yml`
- `docker/grafana/*`
- `scripts/deploy-beta.sh`
- `scripts/init-beta-database.sh`
- `scripts/cert_monitor.py`
- `scripts/nginx_sentry_forwarder.py`

The beta compose stack includes the Go app, PostgreSQL, Redis, backup container, nginx, Prometheus, Grafana, and GlitchTip/Sentry-compatible services.

See the production docs for deployment-specific checklists:

- `docs/PRODUCTION_DEPLOYMENT.md`
- `docs/PRODUCTION_CONFIGURATION.md`
- `docs/PRODUCTION_SECURITY_CHECKLIST.md`
- `docs/MULTI_SERVER_DEPLOYMENT.md`

## Testing

Run the full Go test suite:

```bash
go test ./...
```

Run with the race detector:

```bash
go test -race ./...
```

Run the maintained database/Redis-free CI subset:

```bash
make ci-test-unit
```

The Makefile has older `./tests` targets that depend on test database variables. Prefer `go test ./...` unless you are specifically maintaining that legacy test flow.

## Useful Files

- `env.template`: complete environment variable template.
- `RUN.md`: local operational notes, seed SQL, and manual provider tooling commands.
- `TODO.md`: active project notes.
- `docs/swagger.yaml` and `docs/swagger.json`: generated OpenAPI output.
- `docs/bale.md` and `Najva.md`: Bale/Najva integration notes.
- `migrations/README.md`: current schema head, grouped migration history, execution guidance, and known aggregate-manifest issues.
- `py-ai/`: Python scripts for audience/persona/tag workflows and load testing.

## Development Notes

- Keep migrations reversible with a matching `_down.sql` file.
- Use repository interfaces and business-flow methods instead of putting data access in handlers.
- Keep admin endpoints behind both admin authentication and authorization middleware.
- Preserve the shared response shape for new JSON APIs.
- Regenerate Swagger when handler annotations change.
- Disable schedulers locally unless you are testing provider execution.
