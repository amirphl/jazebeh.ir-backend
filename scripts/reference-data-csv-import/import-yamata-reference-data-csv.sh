#!/usr/bin/env bash

# Stream the three campaign reference-data CSVs through one psql session.
# Tags and src_reference are upserted; src_layer_all_stats is atomically
# replaced because it has no stable database key.
#
# Usage:
#   import-yamata-reference-data-csv.sh STATS_CSV REFERENCE_CSV TAGS_CSV [PROJECT_DIR] --confirm-maintenance-window

set -Eeuo pipefail
umask 077

STATS_ARGUMENT="${1:-}"
REFERENCE_ARGUMENT="${2:-}"
TAGS_ARGUMENT="${3:-}"
PROJECT_DIR="/srv/yamata"
CONFIRMATION=""
readonly POSTGRES_CONTAINER="yamata-postgres-beta"
readonly APP_NAME="yamata-reference-data-csv-import"

log() { printf '[reference-data-csv-import] %s\n' "$*"; }
die() { printf '[reference-data-csv-import] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage:
  import-yamata-reference-data-csv.sh STATS_CSV REFERENCE_CSV TAGS_CSV [PROJECT_DIR] --confirm-maintenance-window

The API and campaign scheduler must remain stopped for the entire run.
EOF
}

[[ -n "$STATS_ARGUMENT" && -n "$REFERENCE_ARGUMENT" && -n "$TAGS_ARGUMENT" ]] || {
    usage >&2
    exit 2
}
shift 3
while (($# > 0)); do
    case "$1" in
        --confirm-maintenance-window) CONFIRMATION="$1" ;;
        --help|-h) usage; exit 0 ;;
        --*) die "Unknown option: $1" ;;
        *) [[ "$PROJECT_DIR" == /srv/yamata ]] || die 'Multiple project directories supplied'; PROJECT_DIR="$1" ;;
    esac
    shift
done

[[ "$CONFIRMATION" == --confirm-maintenance-window ]] || {
    usage >&2
    die 'Refusing to write without --confirm-maintenance-window'
}

STATS_CSV="$(readlink -f -- "$STATS_ARGUMENT")"
REFERENCE_CSV="$(readlink -f -- "$REFERENCE_ARGUMENT")"
TAGS_CSV="$(readlink -f -- "$TAGS_ARGUMENT")"
readonly STATS_CSV REFERENCE_CSV TAGS_CSV PROJECT_DIR

for csv_file in "$STATS_CSV" "$REFERENCE_CSV" "$TAGS_CSV"; do
    [[ -f "$csv_file" && ! -L "$csv_file" && -r "$csv_file" && -s "$csv_file" ]] ||
        die "CSV must be a readable, non-empty regular file: $csv_file"
