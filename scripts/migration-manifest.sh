#!/usr/bin/env bash
# Populate MIGRATION_FILES in the order in migrations/run_all_up.sql.
load_migration_manifest() {
	local root="$1" line name seen=$'\n'
	MIGRATION_FILES=()
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ "$line" =~ ^[[:space:]]*\\(i|ir)([[:space:]]|$) ]] || continue
		if [[ ! "$line" =~ ^[[:space:]]*\\i[[:space:]]+migrations/([0-9]{4}_[A-Za-z0-9_]+\.sql)[[:space:]]*$ ]]; then
			printf 'Invalid migration include in manifest: %s\n' "$line" >&2
			return 1
		fi
		name="${BASH_REMATCH[1]}"
		[[ "$name" != *_down.sql && -f "$root/migrations/$name" ]] || {
			printf 'Invalid or missing migration: %s\n' "$name" >&2
			return 1
		}
		[[ "$seen" != *$'\n'"$name"$'\n'* ]] || {
			printf 'Duplicate migration in manifest: %s\n' "$name" >&2
			return 1
		}
		seen+="$name"$'\n'
		MIGRATION_FILES+=("$name")
	done < "$root/migrations/run_all_up.sql"
	[[ ${#MIGRATION_FILES[@]} -gt 0 ]] || {
		echo 'Migration manifest contains no includes' >&2
		return 1
	}
}
