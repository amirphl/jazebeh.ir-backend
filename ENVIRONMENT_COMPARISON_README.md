# Development, Staging, and Production Comparison

This is the environment decision matrix for Yamata no Orochi. It compares the
supported, effective configurations—not merely the variables that happen to be
present in an env file. When this document conflicts with implementation, use
the source-of-truth references in [docs/README.md](docs/README.md), especially
`config/production.go`, `main.go`, `docker-compose.beta.yml`, and the deploy
scripts.

## At a glance

| Perspective | Development | Staging | Production |
|---|---|---|---|
| Purpose | Individual workstation development and debugging | Isolated pre-release integration/acceptance environment | Live customer service |
| Required identity | `APP_ENV=development`, `EXTERNAL_SERVICES_MODE=mock` | `APP_ENV=staging`, `EXTERNAL_SERVICES_MODE=mock` | `APP_ENV=production`, `EXTERNAL_SERVICES_MODE=live` |
| Canonical env file | `.env.development` (mode `0600`) | `.env.staging` (mode `0600`) | `.env.beta` (mode `0600`) |
| Runtime shape | One locally run Go process plus locally installed PostgreSQL and Redis | Separate Docker Compose project (`yamata-staging`) | production Compose stack plus an isolated campaign-worker container |
| External integrations | Simulated | Simulated and network-isolated | Real providers and credentials |
| Data | Local, dedicated, non-production data only | Separate synthetic/staging data only; never production data | Authoritative live data |
| Public edge | Direct loopback HTTP by default | TLS nginx at `staging.jazebeh.ir` | TLS nginx at the configured production domain |
| Release risk | Low; disposable | Medium; validates an artifact/configuration without live effects | High; changes can affect customers, money, and provider traffic |

The mode pair is intentional and validated by setup/deploy scripts. Do not mix
`development` or `staging` with `live`, or `production` with `mock`.

## Complete operating matrix

