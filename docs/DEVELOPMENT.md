# Local development

Install PostgreSQL and Redis yourself and create a dedicated empty PostgreSQL database and user. Do not use a production database. Redis may use a password; put a percent-encoded Redis URL in the configuration. On Ubuntu 24.04+ and macOS 14+, run:

```sh
./scripts/configure-development-env.sh
./scripts/setup-development.sh
```

The first script creates `.env.development` when needed and opens it in `$EDITOR`; supply database connection settings and `CACHE_REDIS_URL`. The setup script checks PostgreSQL and Redis, downloads Go modules, applies migrations to an empty database, and installs a VS Code launch configuration if one does not already exist. If Go is missing, setup installs it with apt on Ubuntu or Homebrew on macOS; on macOS it installs Homebrew first if needed. These installations can request administrator approval. PostgreSQL and Redis are not installed or started by these scripts. Both scripts have Bash shebangs and can be invoked directly from Bash or Fish.

Select **Yamata development** in VS Code and press Run. If an existing `.vscode/launch.json` was retained, add the configuration from `config/development.launch.json`. The API listens at `http://127.0.0.1:8080`; point the frontend there. The process owns all schedulers and creates a local bot using the configured `BOT_USERNAME` and `BOT_PASSWORD`. Use `./scripts/verify-development.sh` to check the dependencies and, when running, the health endpoint.

The default browser frontend origins are `http://localhost:8081` and `http://127.0.0.1:8081`. Existing `.env.development` files are not overwritten: update `CORS_ALLOWED_ORIGINS` there if they still contain the old port. Mock payment callbacks require the owning customer's bearer token; the path and reservation invoice numbers must match. Production gateway callbacks remain unauthenticated and use real gateway verification.

Setup creates the configured `MOCK_SEED_ADMIN_USERNAME` administrator once with `superadmin`; set `ADMIN_2FA_MOBILES=devadmin:989100000000` (matching the default username) to log in. Change `MOCK_SEED_ADMIN_PASSWORD` before the first seed if desired; retries intentionally do not rotate an existing password. `bootstrap-mock-admin.sh` remains available only for an empty mock database. Login still requires the captcha and a mock OTP from the private `data/mock-otp.jsonl` outbox. The test mobile must use the `989...` format. Do not use production credentials or data.

The development migration tracker refuses an existing untracked database and records an incomplete migration before applying it. Once the underlying SQL or data problem has been repaired, rerun setup: it retries that incomplete migration before proceeding to later files. Do not delete or mark the tracker complete to bypass a failed migration. Setup inserts missing mock fixtures without overwriting existing rows: the three account types, one mock admin and bot, 100 tags (10 inactive), 100,000 deterministic audience profiles, sender lines, prices, hierarchy references, segment factors, and sequence counters. An existing bot's password is not rotated; `BOT_PASSWORD` must match the stored password when campaign execution is enabled. Store runtime files under `data/`, and keep `.env.development` private. Simulated OTP messages are written to the mode-0600 `data/mock-otp.jsonl` outbox so a developer can complete sign-in; this file contains sensitive codes and must not be shared.

Migration output can include PostgreSQL `NOTICE` messages for existing extensions, absent objects removed with `IF EXISTS`, and indexes already created by unique constraints; these are not migration failures.

## PostgreSQL extensions and query statistics

Migration 0131 creates `pg_stat_statements` and `pgstattuple`. If setup reports permission denied, create them in the development database using a PostgreSQL superuser. For example, if the current macOS username is a PostgreSQL superuser and the database uses the default local address:

```sh
psql -h 127.0.0.1 -p 5432 -U "$(whoami)" -d yamata_development \
  -c 'CREATE EXTENSION IF NOT EXISTS pg_stat_statements;'
psql -h 127.0.0.1 -p 5432 -U "$(whoami)" -d yamata_development \
  -c 'CREATE EXTENSION IF NOT EXISTS pgstattuple;'
./scripts/setup-development.sh
```

Use the actual database host, port, name and PostgreSQL administrator role if they differ; the macOS username is not necessarily a PostgreSQL superuser. If an extension is unavailable, install the matching PostgreSQL extension package for the server version first. Setup retries the incomplete migration; do not modify the tracker.

Creating `pg_stat_statements` allows the migration to proceed, but querying it also requires its library to be preloaded at PostgreSQL server startup. If `SELECT count(*) FROM pg_stat_statements;` reports that it must be loaded via `shared_preload_libraries`, inspect the active server configuration as an administrator:

```sh
psql -h 127.0.0.1 -p 5432 -U "$(whoami)" -d yamata_development \
  -c 'SHOW config_file;' -c 'SHOW shared_preload_libraries;'
```

Add `pg_stat_statements` to `shared_preload_libraries` in the active PostgreSQL configuration, preserving any other listed libraries (for example, `shared_preload_libraries = 'existing_library,pg_stat_statements'`). Restart the PostgreSQL server using the method appropriate to its installation; a configuration reload alone is insufficient. This server-wide setting affects all databases on that instance. Verify the setting and query the view again after the restart:

```sh
psql -h 127.0.0.1 -p 5432 -U yamata_development -d yamata_development \
  -c 'SHOW shared_preload_libraries;' -c 'SELECT count(*) FROM pg_stat_statements;'
```

The count can be zero. Preloading is needed for query statistics, not for migration 0131 to create the extension; `pgstattuple` does not require this preload.

Audience-spec Redis entries are populated from PostgreSQL on the first request for each platform and expire after five minutes. An empty Redis cache immediately after setup is expected; the application rebuilds it on demand.

In mock mode, provider sends and status lookups, certificate alerts, short-link publication, smart-tag evaluation and payments are simulated. Delivery outcomes are stable per message and approximately 20% fail. Mock delivery still changes campaign records, statistics, refunds and wallet data. SMS log entries intentionally omit message bodies and OTPs. Ticket attachment downloads from remote URLs are disabled.

The backend's default Go HTTP transport blocks remote destinations in development. Staging additionally permits its own host and internal application/GlitchTip names. This is defense in depth, not a firewall: a custom transport or another process can bypass it, so use OS/container egress restrictions when validating complete isolation.

The Atipay backend returns a mock token and accepts a simulated callback; it cannot control a separately built frontend that may redirect the browser to the real gateway. The frontend must recognize mock tokens and use a local mock-payment flow before end-to-end payment testing. The OxaPay mock returns a local wallet path, but does not emulate a complete gateway UI.