done
[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"

readonly PREFIX_SQL="$PROJECT_DIR/scripts/reference-data-csv-import/reference-data-csv-import-prefix.sql"
readonly MIDDLE_SQL="$PROJECT_DIR/scripts/reference-data-csv-import/reference-data-csv-import-middle.sql"
readonly TAGS_PREFIX_SQL="$PROJECT_DIR/scripts/reference-data-csv-import/reference-data-csv-import-tags-prefix.sql"
readonly SUFFIX_SQL="$PROJECT_DIR/scripts/reference-data-csv-import/reference-data-csv-import-suffix.sql"
for sql_file in "$PREFIX_SQL" "$MIDDLE_SQL" "$TAGS_PREFIX_SQL" "$SUFFIX_SQL"; do
    [[ -f "$sql_file" ]] || die "Missing SQL stream component: $sql_file"
done

for command_name in awk cat df docker du od readlink tail tr; do
    command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

if docker info >/dev/null 2>&1; then DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then DOCKER=(sudo docker)
else die 'Docker is unavailable or requires an interactive sudo login'
fi
readonly DOCKER

[[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null || true)" == true ]] ||
    die "PostgreSQL container is not running: $POSTGRES_CONTAINER"
for container in yamata-campaign-scheduler-beta yamata-app-beta; do
    if "${DOCKER[@]}" inspect "$container" >/dev/null 2>&1 &&
        [[ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$container")" == true ]]; then
        die "Stop $container before this import and keep it stopped until it completes"
    fi
done

DB_USER="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER)"
DB_NAME="$("${DOCKER[@]}" exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB)"
readonly DB_USER DB_NAME
[[ -n "$DB_USER" && -n "$DB_NAME" ]] || die 'Could not resolve PostgreSQL user/database from container'

psql_scalar() {
    "${DOCKER[@]}" exec -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
        psql -X -At -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" -c "$1"
}
for table in tags src_reference src_layer_all_stats; do
    [[ "$(psql_scalar "SELECT to_regclass('public.$table') IS NOT NULL;")" == t ]] ||
        die "Target table public.$table does not exist"
done
[[ "$(psql_scalar "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'tags' AND column_name IN ('id','name','is_active','created_at','updated_at','display_title','audience_persona','audience_count');")" == 8 ]] ||
    die 'Target tags schema is missing one or more required columns'
[[ "$(psql_scalar "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'src_reference' AND column_name IN ('id','src_address','layer1_category','layer2_category','layer3_category','tag_count');")" == 6 ]] ||
    die 'Target src_reference schema is missing one or more required columns'
[[ "$(psql_scalar "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'src_layer_all_stats' AND column_name IN ('layer1_category','layer2_category','layer3_category','distinct_users','calculated_at','black_users','white_users','pink_users','weak_white','good_white','best_white','weak_black','good_black','best_black','weak_pink','good_pink','best_pink','stat_level','scored_users','p33','p66');")" == 21 ]] ||
    die 'Target src_layer_all_stats schema is missing one or more required columns'
active_imports="$(psql_scalar "SELECT count(*) FROM pg_stat_activity WHERE application_name IN ('yamata-audience-csv-import', '$APP_NAME') AND pid <> pg_backend_pid();")"
[[ "$active_imports" == 0 ]] || die 'Another CSV import is already active'

check_header() {
    local csv_file="$1" expected="$2" actual
    IFS= read -r actual <"$csv_file" || die "Could not read CSV header: $csv_file"
    [[ "$actual" == "$expected" ]] || die "Unexpected CSV header in $csv_file; expected: $expected"
}
check_header "$STATS_CSV" 'layer1_category,layer2_category,layer3_category,distinct_users,calculated_at,black_users,white_users,pink_users,weak_white,good_white,best_white,weak_black,good_black,best_black,weak_pink,good_pink,best_pink,stat_level,scored_users,p33,p66'
check_header "$REFERENCE_CSV" 'id,src_address,layer1_category,layer2_category,layer3_category,tag_count'
check_header "$TAGS_CSV" 'id,name,is_active,created_at,updated_at,display_title,audience_persona,audience_count'

for csv_file in "$STATS_CSV" "$REFERENCE_CSV" "$TAGS_CSV"; do
    log "CSV: $csv_file ($(du -h -- "$csv_file" | awk '{print $1}'))"
done
log 'Starting one session: stage -> validate -> atomic merge -> vacuum/analyze'

stream_csv() {
    local csv_file="$1" last_byte
    cat -- "$csv_file"
    last_byte="$(tail -c 1 -- "$csv_file" | od -An -t x1 | tr -d '[:space:]')"
    [[ "$last_byte" == 0a ]] || printf '\n'
    printf '\\.\n'
}

stream_import_input() {
    cat -- "$PREFIX_SQL"
    stream_csv "$STATS_CSV"
    cat -- "$MIDDLE_SQL"
    stream_csv "$REFERENCE_CSV"
    cat -- "$TAGS_PREFIX_SQL"
    stream_csv "$TAGS_CSV"
    cat -- "$SUFFIX_SQL"
}

set +e
stream_import_input | "${DOCKER[@]}" exec -i -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
    psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME"
pipeline_status=("${PIPESTATUS[@]}")
set -e
(( pipeline_status[0] == 0 )) || die "Failed while streaming SQL/CSV into psql (status ${pipeline_status[0]})"
(( pipeline_status[1] == 0 )) || die "psql import failed (status ${pipeline_status[1]}); inspect this unit's journal"

log 'Import completed successfully'
