#!/usr/bin/env fish
#
# Resumable 100,000-row audience_profiles.csv importer.
#
# The --start mode creates a detached systemd unit. The unit runs --run and
# handles one transaction per batch. It deliberately does not stop the API or
# scheduler, so the SQL uses SERIALIZABLE transactions and refuses identity
# conflicts. A target row with a newer updated_at is preserved and reported as
# skipped_newer rather than overwritten by this export. While this is running,
# no other process may insert audience_profiles or consume its ID sequence.

set -g BATCH_SIZE 100000
set -g EXPECTED_HEADER 'id,uid,phone_number,tags,color,created_at,updated_at,normalized_score'
set -g POSTGRES_CONTAINER yamata-postgres-beta
set -g APP_NAME yamata-audience-batch-csv-import

function usage
    printf '%s\n' \
        'Usage:' \
        '  run-yamata-audience-profiles-batched.fish --start CSV_FILE [PROJECT_DIR] [STATE_FILE] [UNIT_NAME]' \
        '  run-yamata-audience-profiles-batched.fish --run   CSV_FILE [PROJECT_DIR] [STATE_FILE]'
end

function log
    printf '[audience-batch-import] %s %s\n' (date -u +%Y-%m-%dT%H:%M:%SZ) "$argv"
end

function die
    log "ERROR: $argv" >&2
    exit 1
end

function in_tehran_window
    set -l hour (env TZ=Asia/Tehran date +%H)
    string match -rq '^(23|0[0-7])$' -- "$hour"
end

function batch_timeout_ms
    # End a batch before 08:00 Tehran time. If there is not at least 30
    # seconds left, do not start it. A timeout rolls back that whole batch.
    set -l hour (env TZ=Asia/Tehran date +%H)
    set -l cutoff
    if test "$hour" = 23
        set cutoff (env TZ=Asia/Tehran date -d 'tomorrow 08:00' +%s)
    else
        set cutoff (env TZ=Asia/Tehran date -d 'today 08:00' +%s)
    end
    set -l now (date +%s)
    math "$cutoff - $now - 30"
end

function write_state --argument-names state_file csv_sha csv_bytes header_sha last_batch next_line completed_rows
    set -l temp "$state_file.tmp."(random)
    begin
        printf 'format=1\n'
        printf 'csv_sha256=%s\n' "$csv_sha"
        printf 'csv_bytes=%s\n' "$csv_bytes"
        printf 'header_sha256=%s\n' "$header_sha"
        printf 'last_completed_batch=%s\n' "$last_batch"
        printf 'next_physical_line=%s\n' "$next_line"
        printf 'completed_rows=%s\n' "$completed_rows"
        printf 'updated_at_utc=%s\n' (date -u +%Y-%m-%dT%H:%M:%SZ)
    end >"$temp"
    chmod 600 "$temp"; or die "Cannot secure state file: $temp"
    mv -f -- "$temp" "$state_file"; or die "Cannot replace state file: $state_file"
end

function state_value --argument-names state_file wanted
    awk -F= -v wanted="$wanted" '$1 == wanted { print substr($0, length(wanted) + 2); exit }' "$state_file"
end

function cleanup_batch_file --on-signal INT --on-signal TERM --on-signal HUP
    if set -q CURRENT_BATCH_FILE; and test -n "$CURRENT_BATCH_FILE"
        rm -f -- "$CURRENT_BATCH_FILE"
    end
    log 'Interrupted; the active database transaction was cancelled and its batch was not marked complete.' >&2
    exit 130
end

if not set -q argv[1]
    usage >&2
    exit 2
end

set -l mode $argv[1]
if not contains -- "$mode" --start --run --locked
    usage >&2
    exit 2
end
set -e argv[1]

if not set -q argv[1]
    usage >&2
    exit 2
end
set -l csv_file (realpath -- "$argv[1]")
set -e argv[1]
set -l project_dir /srv/yamata
if set -q argv[1]
    set project_dir (realpath -- "$argv[1]")
    set -e argv[1]
end
set -l state_file "$project_dir/var/audience-profile-batch-import.state"
if set -q argv[1]
    set state_file (realpath -m -- "$argv[1]")
    set -e argv[1]
end
set -l unit_name ''
if set -q argv[1]
    set unit_name $argv[1]
    set -e argv[1]
end
if test (count $argv) -ne 0
    usage >&2
    exit 2
end

test -f "$csv_file"; and not test -L "$csv_file"; or die "CSV must be a regular file: $csv_file"
test -r "$csv_file"; or die "CSV is not readable: $csv_file"
test -d "$project_dir"; or die "Project directory does not exist: $project_dir"

