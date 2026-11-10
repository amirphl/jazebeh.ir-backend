#!/usr/bin/env bash

# Launch the large audience CSV import as a transient systemd service.
# Usage:
#   run-yamata-audience-profiles-csv-import.sh CSV_FILE [PROJECT_DIR] [UNIT_NAME] --confirm-maintenance-window

set -Eeuo pipefail

CSV_ARGUMENT="${1:-}"
PROJECT_DIR="/srv/yamata"
REQUESTED_UNIT=""
CONFIRMATION=""
project_dir_set=false

die() {
    printf '[audience-csv-import-launcher] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage:
  run-yamata-audience-profiles-csv-import.sh CSV_FILE [PROJECT_DIR] [UNIT_NAME] --confirm-maintenance-window
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
        --help|-h)
            usage
            exit 0
            ;;
        --*)
            die "Unknown option: $1"
            ;;
        *)
            if [[ "$project_dir_set" == false ]]; then
                PROJECT_DIR="$1"
                project_dir_set=true
            elif [[ -z "$REQUESTED_UNIT" ]]; then
                REQUESTED_UNIT="$1"
            else
                die "Unexpected argument: $1"
            fi
            ;;
    esac
    shift
done

[[ "$CONFIRMATION" == --confirm-maintenance-window ]] || {
    usage >&2
    die 'Refusing to start without --confirm-maintenance-window'
}

CSV_FILE="$(readlink -f -- "$CSV_ARGUMENT")"
readonly CSV_FILE
readonly PROJECT_DIR REQUESTED_UNIT
[[ -f "$CSV_FILE" && ! -L "$CSV_FILE" ]] || die "CSV must be a regular file: $CSV_FILE"
[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"

readonly HELPER="$PROJECT_DIR/scripts/audience-profile-csv-import/import-yamata-audience-profiles-csv.sh"
[[ -x "$HELPER" ]] || die "Importer must be executable: $HELPER"

for command_name in systemctl systemd-run journalctl; do
    command -v "$command_name" >/dev/null 2>&1 || die "Required command is missing: $command_name"
done

unit="${REQUESTED_UNIT:-yamata-audience-csv-import-$(date -u +%Y%m%dT%H%M%SZ)}"
unit="${unit%.service}"
[[ "$unit" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "Invalid systemd unit name: $unit"
readonly unit

if [[ $EUID -eq 0 ]]; then
    SYSTEM=()
else
    command -v sudo >/dev/null 2>&1 || die 'sudo is required to start the system service'
    sudo -v
    SYSTEM=(sudo)
fi
readonly SYSTEM

"${SYSTEM[@]}" systemd-run \
    --unit="$unit" \
    --description='Yamata audience CSV profile import' \
    --property=Type=exec \
    --property=Restart=no \
    --property=TimeoutStartSec=infinity \
    "$HELPER" "$CSV_FILE" "$PROJECT_DIR" --confirm-maintenance-window

printf '[audience-csv-import-launcher] Started %s.service\n' "$unit"
printf '[audience-csv-import-launcher] Follow: sudo journalctl -u %s -f -o cat\n' "$unit"
printf '[audience-csv-import-launcher] Status: sudo systemctl show %s -p ActiveState -p SubState -p Result -p ExecMainStatus\n' "$unit"
printf '[audience-csv-import-launcher] Database snapshot: sudo %s %s %s\n' \
    "$PROJECT_DIR/scripts/audience-profile-csv-import/monitor-yamata-audience-profiles-csv-import.sh" "$PROJECT_DIR" "$unit"