| Area | Development | Staging | Production |
|---|---|---|---|
| Host and ownership | Developer-controlled macOS 14+ or Ubuntu 24.04+ workstation. PostgreSQL and Redis are installed and started by the developer. | Separate Ubuntu 24.04 server. Compose project, containers, volumes, DB, Redis, certificates, credentials, and data must be independent from production. | Production server/stack operated through the production Compose workflow. Host certificates, secrets, persistence, firewall, and backups are production operational assets. |
| Start/release path | `./scripts/configure-development-env.sh`, `./scripts/setup-development.sh`, then the **Yamata development** VS Code launch configuration (or equivalent local `go run`). | `./scripts/prepare-staging-host.sh`, provide the frontend image, then `./scripts/deploy-staging-beta.sh --domain staging.jazebeh.ir`. | `./scripts/deploy-production-beta.sh --domain <production-domain>` after preparing `.env.beta`; this deploys the API then recreates the isolated scheduler. |
| Configuration source and precedence | `.env.development`, created from `config/development.env.example`; local process environment may override dotenv values. | `.env.staging`, created from `config/staging.env.example`; base Compose file plus `docker-compose.staging.yml` overlay force the staging-safe values. | `.env.beta`, created from `env.template`; deploy scripts parse dotenv rather than execute shell code and reject unresolved production placeholders. |
| Application identity | `development` / `mock`; development JWT issuer/audience and local test identity values are supplied by the example. | `staging` / `mock`; staging-only secrets, bot identity, system identities, and DSN if error reporting is enabled. | `production` / `live`; unique, strong credentials and real system/tax/wallet identities are required at startup. |
| Database | Dedicated local database, conventionally named `*_development`, normally on loopback; local Redis URL. Never aim it at a production-like DB/host. | Docker PostgreSQL named `yamata_staging`, isolated volume, Redis container, smaller PostgreSQL configuration (512 MiB shared memory; 1.5 GiB / 2 CPU DB limit). | Docker PostgreSQL/Redis persistent volumes with production capacity configuration (default PostgreSQL shared-memory cap 48 GiB). Database and cache are internal Compose services. |
| Migration process | `migrate-development.sh` requires mock development mode, an empty untracked DB on first run, a `yamata_development_migrations` tracker, `*_development` database name, and local host unless explicitly approved. | `migrate-staging.sh` requires mock staging mode, PostgreSQL container ownership by `yamata-staging`, an empty untracked DB on first run, and a `yamata_staging_migrations` tracker. | Use the production migration/deployment runbooks and aggregate migration guidance. Production migration/restore is an operational procedure, not either non-production tracker. |
| Data lifecycle | Disposable developer data; mock operations still write campaign, wallet, refund, and status state. | Back up before upgrades; deployment does not delete volumes. Never copy production data, keys, DSNs, or customer data into staging. | Retained customer and operational data. Backup container creates validated PostgreSQL dumps every two days at 00:00 UTC, retaining the newest three policy-managed dumps; restore/cutover needs explicit production confirmation. |
| API exposure | `127.0.0.1:8080` by example; direct HTTP health at `/api/v1/health`. | Nginx exposes TLS routes at `https://staging.jazebeh.ir`: frontend `/`, API `/api/`, short links `/s/`, Grafana `/grafana/`, GlitchTip `/sentry/`. | Nginx publishes ports 80/443 for the configured production domain; it terminates TLS and proxies internally to the API. `/health` maps to `/api/v1/health`. |
| Documentation surface | Swagger/docs are enabled: `/api/v1/docs`, `/api/v1/swagger.json`, `/swagger`, `/swagger-standalone`, and local Swagger assets. | Development-only Swagger routes are not enabled. | Development-only Swagger routes are not enabled. |
| TLS and proxy trust | TLS off in the sample; no reverse proxy required. | TLS terminates at nginx; backend uses internal HTTP and trusts the static nginx address `172.30.0.30`. Certificate must cover `staging.jazebeh.ir`. | TLS terminates at nginx; backend uses internal HTTP. Valid certificate material must exist under `/etc/letsencrypt`; internal app TLS remains off in the provided topology. |
| Network / egress | Default Go HTTP transport blocks remote destinations. This is defense in depth, not a firewall; custom transports or other processes can bypass it. | Compose application and pgAdmin edge networks are `internal: true`; deployment checks that app and campaign-scheduler cannot reach a public IP. Internal service names remain available. | Normal bridge application network can reach required external services. Egress must be managed as a production network/security policy; providers, SMTP, OpenAI, monitoring, and notifications can be live. |
| External providers | Mock OTP, SMS and campaign delivery/status, certificate alerts, payments, crypto, short-link publication, and smart-tag evaluation. Mock delivery is deterministic per message and roughly 20% fails. | Same mock provider behavior, using isolated state. Backend calls must remain internal; the separately supplied frontend must handle mock payment tokens rather than redirecting to a real gateway. | Live SMS/messaging, payment, crypto, email, bot, short-link, and smart-tag integrations as configured. Real credentials and provider validation are required; production tools additionally require their explicit confirmation flags. |
| Payment/callback behavior | Mock callbacks require the owning customer's bearer token and matching reservation/path. | Mock callbacks and mock frontend flow; no real gateway navigation. | Gateway callbacks use production verification and can be unauthenticated at the edge where required by the provider. |
| OTP/admin bootstrap | OTPs go to private `data/mock-otp.jsonl` (mode 0600), not logs. Bootstrap with `bootstrap-mock-admin.sh --environment development ...`; captcha and mock OTP still apply. | OTPs are at `/data/mock-otp.jsonl` in `yamata-app-beta` (mode 0600); only trusted operators should read it. Bootstrap uses `--environment staging`; staging-only mobile/password required. | Use approved production administration and identity procedures; do not use mock bootstrap identities or OTP outboxes. |
| Scheduler topology | One local process owns enabled workers. The development example enables campaign execution, smart-targeting workers, tag testing, smart-tag evaluation, audience reports, and mock integrations with conservative concurrency; action-file processing is off by default. | Two containers: API has campaign execution **off** and owns API/data workers; campaign scheduler has execution **on**, its own internal metrics port 9091, and the other scheduler families disabled. | Two containers: API has campaign execution **off** and owns capacity/test/execution calculation, reports, action files, refunds, smart tags, and short-link synchronization; isolated campaign scheduler has campaign execution **on** and those other families **off**. |
| Worker duplication protection | Single process avoids cross-container duplication. | Compose environment overrides explicitly partition work; scheduler waits for healthy API. | `deploy-production-beta.sh` validates the ownership switches and recreates both processes. A container restart alone does not load changed environment values. |
| Mock/live feature defaults | Development example enables crypto, external short links, Candoo behavior, and mock smart-tag model to exercise flows without live egress. | Staging overlay enables mock Candoo/short-link flows and mock prompts/models; application concurrency is capped for the small host. | Configuration uses production provider values. `CAMPAIGN_MESSAGE_SEND_MOCK_ENABLED=false`; live behavior depends on valid provider-specific configuration. |
| Logging | Debug-oriented JSON stdout example; access, audit, and security log files disabled by the development example. | Container stdout plus staging observability services; Sentry environment is `staging`. | Structured/rotated app, access, audit, and security logs are configured by `env.template`; Sentry environment/release/server name are production values. |
| Metrics and tracing | Development example disables metrics and leaves Sentry DSN empty. | Prometheus enabled; API metrics and isolated scheduler metrics (internal port 9091) are scraped using `docker/prometheus_staging.yml`; Grafana and optional GlitchTip use staging paths. | Prometheus, Grafana, GlitchTip, PostgreSQL exporter, node exporter, cAdvisor, nginx Sentry forwarder, and certificate monitor are part of the stack. Application metrics default to internal port 9090. |
| Resource/capacity posture | Limited by the developer machine and local service configuration. | Explicit conservative limits for an 8 GiB host (for example API 768 MiB/2 CPUs, scheduler 768 MiB/1 CPU, database 1.5 GiB/2 CPUs); monitor and resize based on load. | Production sizing comes from `env.template`, Compose, and host provisioning; default PostgreSQL shared-memory cap is much larger and no staging overlay limits apply. Capacity changes require production review. |
| Security posture | Local-only interface, development secret, no production credentials/data, remote Go egress blocked by default. | Separate secrets and protected pgAdmin files; internal networks; `no-new-privileges`, read-only/tmpfs hardening inherited/extended from Compose; pgAdmin must bind a real selected host interface—not loopback or `0.0.0.0`. | Unique secrets, provider credentials, firewall/certificate/backup operations, hardened containers, external edge rate limits/headers, and pgAdmin at selected host interface port 14433 behind Nginx Basic Auth plus pgAdmin authentication. |
| pgAdmin | Not supplied by the local workflow. | Separate authenticated proxy at `https://staging.jazebeh.ir:14433/pgadmin/`, bound to the selected non-loopback `PGADMIN_LISTEN_BIND_IP`; provision the three secret files. | Separate proxy exposes `https://pg.<domain>:14433`, bound only to `PGADMIN_LISTEN_BIND_IP`; password and htpasswd inputs are Docker secrets/protected files. |
| Backup/recovery | Developer-owned backups only; safe to rebuild local data when appropriate. | Built-in backup service with staging volumes; take a staging backup before upgrade. | Backup health is monitored; recovery, restore, and migration/cutover are governed by `PRODUCTION_MIGRATION.md` and require maintenance planning/explicit confirmation. |
| Verification | `./scripts/verify-development.sh` validates mode, non-production-like target, migration tracker, PostgreSQL, Redis, and optionally local health. | `verify-staging-observability.sh`, `verify-staging-no-external-egress.sh`, and `verify-build-proxy.sh yamata-no-orochi-staging`; inspect the `yamata-staging` Compose project. | `check-yamata-production.sh` validates topology, environment pairing, image parity, schema, secret hashes, network/certificate assumptions, and worker ownership after deploy/restore. |
| Operational scripts | Setup and migration scripts reject the wrong mode and production-like target. | Deployment rejects a wrong hostname/mode, non-staging container ownership, unsafe pgAdmin bind, missing certificate/required secrets, and production-like database target. | Production-only tools require `APP_ENV=production`; live publishing needs `--execute`, and restore needs `--confirm-production-restore`. |

