# Production configuration

This document describes the configuration consumed by the current application
and beta Compose stack. The complete variable inventory is
[`env.template`](../env.template); parsing, defaults, and startup validation live
in [`config/production.go`](../config/production.go).

## Configuration files

The standalone Go process reads environment variables and then fills unset
values from a repository-root `.env` file. Existing process variables win over
`.env` values.

The production scripts use a separate `.env.beta` file. They parse it as dotenv
data through `scripts/load_dotenv.py`; they do not execute it as shell code.
The file must be a regular, non-symlink file and is changed to mode `0600`.
Use one `KEY=VALUE` entry per line. Quoted values and comments are supported,
but shell expansion and command substitution are not.

To prepare a new file:

```bash
cp env.template .env.beta
chmod 600 .env.beta
```

Replace every provisioning placeholder such as `$domain`, `$db_password`, and
`$jwt_secret`. The strict production loader rejects unresolved placeholders.
Do not commit `.env`, `.env.beta`, credentials, private keys, or provider
tokens.

## Required startup values

`LoadProductionConfig` validates these values on every startup:

- Database: non-empty `DB_HOST`, `DB_NAME`, `DB_USER`, and `DB_PASSWORD`, with
  `DB_PORT` in the valid TCP port range.
- JWT: `JWT_SECRET_KEY` of at least 32 characters, positive access/refresh
  token TTLs, and non-empty issuer and audience. A secret is currently required
  even when the optional RSA fields are populated.
- System identities: valid UUIDs in `SYSTEM_USER_UUID`, `TAX_USER_UUID`,
  `SYSTEM_WALLET_UUID`, and `TAX_WALLET_UUID`; non-empty system/tax mobiles and
  emails; and a valid `SYSTEM_SHEBA_NUMBER`.
- Crypto: no provider credential is needed when `CRYPTO_ENABLED=false`. When
  enabled, `CRYPTO_DEFAULT_PLATFORM=oxapay` and non-empty `OXA_BASE_URL` and
  `OXA_API_KEY` are required.
- Email: because `EMAIL_HOST` defaults to a non-empty SMTP host, username,
  password, and sender address must normally be configured.
- SMS: `mock` needs no external credential; `payamsms` needs source number,
  username, and password; other provider domains need an API key and source
  number.
- Cache: a Redis URL is required when Redis caching is enabled.
- Smart-tag evaluation: when enabled, a model and valid scheduler, batching,
  validation, retry, rate-limit, and timeout settings are required. Both prompt
  files at the repository root must also be readable.

Invalid numeric, Boolean, duration, or optional floating-point text currently
falls back to the code default rather than producing a parse error. Use Go
duration syntax (`23ms`, `30s`, `15m`, `24h`) and verify effective container
values after deployment.

## Variable groups

| Group | Prefixes or principal variables | Notes |
|---|---|---|
| Build/runtime identity | `APP_ENV`, `VERSION`, `COMMIT_HASH`, `BUILD_TIME` | Swagger routes exist only for `development` and `local`. |
| PostgreSQL | `DB_*`, `POSTGRES_SHM_SIZE` | Compose forces the app host to `postgres-beta`; PostgreSQL 15 uses a configurable shared-memory mount. |
| HTTP server | `SERVER_*` | The API defaults to port 8080; the metrics listener is separate. |
| JWT/authentication | `JWT_*`, `PASSWORD_*`, `SESSION_*` | See the required-value caveat above. |
| Edge/security | `TLS_*`, `HSTS_*`, `CORS_*`, rate-limit and header variables | Several values are parsed but the current Fiber middleware and rendered nginx template contain their own settings; see “Runtime caveats.” |
| Logging/observability | `LOG_*`, `METRICS_*`, `SENTRY_*` | Metrics default to port 9090 and path `/metrics`. |
| Redis/cache | `CACHE_*`, `REDIS_PASSWORD` | Compose injects `redis://redis-beta:6379` for the app. |
| Deployment | `DOMAIN`, `API_DOMAIN`, `MONITORING_DOMAIN`, `SENTRY_*_DOMAIN`, `PGADMIN_*`, `CERTBOT_EMAIL`, `GRAFANA_ADMIN_PASSWORD`, backup variables | The deployment script renders nginx from the explicit domain argument. pgAdmin credential paths refer to protected host files; they are not credential values. |
| Business identities | `ADMIN_*`, `SYSTEM_*`, `TAX_*` | UUIDs must match the database identities and wallets. |
| Messaging | `SMS_*`, `PAYAM_SMS_*`, `BALE_*`, `RUBIKA_*`, `SPLUS_*`, `MESSAGE_*` | Bale provider behavior is documented in [`bale.md`](bale.md). |
| Payments | `ATIPAY_*`, `CRYPTO_*`, `OXA_*` | Set `CRYPTO_ENABLED=false` to disable crypto payments; only OxaPay is accepted when enabled. |
| Bot/schedulers | `BOT_*`, `CAMPAIGN_*`, `SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED`, `SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED`, `TAG_TEST_PERFORMANCE_SCHEDULER_*` | Production deliberately splits API-owned and campaign-execution workers. |
| Smart-tag evaluation | `SMART_TAG_EVALUATION_*`, `OPENAI_API_KEY`, `IR_HTTPS_PROXY` | The API owns these jobs in the current production topology. |
| Certificate monitor | `CERT_ALERT_*`, `DOMAIN_MONITOR_*` | Monitoring checks and alerts; it does not issue or renew certificates. |

