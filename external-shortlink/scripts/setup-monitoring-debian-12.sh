#!/usr/bin/env bash
# Run on jzbe.ir as root. Installs node_exporter locally and permits exactly
# one Prometheus host through Nginx to scrape application and host metrics.
set -Eeuo pipefail
readonly SOURCE='/opt/external-shortlink/app/deploy/monitoring-nginx.conf'
readonly TARGET='/etc/nginx/snippets/external-shortlink-monitoring.conf'
ip=''
[[ ${1:-} == '--monitoring-ip' ]] && ip=${2:-}
[[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ || "$ip" == *:* ]] || { echo "Usage: sudo $0 --monitoring-ip <Prometheus-public-IP>" >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo 'run as root' >&2; exit 1; }
command -v nginx >/dev/null && command -v systemctl >/dev/null || { echo 'deploy external-shortlink first' >&2; exit 1; }
apt-get update
apt-get install -y --no-install-recommends prometheus-node-exporter
# Debian's package listens on every interface by default. Override its command
# rather than relying on the network firewall: only Nginx may reach exporter.
install -d -o root -g root -m 0755 /etc/systemd/system/prometheus-node-exporter.service.d
cat > /etc/systemd/system/prometheus-node-exporter.service.d/external-shortlink.conf <<'EOF'
[Service]
ExecStart=
ExecStart=/usr/bin/prometheus-node-exporter --web.listen-address=127.0.0.1:9100
EOF
systemctl daemon-reload
install -d -o root -g root -m 0755 /etc/nginx/snippets
tmp=$(mktemp /etc/nginx/snippets/.external-shortlink-monitoring.XXXXXX)
sed "s/MONITORING_SOURCE_IP/$ip/g" "$SOURCE" > "$tmp"
install -o root -g root -m 0644 "$tmp" "$TARGET"
rm -f "$tmp"
nginx -t
systemctl enable --now prometheus-node-exporter
systemctl restart prometheus-node-exporter
systemctl reload nginx
echo 'Done: Prometheus may scrape https://jzbe.ir/metrics and https://jzbe.ir/node-metrics.'
