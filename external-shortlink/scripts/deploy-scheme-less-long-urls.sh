#!/usr/bin/env bash
# Deploy the external-shortlink release that accepts scheme-less destinations.
# The Rust service prepends HTTPS before persistence; this script does not
# alter production or external database rows.

set -Eeuo pipefail
IFS=$'\n\t'

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly DEPLOY_SCRIPT="$SCRIPT_DIR/deploy-debian-12.sh"

usage() {
	cat >&2 <<'USAGE'
Usage:
  sudo ./scripts/deploy-scheme-less-long-urls.sh \
    --api-token-file /root/external-shortlink-api-token \
    [--acme-email EMAIL] [--source-dir DIRECTORY]

Builds and deploys the checked-in external-shortlink service, then verifies
local liveness and database readiness. The source checkout must be a clean,
reviewed Git commit, as enforced by deploy-debian-12.sh.
USAGE
	exit 2
}

[[ ${EUID} -eq 0 ]] || {
	printf 'Run this script with sudo or as root.\n' >&2
	exit 1
}
[[ -x "$DEPLOY_SCRIPT" ]] || {
	printf 'Missing deployment script: %s\n' "$DEPLOY_SCRIPT" >&2
	exit 1
}
[[ $# -gt 0 ]] || usage

"$DEPLOY_SCRIPT" deploy "$@"
curl -fsS http://127.0.0.1:8081/healthz >/dev/null
curl -fsS http://127.0.0.1:8081/readyz >/dev/null
printf 'external-shortlink scheme-less destination release is healthy\n'
