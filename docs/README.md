# Yamata no Orochi Documentation

This directory contains the generated OpenAPI output and the deployment, configuration, security, and provider notes for the Jazebeh backend. For the project overview and local setup, start with the [root README](../README.md).

> The current beta production migration and restore source of truth is
> [`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md). For fresh releases,
> use the checked-in Compose deployment workflow documented below.

## Current Implementation

The application is a Go 1.26 service built on Fiber v3, GORM, PostgreSQL, and Redis. It currently provides three authentication surfaces (customer, admin, and bot) and supports:

- SMS, Bale, Rubika, and Soroush Plus campaigns.
- Bundle-based test and execution phases, audience selection, scoring, and campaign grouping.
- Asynchronous bundle smart-tag evaluation through an OpenAI-compatible Responses API.
- Wallets, Atipay, deposit receipts, invoices, crypto-payment providers, and agency discounts.
- Short links and click reporting, media, tickets, pricing, platform settings, admin customer management, and maker-checker access control.
- Prometheus metrics, Grafana dashboards, structured/rotated logs, request IDs, and Sentry-compatible GlitchTip reporting.

The schema head is migration `0133`. The newest schema work adds durable Tag
Test performance reporting and makes Test sampling independently track Bundle
allocation freshness instead of depending on an exact-capacity generation.

## Source of Truth

Use these files when documentation and implementation differ:

| Concern | Canonical source |
|---|---|
| HTTP routes and middleware | [`app/router/routes.go`](../app/router/routes.go) |
| Environment variables and defaults | [`config/production.go`](../config/production.go) and [`env.template`](../env.template) |
| Startup wiring and workers | [`main.go`](../main.go) |
| Database order and execution guidance | [`migrations/README.md`](../migrations/README.md) |
| Beta deployment topology | [`docker-compose.beta.yml`](../docker-compose.beta.yml) |
| Build, test, migration, and Swagger commands | [`Makefile`](../Makefile) |

## Documentation Map

| Document | Purpose |
|---|---|
| [Production deployment](PRODUCTION_DEPLOYMENT.md) | Fresh beta Compose deployment and release operations |
| [Production migration](../PRODUCTION_MIGRATION.md) | Canonical server move, restore, imports, and cutover |
| [Production configuration](PRODUCTION_CONFIGURATION.md) | Current environment parsing, required values, worker ownership, and runtime caveats |
| [Production security checklist](PRODUCTION_SECURITY_CHECKLIST.md) | Implementation-aware host, application, container, and operational review |
| [Multi-server deployment](MULTI_SERVER_DEPLOYMENT.md) | Supported one-stack-per-host model and promotion/cutover guidance |
| [Selective PostgreSQL backup/restore](SELECTIVE_POSTGRES_BACKUP_RESTORE_README.md) | Four-table local dataset export with phone-number anonymization |
| [Jazebeh audience CSV import runbook](JAZEBEH_AUDIENCE_CSV_IMPORT_RUNBOOK.md) | Production procedure for validating, importing, monitoring, and verifying large mixed new/update audience exports |
| [Resumable audience CSV batch import](JAZEBEH_AUDIENCE_CSV_BATCH_IMPORT_RUNBOOK.md) | Nightly 100,000-row atomic audience upserts that retain checkpointed progress while application services remain running |
| [Smart Targeting API](smart-targeting-api.md) | Tag selection, exact capacity, Test sampling, and Execution ordering |
| [Normal-targeting campaign diagnostics](normal-targeting-campaign-diagnostics/) | Reporting and validation queries for standard campaign selection and delivery |
| [Bale integration](bale.md) | Implemented Najva v2/Safir v3 client, batching, uploads, status, and retries |
| [Migration guide](../migrations/README.md) | Current schema head, migration groups, execution, rollback, and known gaps |
| [Pitch and architecture index](../pitch/README.md) | Board-facing and technical Mermaid documents |

Deployment documents use `example.com` and `/srv/yamata` placeholders. Verify
hostnames, image versions, secrets, and service names against
`docker-compose.beta.yml` and `env.template` before applying them.

## OpenAPI and Swagger

The checked-in generated artifacts are:

- `docs.go`
- `swagger.yaml`
- `swagger.json`
- `swagger-ui.html`
- `swagger-ui-standalone.html`
- `swagger-ui-assets/`

Regenerate the Go/OpenAPI artifacts after changing handler annotations:

```bash
make swag
```

`make swag` installs the `swag` CLI when it is missing. Review generated diffs before committing them.

Swagger routes are registered only when `APP_ENV=development` or `APP_ENV=local`:

| Route | Description |
|---|---|
| `GET /api/v1/docs` | API documentation response |
| `GET /api/v1/swagger.json` | Generated OpenAPI JSON |
| `GET /swagger` | Embedded Swagger UI |
| `GET /swagger-standalone` | Standalone Swagger UI |
| `GET /swagger-ui-assets/*` | Local UI assets |

The generated specification can lag behind `app/router/routes.go` until `make swag` is run.

## API Surface

All JSON API routes use the `/api/v1` prefix unless noted.

| Surface | Main route groups |
|---|---|
| Public | `/api/v1/health`, customer/admin/bot login routes, crypto provider callback, `/s/:uid`, `/:uid` |
| Customer | `/campaigns`, `/bundles`, `/wallet`, `/payments`, `/crypto`, `/reports`, `/profile`, `/media`, `/platform-settings`, `/platform-base-prices`, `/segment-price-factors`, `/line-numbers`, `/tickets` |
| Admin | `/admin/campaigns`, `/admin/payments`, `/admin/media`, `/admin/platform-settings`, `/admin/platform-base-prices`, `/admin/segment-price-factors`, `/admin/line-numbers`, `/admin/tickets`, `/admin/short-links`, `/admin/customer-management`, `/admin/access-control` |
| Bot | `/bot/campaigns`, `/bot/short-links`, `/bot/media` |

Admin groups use both admin JWT authentication and the central permission registry. Bot groups use separate bot JWT claims. Customer groups use customer JWT claims.

Bundle endpoints include CRUD plus:

```text
POST /api/v1/bundles/:id/tag-evaluations
GET  /api/v1/bundles/:id/tag-evaluation
GET  /api/v1/bundles/:id/tag-scores
```

## Local Documentation Setup

Create the environment file and configure at least PostgreSQL, JWT, required system identities/wallets, and any enabled integrations:

```bash
cp env.template .env
go mod download
```

Create the database separately, then consult the [migration guide](../migrations/README.md). The aggregate manifests include every numbered migration; run them with `ON_ERROR_STOP=1` so any database error stops immediately.

Start the API:

```bash
make run
```

Check health and open the local documentation:

```bash
curl http://localhost:8080/api/v1/health
```

Then visit `http://localhost:8080/swagger` with `APP_ENV=development` or `APP_ENV=local`.

## Smart-Tag Evaluation

Smart-tag evaluation is off by default. When enabling it:

- Set `SMART_TAG_EVALUATION_ENABLED=true` and configure `SMART_TAG_EVALUATION_OPENAI_*`.
- Set the environment variable named by `SMART_TAG_EVALUATION_OPENAI_API_KEY_ENV` (defaults to `OPENAI_API_KEY`).
- Place both multiline prompt files at the repository root:
  - `SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT`
  - `SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT`
- Enable `SMART_TAG_EVALUATION_SCHEDULER_ENABLED` only when this process should claim queued runs.
- Enable `SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED` only when this process should claim exact Smart Targeting capacity jobs.
- Enable `SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED` only when this process should claim Smart Targeting Test sampling jobs.

The scheduler poll interval, maximum parallel runs, tag batch size, validation strictness, model, reasoning effort, token limit, timeout, retry count, temperature, and optional proxy are configurable through `env.template`.

## Beta Deployment Stack

`docker-compose.beta.yml` currently defines PostgreSQL 15, Redis 8, the Go application, nginx, a PostgreSQL backup container, GlitchTip with its PostgreSQL/Redis dependencies, an nginx Sentry forwarder, certificate monitoring, Prometheus, Grafana, PostgreSQL exporter, node exporter, cAdvisor, and the frontend image. Only nginx publishes host ports (`80` and `443`) in the checked-in compose file.

Use
[`scripts/deploy-production-beta.sh`](../scripts/deploy-production-beta.sh) for
production releases; it wraps the API deployment, recreates the isolated
campaign scheduler, and runs topology checks. There is no checked-in production
Kubernetes manifest or generic `docker-compose.production.yml`.

## Verification

Run the complete Go package test suite:

```bash
go test ./...
```

Run the maintained database/Redis-free CI subset with the race detector:

```bash
make ci-test-unit
```

Other useful checks are `make fmt`, `make vet`, `make lint`, and `make build`. Several older Make targets still point at a non-existent `./tests` package; use the commands above for the maintained suite.
