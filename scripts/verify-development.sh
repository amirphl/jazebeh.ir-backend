#!/usr/bin/env bash
set -Eeuo pipefail
set +x
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
source "$script_dir/load-yamata-env.sh"
load_yamata_env_file "$root/.env.development"
[[ "${APP_ENV:-}" == development && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Expected development mock mode' >&2
	exit 1
}
source "$script_dir/guard-environment.sh"
reject_production_like_target "$DB_NAME" "$DB_HOST"
export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGDATABASE="$DB_NAME" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD" PGSSLMODE="${DB_SSL_MODE:-disable}"
psql -X -v ON_ERROR_STOP=1 -Atc 'SELECT count(*) FROM yamata_development_migrations WHERE NOT complete' | grep -qx 0
redis-cli -u "$CACHE_REDIS_URL" ping | grep -qx PONG
if curl --noproxy '*' -fsS "http://127.0.0.1:${SERVER_PORT:-8080}/api/v1/health" >/dev/null; then
	echo 'PostgreSQL, Redis, migration tracker and backend health checks passed.'
else
	echo 'PostgreSQL, Redis and migration tracker passed; start the backend from VS Code to check its health.'
fi
