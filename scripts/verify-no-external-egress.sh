#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
cd "$root"
go test ./ ./config ./app/scheduler ./app/services ./business_flow
printf 'Mock selection and Go HTTP egress guard tests passed. Non-Go processes and custom transports still require host or container egress controls.\n'