if test "$mode" = --start
    test (id -u) -eq 0; or die 'Run --start with sudo so the systemd unit has Docker access.'
    in_tehran_window; or die 'Refusing to start outside 23:00-08:00 Asia/Tehran.'
    test -n "$unit_name"; or set unit_name yamata-audience-batch-import-(date -u +%Y%m%dT%H%M%SZ)
    set unit_name (string replace -r '\\.service$' '' -- "$unit_name")
    string match -rq '^[A-Za-z0-9_.@-]+$' -- "$unit_name"; or die "Invalid systemd unit name: $unit_name"
    set -l script_file (realpath -- (status filename))
    command systemd-run \
        --unit="$unit_name" \
        --description='Yamata resumable audience CSV batch import' \
        --property=Type=exec \
        --property=Restart=no \
        --property=TimeoutStartSec=infinity \
        --property=KillSignal=SIGINT \
        "$script_file" --run "$csv_file" "$project_dir" "$state_file"
    or die 'systemd-run failed to start the batch importer'
    log "Started $unit_name.service"
    log "Follow: journalctl -u $unit_name -f -o cat"
    log "Stop safely: systemctl stop $unit_name"
    exit 0
end

test (id -u) -eq 0; or die '--run must execute as root (normally through --start/systemd).'
in_tehran_window; or die 'Refusing to run outside 23:00-08:00 Asia/Tehran.'

for required in awk cat date docker flock head mktemp realpath sha256sum tail wc
    command -q "$required"; or die "Required command is missing: $required"
end

set -l prefix_sql "$project_dir/scripts/audience-profile-csv-import/audience-profile-batch-import-prefix.sql"
set -l suffix_sql "$project_dir/scripts/audience-profile-csv-import/audience-profile-batch-import-suffix.sql"
test -f "$prefix_sql"; or die "Missing SQL prefix: $prefix_sql"
test -f "$suffix_sql"; or die "Missing SQL suffix: $suffix_sql"

docker info >/dev/null 2>&1; or die 'Docker is unavailable.'
test (docker inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null) = true; or die "PostgreSQL container is not running: $POSTGRES_CONTAINER"

set -l db_user (docker exec "$POSTGRES_CONTAINER" printenv POSTGRES_USER); or die 'Could not read POSTGRES_USER.'
set -l db_name (docker exec "$POSTGRES_CONTAINER" printenv POSTGRES_DB); or die 'Could not read POSTGRES_DB.'
test -n "$db_user"; and test -n "$db_name"; or die 'PostgreSQL user/database is empty.'

set -l header (head -n 1 -- "$csv_file")
test "$header" = "$EXPECTED_HEADER"; or die "Unexpected CSV header; expected: $EXPECTED_HEADER"
set -l csv_sha (sha256sum -- "$csv_file" | awk '{print $1}'); or die 'Could not checksum CSV.'
set -l csv_bytes (wc -c <"$csv_file" | string trim)
set -l header_sha (printf '%s' "$header" | sha256sum | awk '{print $1}')

mkdir -p (dirname "$state_file"); or die "Cannot create state directory for: $state_file"
set -l lock_file "$state_file.lock"

# Fish's exec builtin does not support Bash's `exec 9>file` locking idiom.
# Re-exec the worker under flock instead; flock retains the lock until this
# child process exits, including while it is waiting for PostgreSQL.
if test "$mode" = --run
    set -l script_file (realpath -- (status filename))
    command flock -n -E 75 "$lock_file" "$script_file" --locked "$csv_file" "$project_dir" "$state_file"
    set -l flock_status $status
    if test "$flock_status" -eq 75
        die "Another importer owns this state file: $state_file"
    end
    exit "$flock_status"
end

set -l last_batch 0
set -l next_line 2
set -l completed_rows 0
if test -e "$state_file"
    test -f "$state_file"; and not test -L "$state_file"; or die "State file must be a regular file: $state_file"
    test (state_value "$state_file" format) = 1; or die "Unsupported or corrupt state file: $state_file"
    test (state_value "$state_file" csv_sha256) = "$csv_sha"; or die 'CSV checksum differs from the state file; use a new state file.'
    test (state_value "$state_file" csv_bytes) = "$csv_bytes"; or die 'CSV size differs from the state file; use a new state file.'
    test (state_value "$state_file" header_sha256) = "$header_sha"; or die 'CSV header differs from the state file; use a new state file.'
    set last_batch (state_value "$state_file" last_completed_batch)
    set next_line (state_value "$state_file" next_physical_line)
    set completed_rows (state_value "$state_file" completed_rows)
    string match -rq '^[0-9]+$' -- "$last_batch"; or die 'Invalid last_completed_batch in state file.'
    string match -rq '^[0-9]+$' -- "$next_line"; or die 'Invalid next_physical_line in state file.'
    test "$next_line" -ge 2; or die 'Invalid next_physical_line in state file.'
    string match -rq '^[0-9]+$' -- "$completed_rows"; or die 'Invalid completed_rows in state file.'
else
    write_state "$state_file" "$csv_sha" "$csv_bytes" "$header_sha" "$last_batch" "$next_line" "$completed_rows"
end