## Scheduler ownership in detail

The most important topology difference is who may claim scheduled work. The
following represents the intended effective ownership; do not enable the same
job family in both processes.

| Job family | Development local app | Staging API | Staging scheduler | Production API | Production scheduler |
|---|---:|---:|---:|---:|---:|
| Campaign execution/send and delivery-status work | On | Off | On | Off | On |
| Smart-targeting capacity | On | On | Off | On | Off |
| Smart-targeting test sampling | On | On | Off | On | Off |
| Smart-targeting execution calculation | On | On | Off | On | Off |
| Tag test performance | On | On | Off | On | Off |
| Audience-report jobs | On | On | Off | On | Off |
| Bundle action-file jobs | Off by default | On | Off | On | Off |
| Refund reconciliation | Default/configured locally | On | Off | On | Off |
| Smart-tag evaluation | On, mock model | On, mock model | Off | On, live model/API as configured | Off |
| External short-link synchronization | On, mock | On, mock | Off | On, live as configured | Off |

## Configuration differences that are easy to miss

| Topic | Required interpretation |
|---|---|
| Static router vs environment values | CORS, security-header, rate-limit, and compression behavior is static in the router and nginx configuration. `CORS_ALLOWED_ORIGINS` remains configurable for the development router only. |
| Docker overrides | `docker-compose.staging.yml` has higher precedence than `.env.staging` for its forced environment fields. Production Compose likewise injects container hostnames and internal cache URLs; the effective value is not always the literal value in the env file. |
| Prompt files | Development/staging mount checked-in mock prompt files. Production requires two repository-root prompt files mounted read-only when smart-tag evaluation is enabled; the API key is the named environment variable (normally `OPENAI_API_KEY`), not prompt-file content. |
| Frontend artifact | This repository does not build the `yamata-frontend-beta` image. Staging and production deployment both require a separately supplied compatible image. |
| DNS names | `API_DOMAIN` exists in configuration, but the current production nginx template does not necessarily create a separate `api.<domain>` vhost. Validate the rendered nginx configuration rather than assuming it does. |
| Secrets | Example values are fixtures/placeholders only. Every staging and production secret must be unique; production placeholders such as `$domain` and `$jwt_secret` are invalid. Never commit env files, credentials, private keys, OTP outboxes, provider tokens, or protected pgAdmin secret files. |
| Migration recovery | Each non-production tracker records an incomplete migration before executing it. A subsequent setup or deployment retries it before later migrations; inspect and repair the SQL or partial state first. Never delete or mark the tracker complete merely to bypass a failure. |

