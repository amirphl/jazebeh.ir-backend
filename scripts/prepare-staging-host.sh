#!/usr/bin/env bash
set -Eeuo pipefail
[[ -r /etc/os-release ]] || {
	echo 'Ubuntu 24.04 or later required' >&2
	exit 1
}
source /etc/os-release
[[ "${ID:-}" == ubuntu && "${VERSION_ID%%.*}" -ge 24 ]] || {
	echo 'Ubuntu 24.04 or later required' >&2
	exit 1
}
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-v2 gettext-base openssl curl python3 apache2-utils ufw
sudo systemctl enable --now docker
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
printf 'Docker is ready. Configure Docker access for your deploy user, provision the wildcard certificate and staging secrets, then run deploy-staging-beta.sh.\n'
printf 'UFW rules were added but the firewall was not enabled; confirm your SSH port before enabling it.\n'