Comma-separated values are used for string slices such as origins, supported
coins, admin mobiles, API keys, and IP lists. `ADMIN_2FA_MOBILES` is a
comma-separated `mobile:value` map.

## Production scheduler ownership

The checked-in production workflow requires the following `.env.beta` values:

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

`scripts/deploy-campaign-scheduler-beta.sh` derives a private worker from the
running API container and overrides those responsibilities:

| Process | Campaign execution | Exact capacity | Test sampling | Tag Test reports | Smart-tag evaluation |
|---|---:|---:|---:|---:|---:|
| `yamata-app-beta` | off | on | on | on | on |
| `yamata-campaign-scheduler-beta` | on | off | off | off | off |

The capacity and Test-sampling switches are independent. When the sampling
variable is omitted, it inherits the capacity switch for backward
compatibility; set it explicitly in production.

This prevents two processes from claiming the same job family. Run
`scripts/deploy-production-beta.sh` after any image or environment change;
restarting a container does not reload its environment.

## Smart-tag prompt and API-key handling

When `SMART_TAG_EVALUATION_ENABLED=true`, these repository-root files are
required and bind-mounted read-only by Compose:

```text
SMART_TAG_EVALUATION_PERSONA_ANALYSIS_SYSTEM_PROMPT
SMART_TAG_EVALUATION_TAG_SCORING_SYSTEM_PROMPT
```

`SMART_TAG_EVALUATION_OPENAI_API_KEY_ENV` names the environment variable that
contains the API key and defaults to `OPENAI_API_KEY`. Configure the named
variable, not the prompt files, with the credential.

## Runtime caveats

- TLS terminates at nginx in the beta stack; `TLS_ENABLED=false` is appropriate
  for the internal HTTP hop. Certificate paths in the nginx template must
  already exist under `/etc/letsencrypt`.
- Fiber currently hard-codes its CORS allowlist, security headers, global limit
  of 2,000 requests/minute, and auth limit of 20 requests/minute in
  `app/router/routes.go`. Nginx defines separate per-IP zones of 100 API
  requests/minute, 5 auth requests/minute, and 200 general requests/minute in
  `docker/nginx/nginx.conf`. Do not assume every parsed `CORS_*`, header, or
  rate-limit variable changes runtime middleware.
- `SERVER_ENABLE_COMPRESSION` and its level are parsed, but the current router
  always installs best-speed compression. Similar config-to-runtime gaps should
  be checked against code before an operational change.
- The app’s default body limit is 100 MiB. Nginx separately allows 100 MiB for
  media and bot audience uploads and 50 MiB for admin short-link CSV uploads.
- The public health endpoint is `/api/v1/health`; nginx also maps `/health` to
  it on the main domain.
- pgAdmin is served only at `https://pg.<domain>:14433`, behind Nginx Basic Auth
  and pgAdmin internal authentication. Nginx binds that port only to
  `PGADMIN_LISTEN_BIND_IP`, the selected host interface address. Its password
  and htpasswd sources are Docker secrets referenced by protected, Git-ignored
  files in the checkout's `.secrets` directory. Follow
  [PGADMIN_DEPLOYMENT.md](PGADMIN_DEPLOYMENT.md) for the required permissions,
  firewall rule, certificate SAN/wildcard check, and credential rotation rules.

## Verification

Validate configuration and behavior before cutover:

```bash
go test ./config ./app/router ./app/middleware
docker compose --env-file .env.beta -f docker-compose.beta.yml config --quiet
./scripts/deploy-production-beta.sh --domain example.com
./scripts/check-yamata-production.sh /srv/yamata
curl --fail https://example.com/api/v1/health
```

Use [`PRODUCTION_DEPLOYMENT.md`](PRODUCTION_DEPLOYMENT.md) for deployment and
[`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md) for a server
migration or restored production database.
