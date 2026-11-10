#!/usr/bin/env bash

# Launch the reference-data CSV import as a detached transient systemd service.
# Usage:
#   run-yamata-reference-data-csv-import.sh STATS_CSV REFERENCE_CSV TAGS_CSV [PROJECT_DIR] [UNIT_NAME] --confirm-maintenance-window

set -Eeuo pipefail

STATS_CSV="${1:-}"
REFERENCE_CSV="${2:-}"
TAGS_CSV="${3:-}"
PROJECT_DIR="/srv/yamata"
REQUESTED_UNIT=""
CONFIRMATION=""
project_dir_set=false

die() { printf '[reference-data-csv-import-launcher] ERROR: %s\n' "$*" >&2; exit 1; }
[[ -n "$STATS_CSV" && -n "$REFERENCE_CSV" && -n "$TAGS_CSV" ]] || die "Usage: $(basename "$0") STATS_CSV REFERENCE_CSV TAGS_CSV [PROJECT_DIR] [UNIT_NAME] --confirm-maintenance-window"
shift 3
while (($# > 0)); do
    case "$1" in
        --confirm-maintenance-window) CONFIRMATION="$1" ;;
        --help|-h) printf 'Usage: %s STATS_CSV REFERENCE_CSV TAGS_CSV [PROJECT_DIR] [UNIT_NAME] --confirm-maintenance-window\n' "$(basename "$0")"; exit 0 ;;
        --*) die "Unknown option: $1" ;;
        *)
            if [[ "$project_dir_set" == false ]]; then PROJECT_DIR="$1"; project_dir_set=true
            elif [[ -z "$REQUESTED_UNIT" ]]; then REQUESTED_UNIT="$1"
            else die "Unexpected argument: $1"; fi
            ;;
    esac
    shift
done
[[ "$CONFIRMATION" == --confirm-maintenance-window ]] || die 'Refusing to start without --confirm-maintenance-window'

for csv_file in "$STATS_CSV" "$REFERENCE_CSV" "$TAGS_CSV"; do
    [[ -f "$csv_file" && ! -L "$csv_file" ]] || die "CSV must be a regular file: $csv_file"
done
[[ -d "$PROJECT_DIR" ]] || die "Project directory does not exist: $PROJECT_DIR"
readonly HELPER="$PROJECT_DIR/scripts/reference-data-csv-import/import-yamata-reference-data-csv.sh"
[[ -x "$HELPER" ]] || die "Importer must be executable: $HELPER"

unit="${REQUESTED_UNIT:-yamata-reference-data-csv-import-$(date -u +%Y%m%dT%H%M%SZ)}"
unit="${unit%.service}"
[[ "$unit" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "Invalid systemd unit name: $unit"

if [[ $EUID -eq 0 ]]; then SYSTEM=()
else command -v sudo >/dev/null 2>&1 || die 'sudo is required to start the system service'; sudo -v; SYSTEM=(sudo)
fi

"${SYSTEM[@]}" systemd-run --unit="$unit" --description='Yamata reference-data CSV import' \
    --property=Type=exec --property=Restart=no --property=TimeoutStartSec=infinity \
    "$HELPER" "$STATS_CSV" "$REFERENCE_CSV" "$TAGS_CSV" "$PROJECT_DIR" --confirm-maintenance-window

printf '[reference-data-csv-import-launcher] Started %s.service\n' "$unit"
printf '[reference-data-csv-import-launcher] Follow: sudo journalctl -u %s -f -o cat\n' "$unit"
printf '[reference-data-csv-import-launcher] Status: sudo systemctl show %s -p ActiveState -p SubState -p Result -p ExecMainStatus\n' "$unit"
