#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$script_dir")"
cd "$root"
failed=0
while IFS= read -r -d '' file; do
	case "$file" in
	.env | .env.* | *.pem | *.key | *.p12 | *.pfx | *.dump | *.tar.gz)
		printf 'Tracked sensitive artifact needs review: %s\n' "$file" >&2
		failed=1
		;;
	esac
done < <(git ls-files -z)
if git ls-files --error-unmatch atipay.txt >/dev/null 2>&1; then
	printf 'Review tracked atipay.txt for credentials; this check does not inspect its contents.\n' >&2
fi
python3 "$script_dir/audit_deployment_artifacts.py" || failed=1
exit "$failed"
