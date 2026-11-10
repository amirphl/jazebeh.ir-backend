#!/usr/bin/env bash

# Stream a large host CSV through one psql session into a temporary staging
# table, validate it, and atomically upsert audience_profiles.
#
# Usage:
#   import-yamata-audience-profiles-csv.sh CSV_FILE [PROJECT_DIR] [--allow-active-backend] --confirm-maintenance-window

set -Eeuo pipefail
umask 077

CSV_ARGUMENT="${1:-}"
PROJECT_DIR="/srv/yamata"
CONFIRMATION=""
ALLOW_ACTIVE_BACKEND=false
readonly POSTGRES_CONTAINER="yamata-postgres-beta"
readonly APP_NAME="yamata-audience-csv-import"

log() {
    printf '[audience-csv-import] %s\n' "$*"
}

die() {
    printf '[audience-csv-import] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage:
  import-yamata-audience-profiles-csv.sh CSV_FILE [PROJECT_DIR] [--allow-active-backend] --confirm-maintenance-window

The confirmation is required because this is a production write operation.
The campaign scheduler must remain stopped for the entire run. By default the
API must also be stopped; --allow-active-backend keeps it up, but profile
writes will wait behind the import's target-table lock.
EOF
}

[[ -n "$CSV_ARGUMENT" ]] || {
    usage >&2
    exit 2
}

shift
while (($# > 0)); do
    case "$1" in
        --confirm-maintenance-window)
            CONFIRMATION="$1"
            ;;
        --allow-active-backend)
            ALLOW_ACTIVE_BACKEND=true
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        --*)
            die "Unknown option: $1"
            ;;
        *)
            [[ "$PROJECT_DIR" == /srv/yamata ]] || die "Multiple project directories supplied"
            PROJECT_DIR="$1"
            ;;
    esac
    shift
done

[[ "$CONFIRMATION" == --confirm-maintenance-window ]] || {
    usage >&2
    die 'Refusing to write without --confirm-maintenance-window'
}

CSV_FILE="$(readlink -f -- "$CSV_ARGUMENT")"
readonly CSV_FILE
readonly PROJECT_DIR
[[ -f "$CSV_FILE" && ! -L "$CSV_FILE" ]] || die "CSV must be a regular file: $CSV_FILE"
[[ -r "$CSV_FILE" ]] || die "CSV is not readable: $CSV_FILE"
[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"

readonly PREFIX_SQL="$PROJECT_DIR/scripts/audience-profile-csv-import/audience-profile-csv-import-prefix.sql"
readonly SUFFIX_SQL="$PROJECT_DIR/scripts/audience-profile-csv-import/audience-profile-csv-import-suffix.sql"
[[ -f "$PREFIX_SQL" ]] || die "Missing SQL prefix: $PREFIX_SQL"
[[ -f "$SUFFIX_SQL" ]] || die "Missing SQL suffix: $SUFFIX_SQL"
[[ -s "$CSV_FILE" ]] || die "CSV is empty: $CSV_FILE"

expected_header='id,uid,phone_number,tags,color,created_at,updated_at,normalized_score'
IFS= read -r actual_header <"$CSV_FILE" || die "Could not read CSV header: $CSV_FILE"
[[ "$actual_header" == "$expected_header" ]] ||
    die "Unexpected CSV header; expected: $expected_header"

for command_name in awk cat df docker du od readlink tail tr; do
    command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

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

if "${DOCKER[@]}" inspect yamata-campaign-scheduler-beta >/dev/null 2>&1 &&
    [[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-campaign-scheduler-beta)" == true ]]; then
    die 'Stop yamata-campaign-scheduler-beta before this import and keep it stopped until it completes'
fi

if "${DOCKER[@]}" inspect yamata-app-beta >/dev/null 2>&1 &&
    [[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' yamata-app-beta)" == true ]]; then
    [[ "$ALLOW_ACTIVE_BACKEND" == true ]] ||
        die 'Stop yamata-app-beta, or explicitly pass --allow-active-backend'
    log 'WARNING: yamata-app-beta is active; audience-profile writers will wait behind the merge lock'
fi

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME
[[ -n "$DB_USER" && -n "$DB_NAME" ]] || die 'Could not resolve PostgreSQL user/database from container'

psql_scalar() {
    "${DOCKER[@]}" exec -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
        psql -X -At -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" -c "$1"
}

[[ "$(psql_scalar "SELECT to_regclass('public.audience_profiles') IS NOT NULL;")" == t ]] ||
    die 'Target table public.audience_profiles does not exist'
[[ "$(psql_scalar "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'audience_profiles' AND column_name IN ('id','uid','phone_number','tags','color','created_at','updated_at','normalized_score');")" == 8 ]] ||
    die 'Target audience_profiles schema is missing one or more required columns'

active_imports="$(psql_scalar "SELECT count(*) FROM pg_stat_activity WHERE application_name = '$APP_NAME' AND pid <> pg_backend_pid();")"
[[ "$active_imports" == 0 ]] || die 'Another audience CSV import is already active'

data_volume="$("${DOCKER[@]}" volume inspect -f '{{.Mountpoint}}' postgres_data_beta 2>/dev/null || true)"
log "CSV: $CSV_FILE ($(du -h -- "$CSV_FILE" | awk '{print $1}'))"
log "CSV filesystem: $(df -h -- "$CSV_FILE" | awk 'NR == 2 {print $4 " available of " $2}')"
if [[ -n "$data_volume" ]]; then
    log "PostgreSQL volume filesystem: $(df -h -- "$data_volume" | awk 'NR == 2 {print $4 " available of " $2}')"
fi
log 'Starting one session: stage -> validate -> atomic merge -> vacuum/analyze'

stream_import_input() {
    cat -- "$PREFIX_SQL"
    cat -- "$CSV_FILE"

    # psql requires \\. on a fresh line to end COPY FROM STDIN. Do not add a
    # blank CSV record when the source already ends with a newline.
    last_byte="$(tail -c 1 -- "$CSV_FILE" | od -An -t x1 | tr -d '[:space:]')"
    if [[ "$last_byte" != 0a ]]; then
        printf '\n'
    fi
    printf '\\.\n'

    cat -- "$SUFFIX_SQL"
}

set +e
stream_import_input | "${DOCKER[@]}" exec -i -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
    psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME"
pipeline_status=("${PIPESTATUS[@]}")
set -e

if (( pipeline_status[0] != 0 )); then
    die "Failed while streaming SQL/CSV into psql (status ${pipeline_status[0]})"
fi
if (( pipeline_status[1] != 0 )); then
    die "psql import failed (status ${pipeline_status[1]}); inspect this unit's journal"
fi

log 'Import completed successfully'