## Promotion rules

1. Develop only against `.env.development`, local PostgreSQL/Redis, and mock
   data. Verify migrations and local health.
2. Promote a build to the isolated staging host. Use fresh staging secrets and
   data, run the mock end-to-end flows, observe metrics, and prove no public
   egress.
3. Promote the approved artifact/configuration to production through the
   production deployment wrapper. Replace mock credentials/configuration with
   reviewed live values, preserve scheduler ownership, and run the production
   validation check.
4. Never promote databases, caches, `.env` files, secrets, OTP outboxes, or
   staging volumes into production. Promotion is code/image and reviewed
   configuration—not a copy of an environment.

## Primary references

| Concern | Reference |
|---|---|
| Local setup and mock behavior | [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) |
| Staging topology and deployment | [docs/STAGING.md](docs/STAGING.md) and [docker-compose.staging.yml](docker-compose.staging.yml) |
| Production configuration and worker ownership | [docs/PRODUCTION_CONFIGURATION.md](docs/PRODUCTION_CONFIGURATION.md) |
| Production deployment and recovery | [docs/PRODUCTION_DEPLOYMENT.md](docs/PRODUCTION_DEPLOYMENT.md) and [PRODUCTION_MIGRATION.md](PRODUCTION_MIGRATION.md) |
| Full variable inventory | [env.template](env.template), [config/development.env.example](config/development.env.example), and [config/staging.env.example](config/staging.env.example) |
| Effective startup/config parsing | [config/production.go](config/production.go) and [main.go](main.go) |
| Deployment guardrails | [scripts/](scripts/README.md) |
