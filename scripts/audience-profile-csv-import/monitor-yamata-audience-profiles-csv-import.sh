#!/usr/bin/env bash

# Print a point-in-time systemd and PostgreSQL status for the audience CSV job.
# Run repeatedly (for example: watch -n 5 sudo ./scripts/monitor-... /srv/yamata UNIT).
# Usage:
#   monitor-yamata-audience-profiles-csv-import.sh [PROJECT_DIR] [UNIT_NAME]

set -Eeuo pipefail

readonly PROJECT_DIR="${1:-/srv/yamata}"
readonly UNIT_NAME="${2:-}"
readonly POSTGRES_CONTAINER="yamata-postgres-beta"
readonly APP_NAME="yamata-audience-csv-import"

die() {
    printf '[audience-csv-import-monitor] ERROR: %s\n' "$*" >&2
    exit 1
}

[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"

if docker info >/dev/null 2>&1; then
    DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
    DOCKER=(sudo docker)
else
    die 'Docker is unavailable or requires an interactive sudo login'
fi
readonly DOCKER

[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null || true)" == true ]] ||
    die "PostgreSQL container is not running: $POSTGRES_CONTAINER"

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME

run_sql() {
    "${DOCKER[@]}" exec "$POSTGRES_CONTAINER" \
        psql -X -v ON_ERROR_STOP=1 -P pager=off -U "$DB_USER" -d "$DB_NAME" -c "$1"
}

printf '=== %s ===\n' "$(date -u --iso-8601=seconds)"

if [[ -n "$UNIT_NAME" ]]; then
    if [[ $EUID -eq 0 ]]; then
        systemctl show "$UNIT_NAME" -p ActiveState -p SubState -p Result -p ExecMainStatus || true
    elif command -v sudo >/dev/null 2>&1; then
        sudo systemctl show "$UNIT_NAME" -p ActiveState -p SubState -p Result -p ExecMainStatus || true
    fi
fi

printf '\n=== import backend ===\n'
run_sql "
SELECT
    pid,
    state,
    wait_event_type,
    wait_event,
    clock_timestamp() - query_start AS command_age,
    left(query, 180) AS query
FROM pg_stat_activity
WHERE application_name = '$APP_NAME'
ORDER BY query_start;"

printf '\n=== COPY progress (available only during staging) ===\n'
run_sql "
SELECT
    activity.pid,
    progress.command,
    progress.type,
    progress.tuples_processed,
    progress.bytes_processed,
    progress.bytes_total,
    CASE WHEN progress.bytes_total > 0
         THEN round(100.0 * progress.bytes_processed / progress.bytes_total, 1)
    END AS percent_complete
FROM pg_stat_progress_copy AS progress
JOIN pg_stat_activity AS activity USING (pid)
WHERE activity.application_name = '$APP_NAME';"

printf '\n=== blockers (must be empty) ===\n'
run_sql "
SELECT
    waiting.pid AS waiting_pid,
    blocking.pid AS blocking_pid,
    blocking.usename AS blocking_user,
    clock_timestamp() - blocking.query_start AS blocking_query_age,
    left(blocking.query, 180) AS blocking_query
FROM pg_stat_activity AS waiting
JOIN LATERAL unnest(pg_blocking_pids(waiting.pid)) AS blocker(pid) ON true
JOIN pg_stat_activity AS blocking ON blocking.pid = blocker.pid
WHERE waiting.application_name = '$APP_NAME';"

printf '\n=== target table statistics ===\n'
run_sql "
SELECT
    n_live_tup,
    n_dead_tup,
    last_analyze,
    last_autoanalyze,
    last_vacuum,
    last_autovacuum
FROM pg_stat_all_tables
WHERE relid = 'public.audience_profiles'::regclass;"
