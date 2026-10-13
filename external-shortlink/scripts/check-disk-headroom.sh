#!/usr/bin/env bash
# Protect the click store before PostgreSQL/WAL reaches ENOSPC. Alerts are sent
# to journald at 70% and 80%; host monitoring must page on the latter. At 90%
# new redirects are stopped rather than accepting a click that cannot be made
# durable. This script never deletes retained click data.
set -Eeuo pipefail

readonly DATA_PATH='/var/lib/docker'
readonly WARN_PERCENT=70
readonly PAGE_PERCENT=80
readonly FAIL_CLOSED_PERCENT=90

used_percent="$(df -P "$DATA_PATH" | awk 'NR == 2 { gsub(/%/, "", $5); print $5 }')"
[[ "$used_percent" =~ ^[0-9]+$ ]] || {
    logger -p daemon.err -t external-shortlink-disk-guard "could not determine filesystem usage for $DATA_PATH"
    exit 1
}

if (( used_percent >= FAIL_CLOSED_PERCENT )); then
    logger -p daemon.crit -t external-shortlink-disk-guard "disk usage=${used_percent}% threshold=${FAIL_CLOSED_PERCENT}%; stopping redirects before PostgreSQL exhausts disk"
    systemctl stop external-shortlink.service
    exit 1
fi
if (( used_percent >= PAGE_PERCENT )); then
    logger -p daemon.err -t external-shortlink-disk-guard "disk usage=${used_percent}% threshold=${PAGE_PERCENT}%; page storage on-call and expand capacity"
    exit 1
fi
if (( used_percent >= WARN_PERCENT )); then
    logger -p daemon.warning -t external-shortlink-disk-guard "disk usage=${used_percent}% threshold=${WARN_PERCENT}%; storage capacity warning"
fi
