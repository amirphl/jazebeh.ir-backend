#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
if [[ ! -e "$root/.env.development" ]]; then cp "$root/config/development.env.example" "$root/.env.development"; fi
[[ -f "$root/.env.development" && ! -L "$root/.env.development" ]] || {
	echo 'Refusing invalid env file' >&2
	exit 1
}
chmod 600 "$root/.env.development"
printf 'Edit DB_HOST, DB_PORT, DB_NAME, DB_USER, DB_PASSWORD and CACHE_REDIS_URL in %s\n' "$root/.env.development"
printf 'For Redis credentials use a URL-encoded redis://username:password@host:port/db URL.\n'
if [[ -t 0 ]]; then "${EDITOR:-vi}" "$root/.env.development"; fi