set -l schema_ok (docker exec -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
    psql -X -At -v ON_ERROR_STOP=1 -U "$db_user" -d "$db_name" -c "
SELECT to_regclass('public.audience_profiles') IS NOT NULL
   AND count(*) = 8
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'audience_profiles'
  AND column_name IN ('id','uid','phone_number','tags','color','created_at','updated_at','normalized_score');")
test "$schema_ok" = t; or die 'Target audience_profiles schema is not supported.'

set -l active_imports (docker exec -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
    psql -X -At -v ON_ERROR_STOP=1 -U "$db_user" -d "$db_name" -c "
SELECT count(*) FROM pg_stat_activity
WHERE application_name = '$APP_NAME' AND pid <> pg_backend_pid();")
test "$active_imports" = 0; or die 'Another batch importer is already active.'

log "Starting from batch="(math "$last_batch + 1")" source_line=$next_line completed_rows=$completed_rows batch_size=$BATCH_SIZE"
log 'The API and scheduler are not stopped. They must not insert audience_profiles or consume its ID sequence; newer live profile updates are preserved and identity conflicts or serialization failures stop the run.'

# CSV fields in this export have no embedded newlines. A physical line is thus
# one CSV record; COPY remains PostgreSQL's CSV parser and validates every field.
tail -n +"$next_line" -- "$csv_file" | begin
    set -l batch_number (math "$last_batch + 1")
    set -l line_number "$next_line"
    set -l rows_in_batch 0
    set -l batch_file ''

    function open_batch_file
        set batch_file (mktemp "$project_dir/var/audience-batch.XXXXXXXX"); or die 'Could not create batch file.'
        set -g CURRENT_BATCH_FILE "$batch_file"
        printf '%s\n' "$header" >"$batch_file"; or die "Could not write batch file: $batch_file"
    end

    function commit_batch
        set -l timeout_ms (batch_timeout_ms)
        if test "$timeout_ms" -le 0
            log "WINDOW_CLOSED batch=$batch_number completed_rows=$completed_rows; no new batch started."
            return 10
        end

        set -l result_file "$batch_file.result"
        log "BATCH_START batch=$batch_number rows=$rows_in_batch source_lines=$line_number-"(math "$line_number + $rows_in_batch - 1")" timeout_ms=$timeout_ms"
        begin
            cat -- "$prefix_sql"
            cat -- "$batch_file"
            printf '\\.\n'
            cat -- "$suffix_sql"
        end | docker exec -i -e "PGAPPNAME=$APP_NAME" "$POSTGRES_CONTAINER" \
            psql -X -v ON_ERROR_STOP=1 -v "batch_timeout_ms=$timeout_ms" -U "$db_user" -d "$db_name" >"$result_file"
        set -l pipeline_status $pipestatus
        for pipeline_exit in $pipeline_status
            if test "$pipeline_exit" -ne 0
                log "BATCH_FAILED batch=$batch_number rows=$rows_in_batch source_line=$line_number pipeline_status=$pipeline_status; state remains at batch=$last_batch."
                test -s "$result_file"; and cat -- "$result_file" >&2
                rm -f -- "$result_file" "$batch_file"
                set -e CURRENT_BATCH_FILE
                return 1
            end
        end
        if not test -s "$result_file"
            log "BATCH_FAILED batch=$batch_number rows=$rows_in_batch source_line=$line_number: psql produced no result."
            test -s "$result_file"; and cat -- "$result_file" >&2
            rm -f -- "$result_file" "$batch_file"
            set -e CURRENT_BATCH_FILE
            return 1
        end

        set -l plan_line (string match -r '^BATCH_PLAN .*' <"$result_file")
        string match -q 'BATCH_COMMITTED' <"$result_file"; or begin
            log "BATCH_FAILED batch=$batch_number: psql exited without the commit marker."
            rm -f -- "$result_file" "$batch_file"
            set -e CURRENT_BATCH_FILE
            return 1
        end

        set last_batch "$batch_number"
        set completed_rows (math "$completed_rows + $rows_in_batch")
        set line_number (math "$line_number + $rows_in_batch")
        write_state "$state_file" "$csv_sha" "$csv_bytes" "$header_sha" "$last_batch" "$line_number" "$completed_rows"
        log "BATCH_SUCCESS batch=$last_batch rows=$rows_in_batch completed_rows=$completed_rows next_source_line=$line_number $plan_line"
        rm -f -- "$result_file" "$batch_file"
        set -e CURRENT_BATCH_FILE
        set batch_number (math "$batch_number + 1")
        set rows_in_batch 0
        return 0
    end

    while read -l row
        if test "$rows_in_batch" -eq 0
            in_tehran_window; or begin
                log "WINDOW_CLOSED completed_rows=$completed_rows; resume manually on a later night."
                exit 0
            end
            open_batch_file
        end
        printf '%s\n' "$row" >>"$batch_file"; or die "Could not append batch file: $batch_file"
        set rows_in_batch (math "$rows_in_batch + 1")
        if test "$rows_in_batch" -eq "$BATCH_SIZE"
            commit_batch; or exit $status
        end
    end

    if test "$rows_in_batch" -gt 0
        commit_batch; or exit $status
    end
    log "IMPORT_COMPLETE batches=$last_batch rows=$completed_rows"
end

set -l worker_status $pipestatus[-1]
exit "$worker_status"
