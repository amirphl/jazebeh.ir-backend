#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
cd "$root"
case "$(uname -s)" in
Darwin)
	major="$(sw_vers -productVersion | cut -d. -f1)"
	((major >= 14)) || {
		echo 'macOS 14 or later required' >&2
		exit 1
	}
	;;
Linux)
	[[ -r /etc/os-release ]] || {
		echo 'Cannot identify Linux distribution' >&2
		exit 1
	}
	source /etc/os-release
	[[ "${ID:-}" == ubuntu && "${VERSION_ID%%.*}" -ge 24 ]] || {
		echo 'Ubuntu 24.04 or later required' >&2
		exit 1
	}
	;;
*)
	echo 'Unsupported operating system' >&2
	exit 1
	;;
esac
if ! command -v go >/dev/null 2>&1; then
	if [[ "$(uname -s)" == Linux ]]; then
		sudo apt-get update
		sudo apt-get install -y golang-go
	else
		if ! command -v brew >/dev/null 2>&1; then
			echo 'Installing Homebrew to provide the Go toolchain (administrator approval may be required).'
			installer="$(mktemp "${TMPDIR:-/tmp}/yamata-homebrew.XXXXXXXX")"
			trap 'rm -f -- "$installer"' EXIT
			curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh -o "$installer"
			/bin/bash "$installer"
			for prefix in /opt/homebrew/bin /usr/local/bin; do
				if [[ -x "$prefix/brew" ]]; then
					export PATH="$prefix:$PATH"
					break
				fi
			done
			command -v brew >/dev/null 2>&1 || {
				echo 'Homebrew installation did not provide brew on PATH' >&2
				exit 1
			}
		fi
		brew install go
	fi
fi
for cmd in go python3 psql redis-cli; do command -v "$cmd" >/dev/null || {
	printf 'Install %s and retry\n' "$cmd" >&2
	exit 1
}; done
if [[ ! -f .env.development ]]; then
	cp config/development.env.example .env.development
	chmod 600 .env.development
	printf 'Created .env.development. Set DB_PASSWORD and CACHE_REDIS_URL, then rerun this script.\n'
	exit 1
fi
source "$script_dir/load-yamata-env.sh"
load_yamata_env_file "$root/.env.development"
[[ "${APP_ENV:-}" == development && "${EXTERNAL_SERVICES_MODE:-}" == mock ]] || {
	echo 'Development mock mode required' >&2
	exit 1
}
: "${DB_HOST:?}" "${DB_PORT:?}" "${DB_NAME:?}" "${DB_USER:?}" "${DB_PASSWORD:?}" "${CACHE_REDIS_URL:?}"
source "$script_dir/guard-environment.sh"
reject_production_like_target "$DB_NAME" "$DB_HOST"
export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGDATABASE="$DB_NAME" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD" PGSSLMODE="${DB_SSL_MODE:-disable}"
psql -X -v ON_ERROR_STOP=1 -c 'SELECT 1' >/dev/null
redis-cli -u "$CACHE_REDIS_URL" ping | grep -q '^PONG$' || {
	echo 'Redis check failed' >&2
	exit 1
}
"$script_dir/migrate-development.sh"
"$script_dir/seed-mock-reference-data.sh" --environment development
go mod download
mkdir -p .vscode data/campaign-audience-reports data/bundle-action-files
if [[ ! -e .vscode/launch.json ]]; then cp config/development.launch.json .vscode/launch.json; fi
printf 'Setup complete. Select Yamata development in VS Code and press Run.\n'
