#!/usr/bin/env bash
# Run daily as root on the dedicated host. The runtime service deliberately
# lacks DDL privileges; this script uses the private PostgreSQL administrator.
set -Eeuo pipefail

readonly COMPOSE_FILE='/opt/external-shortlink/app/deploy/postgres.compose.yml'
readonly ENV_FILE='/etc/external-shortlink-postgres.env'
readonly SERVICE_ENV_FILE='/etc/external-shortlink.env'
readonly MAINTENANCE_SQL='/opt/external-shortlink/app/maintain-click-partitions.sql'
# The least-privilege role the service connects as. Privileges are reconciled in
# postgres-init at deploy time and re-asserted by the maintenance SQL, so a
# missing grant is repaired without a deployment.
readonly RUNTIME_ROLE='external_shortlink_runtime'
# The number of days a partition is pre-created ahead of today. Kept in step with
# the deployment schema's own window.
readonly PRECREATE_DAYS=14

on_error() {
    local status="$?"
    logger -p daemon.err -t external-shortlink-partitions "partition maintenance failed exit_code=$status"
    exit "$status"
}
trap on_error ERR

# EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS is the single retention setting for the
# click store: the service uses it for legacy rows, and the maintenance SQL uses
# it for partitions and deduplication rows. Read it from the service environment
# instead of defaulting, so the two can never silently disagree.
retention_days="$(sed -n 's/^EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS=//p' "$SERVICE_ENV_FILE" | tail -n 1 | tr -d '[:space:]')"
if [[ ! "$retention_days" =~ ^[0-9]+$ ]] || (( retention_days < 1 )); then
    logger -p daemon.err -t external-shortlink-partitions \
        "partition maintenance needs a positive EXTERNAL_SHORTLINK_ACK_RETENTION_DAYS in $SERVICE_ENV_FILE, got '${retention_days}'"
    exit 1
fi

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

logger -p daemon.info -t external-shortlink-partitions \
    "partition maintenance started retention_days=$retention_days precreate_days=$PRECREATE_DAYS runtime_role=$RUNTIME_ROLE"
docker compose -f "$COMPOSE_FILE" exec -T postgres sh -ceu '
PGPASSWORD="$POSTGRES_PASSWORD" psql --set=ON_ERROR_STOP=1 \
    --host 127.0.0.1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    --set=retention_days="$1" --set=precreate_days="$2" --set=runtime_role="$3" \
    -f /opt/external-shortlink-maintain-partitions.sql
' maintenance "$retention_days" "$PRECREATE_DAYS" "$RUNTIME_ROLE"
logger -p daemon.info -t external-shortlink-partitions 'partition maintenance completed'
