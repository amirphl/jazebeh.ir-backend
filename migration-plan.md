🐉 Yamata no Orochi — Complete Server Migration Plan
Table of Contents
Pre-Migration Inventory & Preparation
New Server OS Hardening & Kernel Tuning
New Server Software Setup
Data Migration — PostgreSQL
Data Migration — Redis
Data Migration — Uploaded Files
Data Migration — Grafana & Prometheus
Code & Config Transfer
.env File Migration
TLS Certificates
DNS Cutover
docker/ Directory & docker-compose
Scripts Directory
Database Migration Tracker (golang-migrate)
First Boot & Smoke Tests
Monitoring & Rollback Plan
Decommission Old Server
1. Pre-Migration Inventory & Preparation
1.1 Document what you have on the OLD server
Run this on the old server and save the output somewhere safe:


#!/bin/bash
# Run as root on old server — save output to migration-audit.txt

echo "=== Docker volumes ===" 
docker volume ls

echo "=== Volume sizes ==="
for vol in postgres_data_beta redi_data_beta uploads_beta \
           prometheus_data_beta grafana_data_beta postgres_backups_beta \
           sentry_postgres_data_beta sentry_redis_data_beta; do
  size=$(docker run --rm -v ${vol}:/data alpine du -sh /data 2>/dev/null | cut -f1)
  echo "  $vol : $size"
done

echo "=== Disk free ==="
df -h /var/lib/docker

echo "=== Running containers ==="
docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"

echo "=== acme.sh domains ==="
~/.acme.sh/acme.sh --list

echo "=== Let's Encrypt live certs ==="
ls /etc/letsencrypt/live/

echo "=== Network interfaces ==="
ip -4 addr show scope global

echo "=== sysctl key values ==="
sysctl net.core.somaxconn vm.swappiness fs.file-max

echo "=== OS version ==="
cat /etc/os-release
uname -r

echo "=== Docker version ==="
docker version --format '{{.Server.Version}}'

echo "=== Compose version ==="
docker compose version
1.2 Choose migration window
Pick a low-traffic maintenance window (e.g., 02:00–05:00 local time).
Announce downtime if your app has SLA obligations.
Expected total downtime: 15–30 minutes (database freeze → DNS propagation).
1.3 Back up everything on the old server right now

# On old server — create a full pre-migration backup before touching anything

cd /path/to/yamata-no-orochi

# 1. pg_dump of main database
docker exec yamata-postgres-beta pg_dump \
  -U "$DB_USER" -d "$DB_NAME" -Fc \
  -f /tmp/premigration-main-$(date +%Y%m%dT%H%M%SZ).dump

# 2. pg_dump of Sentry database
docker exec yamata-sentry-postgres-beta pg_dump \
  -U "$SENTRY_POSTGRES_USER" -d "$SENTRY_POSTGRES_DB" -Fc \
  -f /tmp/premigration-sentry-$(date +%Y%m%dT%H%M%SZ).dump

# 3. Redis RDB snapshot
docker exec yamata-redis-beta redis-cli BGSAVE
sleep 5
docker exec yamata-redis-beta redis-cli LASTSAVE

# 4. Tar the entire repo (includes .env, docker/, scripts/, migrations/)
tar -czf /root/yamata-premigration-repo-$(date +%Y%m%d).tar.gz \
  --exclude='.gocache' --exclude='.git' \
  /path/to/yamata-no-orochi

# 5. Upload dumps off-server (S3, SFTP, etc.)
aws s3 cp /tmp/premigration-main-*.dump s3://$BACKUP_S3_BUCKET/premigration/
aws s3 cp /tmp/premigration-sentry-*.dump s3://$BACKUP_S3_BUCKET/premigration/
2. New Server OS Hardening & Kernel Tuning
2.1 Initial OS setup (Debian/Ubuntu)

# As root on NEW server

apt update && apt upgrade -y
apt install -y curl wget git unzip gnupg2 lsb-release ca-certificates \
               htop iotop nethogs iftop sysstat dstat \
               net-tools ethtool iperf3 ufw fail2ban \
               openssl jq acl

# Create a non-root deploy user
useradd -m -s /bin/bash -G sudo,docker deploy
# Set up SSH key for deploy user (copy your public key)
mkdir -p /home/deploy/.ssh
cp ~/.ssh/authorized_keys /home/deploy/.ssh/
chown -R deploy:deploy /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
chmod 600 /home/deploy/.ssh/authorized_keys
2.2 SSH hardening

cat > /etc/ssh/sshd_config.d/99-hardening.conf << 'EOF'
PermitRootLogin no
PasswordAuthentication no
ChallengeResponseAuthentication no
UsePAM yes
X11Forwarding no
AllowUsers deploy
MaxAuthTries 3
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
EOF

systemctl reload sshd
2.3 Firewall (UFW)

ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # HTTP (cert renewal + redirect)
ufw allow 443/tcp   # HTTPS
# If you need direct DB access from a specific admin IP:
# ufw allow from 1.2.3.4 to any port 5432
ufw enable
ufw status verbose
2.4 fail2ban

cat > /etc/fail2ban/jail.local << 'EOF'
[DEFAULT]
bantime  = 3600
findtime = 600
maxretry = 5
destemail = your-alert@email.com
action = %(action_mwl)s

[sshd]
enabled = true
port = 22
logpath = /var/log/auth.log

[nginx-http-auth]
enabled = true

[nginx-limit-req]
enabled = true
filter = nginx-limit-req
logpath = /var/log/nginx/error.log
EOF

systemctl enable --now fail2ban
2.5 Kernel & system tuning
Run the existing system/tune-server.sh — but review/extend it first with the additions below:


# Copy it to the new server, then run:
sudo bash system/tune-server.sh
Additional sysctl settings to append (add to /etc/sysctl.d/99-yamata.conf):


# Huge pages for PostgreSQL (optional but beneficial)
vm.nr_hugepages = 128

# Protect against SYN flood
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_syn_retries = 3
net.ipv4.tcp_synack_retries = 3

# Kernel pointer restriction (security)
kernel.kptr_restrict = 2
kernel.dmesg_restrict = 1

# Disable core dumps for setuid binaries
fs.suid_dumpable = 0

# Randomize memory layout
kernel.randomize_va_space = 2

# BBR congestion control (already in tune-server.sh, verify module is loaded)
net.ipv4.tcp_congestion_control = bbr
net.core.default_qdisc = fq

# Increase UDP buffer for Prometheus metrics
net.core.rmem_max = 26214400
net.core.wmem_max = 26214400

# Docker/container networking
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward = 1
Apply:


sysctl --system
2.6 Swap (if not already present)

# Only if RAM < 8 GB or you want a safety net
fallocate -l 4G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
2.7 Docker install

# Official Docker install (Debian/Ubuntu)
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
  gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

echo "deb [arch=$(dpkg --print-architecture) \
  signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
  https://download.docker.com/linux/ubuntu \
  $(lsb_release -cs) stable" | \
  tee /etc/apt/sources.list.d/docker.list

apt update
apt install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Add deploy user to docker group
usermod -aG docker deploy

# Tune Docker daemon
cat > /etc/docker/daemon.json << 'EOF'
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "100m",
    "max-file": "5"
  },
  "storage-driver": "overlay2",
  "live-restore": true,
  "default-ulimits": {
    "nofile": {
      "Name": "nofile",
      "Hard": 65536,
      "Soft": 65536
    }
  }
}
EOF

systemctl daemon-reload
systemctl enable --now docker
3. New Server Software Setup

# acme.sh (same version as old server — check with: ~/.acme.sh/acme.sh --version)
curl https://get.acme.sh | sh -s email=$CERTBOT_EMAIL
source ~/.bashrc

# Python 3 + deps for scripts
apt install -y python3 python3-pip python3-venv
pip3 install --break-system-packages requests psutil sentry-sdk

# aws cli (for S3 backups) — optional
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o awscliv2.zip
unzip awscliv2.zip && ./aws/install

# git (to clone repo)
git clone git@github.com:your-org/yamata-no-orochi.git /srv/yamata
cd /srv/yamata
4. Data Migration — PostgreSQL
This is the most critical step. Use the stop-the-world approach for consistency.

4.1 Strategy: pg_dump / pg_restore (safest, zero schema drift)

# ========== ON OLD SERVER ==========

# Step 1: Put app in maintenance mode
# (stop scheduler / campaign execution first to prevent dirty writes)
docker stop yamata-app-beta yamata-postgres-backup-beta

# Step 2: Final authoritative dump (after app is stopped)
TIMESTAMP=$(date +%Y%m%dT%H%M%SZ)

docker exec yamata-postgres-beta pg_dump \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  --format=custom \
  --compress=9 \
  --verbose \
  -f /tmp/main-final-${TIMESTAMP}.dump

docker exec yamata-sentry-postgres-beta pg_dump \
  -U "$SENTRY_POSTGRES_USER" \
  -d "$SENTRY_POSTGRES_DB" \
  --format=custom \
  --compress=9 \
  -f /tmp/sentry-final-${TIMESTAMP}.dump

# Step 3: Copy dumps to new server
scp /tmp/main-final-${TIMESTAMP}.dump deploy@NEW_SERVER_IP:/tmp/
scp /tmp/sentry-final-${TIMESTAMP}.dump deploy@NEW_SERVER_IP:/tmp/

# ========== ON NEW SERVER ==========

# Step 4: Start only postgres (not the full stack yet)
cd /srv/yamata
docker compose -f docker-compose.beta.yml up -d postgres-beta sentry-postgres-beta

# Wait for healthy
docker compose -f docker-compose.beta.yml ps

# Step 5: Restore main database
docker cp /tmp/main-final-${TIMESTAMP}.dump yamata-postgres-beta:/tmp/

docker exec yamata-postgres-beta pg_restore \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  --verbose \
  --no-owner \
  --no-privileges \
  /tmp/main-final-${TIMESTAMP}.dump

# Step 6: Restore Sentry database
docker cp /tmp/sentry-final-${TIMESTAMP}.dump yamata-sentry-postgres-beta:/tmp/

docker exec yamata-sentry-postgres-beta pg_restore \
  -U "$SENTRY_POSTGRES_USER" \
  -d "$SENTRY_POSTGRES_DB" \
  --verbose \
  --no-owner \
  --no-privileges \
  /tmp/sentry-final-${TIMESTAMP}.dump

# Step 7: Verify row counts match
docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" -c "
  SELECT schemaname, tablename, n_live_tup AS rows
  FROM pg_stat_user_tables
  ORDER BY n_live_tup DESC;
"
4.2 Verify the migration_tracker table
Your app uses golang-migrate. Confirm the version table is intact:


docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" -c \
  "SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 5;"
Expected: latest migration version present, dirty = false. See §14 for full details.

5. Data Migration — Redis
Redis holds cache + session data (not the source of truth). The risk of losing it is low — the app will rebuild cache on first access. However, for session continuity (users won't get logged out), migrate it.


# ========== ON OLD SERVER ==========

# Flush pending writes to disk
docker exec yamata-redis-beta redis-cli BGSAVE
# Wait until: docker exec yamata-redis-beta redis-cli LASTSAVE returns a newer timestamp

# Copy the RDB file out of the volume
docker cp yamata-redis-beta:/data/dump.rdb /tmp/redis-dump-$(date +%Y%m%d).rdb

# Also copy AOF if appendonly is enabled (it is — see redis.conf)
docker cp yamata-redis-beta:/data/appendonly.aof /tmp/redis-aof-$(date +%Y%m%d).aof

# Send to new server
scp /tmp/redis-dump-*.rdb deploy@NEW_SERVER_IP:/tmp/
scp /tmp/redis-aof-*.aof deploy@NEW_SERVER_IP:/tmp/

# ========== ON NEW SERVER ==========

# Start redis on new server (don't start the app yet)
docker compose -f docker-compose.beta.yml up -d redis-beta

# Stop redis temporarily to inject data
docker stop yamata-redis-beta

# Find the volume mount path
REDIS_VOL=$(docker volume inspect redi_data_beta --format '{{.Mountpoint}}')

# Copy RDB into volume
sudo cp /tmp/redis-dump-*.rdb ${REDIS_VOL}/dump.rdb
sudo cp /tmp/redis-aof-*.aof ${REDIS_VOL}/appendonly.aof
sudo chown 999:999 ${REDIS_VOL}/dump.rdb ${REDIS_VOL}/appendonly.aof

# Restart Redis — it will load from the files
docker start yamata-redis-beta

# Verify key count matches old server
docker exec yamata-redis-beta redis-cli DBSIZE
Note on Sentry Redis: GlitchTip's Redis is ephemeral (task queues). No need to migrate — it will reset cleanly when restarted.

6. Data Migration — Uploaded Files
Uploaded files live in the uploads_beta Docker volume, mounted at /data inside yamata-app-beta.


# ========== ON OLD SERVER ==========

# Find volume path
UPLOADS_VOL=$(docker volume inspect uploads_beta --format '{{.Mountpoint}}')

# Tar the uploads
tar -czf /tmp/uploads-$(date +%Y%m%d).tar.gz -C "$UPLOADS_VOL" .

# Check size before sending
du -sh /tmp/uploads-*.tar.gz

# Send to new server (rsync is better for large sets — preserves partial transfers)
rsync -avz --progress \
  /tmp/uploads-$(date +%Y%m%d).tar.gz \
  deploy@NEW_SERVER_IP:/tmp/

# ========== ON NEW SERVER ==========

NEW_VOL=$(docker volume inspect uploads_beta --format '{{.Mountpoint}}')
# If volume doesn't exist yet, create it:
docker volume create uploads_beta

sudo tar -xzf /tmp/uploads-*.tar.gz -C "$NEW_VOL"
sudo chown -R 1000:1000 "$NEW_VOL"   # match appuser UID in container
Verify:


# Compare file counts
OLD_COUNT=$(docker exec yamata-app-beta find /data -type f | wc -l)  # on old server
NEW_COUNT=$(find "$NEW_VOL" -type f | wc -l)                          # on new server
echo "Old: $OLD_COUNT  New: $NEW_COUNT"
7. Data Migration — Grafana & Prometheus
Grafana (dashboards + settings)

# On old server
GRAFANA_VOL=$(docker volume inspect grafana_data_beta --format '{{.Mountpoint}}')
tar -czf /tmp/grafana-$(date +%Y%m%d).tar.gz -C "$GRAFANA_VOL" .
scp /tmp/grafana-*.tar.gz deploy@NEW_SERVER_IP:/tmp/

# On new server
docker volume create grafana_data_beta
NEW_GRAFANA=$(docker volume inspect grafana_data_beta --format '{{.Mountpoint}}')
sudo tar -xzf /tmp/grafana-*.tar.gz -C "$NEW_GRAFANA"
sudo chown -R 472:472 "$NEW_GRAFANA"
Note: Your Grafana dashboards are also provisioned from docker/grafana/dashboards/yamata-overview.json — they'll be re-applied on startup. The volume migration preserves alert rules, user accounts, and custom data sources that aren't in version control.

Prometheus
Prometheus TSDB data (prometheus_data_beta) can be migrated or left fresh — your choice:

Fresh start: Prometheus rebuilds metrics from scrape targets within minutes. Only historical data is lost.
Migrate: Same tar/scp/untar process as Grafana, chown to 65534:65534 (nobody).
8. Code & Config Transfer

# Option A: Clone from git (cleanest)
git clone git@github.com:your-org/yamata-no-orochi.git /srv/yamata
cd /srv/yamata
git checkout main   # or your production branch

# Option B: rsync from old server (includes uncommitted changes)
rsync -avz --exclude='.gocache' --exclude='.git' \
  old-server:/path/to/yamata-no-orochi/ \
  /srv/yamata/

# Build the production Docker images on new server
cd /srv/yamata

# Build app image
docker build -f docker/Dockerfile.production -t yamata-no-orochi .

# Build cert-monitor image
docker build -f docker/cert-monitor/Dockerfile -t yamata-cert-monitor-beta docker/cert-monitor/

# Build frontend image (if Dockerfile exists)
# docker build -f frontend/Dockerfile -t yamata-frontend-beta frontend/
9. .env File Migration
.env / .env.beta are not in git (correctly gitignored). Copy them securely.


# Secure copy via SCP (never email or paste in chat)
scp old-server:/path/to/yamata-no-orochi/.env.beta /srv/yamata/.env.beta

# Verify file integrity (compare checksums on both servers)
md5sum /srv/yamata/.env.beta
# Compare with: md5sum /path/to/yamata/.env.beta on old server
Keys to update in .env.beta for the new server:
Variable	Action
SERVER_TRUSTED_PROXIES	Update to new server IP / Docker subnet
SENTRY_SERVER_NAME	Update to new server hostname
SENTRY_DSN	Keep same (GlitchTip is migrated) or update if changed
DOMAIN, API_DOMAIN, etc.	Keep same (DNS will point to new server)
BACKUP_S3_BUCKET/ACCESS/SECRET	Keep same
CERT_ALERT_PHONE	Keep same
All passwords/secrets	Keep same (data was migrated with these credentials)
⚠️ Do NOT rotate JWT_SECRET_KEY or DB_PASSWORD yet. Old sessions and the restored database both use existing credentials. Rotate secrets during a separate planned maintenance window after migration is confirmed stable.

10. TLS Certificates
You use acme.sh with Let's Encrypt, installing certs to /etc/letsencrypt/live/. Nginx reads them directly from the host path.

Strategy: Reissue certs on new server (recommended)
Reissuing is safer than copying private keys across servers. DNS must point to the new server first, OR use DNS-01 challenge.

Option A: HTTP-01 challenge (requires DNS already pointing to new server)

# On new server — run BEFORE starting full docker-compose
# Start nginx on port 80 only (or use standalone)
~/.acme.sh/acme.sh --issue \
  -d $DOMAIN \
  -d www.$DOMAIN \
  -d api.$DOMAIN \
  -d monitoring.$DOMAIN \
  -d sentry.$DOMAIN \
  --webroot /srv/yamata/docker/nginx/public \
  --server letsencrypt

~/.acme.sh/acme.sh --issue -d jaazebeh.ir -d www.jaazebeh.ir \
  --webroot /srv/yamata/docker/nginx/public --server letsencrypt

~/.acme.sh/acme.sh --issue -d jo1n.ir -d www.jo1n.ir \
  --webroot /srv/yamata/docker/nginx/public --server letsencrypt

~/.acme.sh/acme.sh --issue -d joinsahel.ir -d www.joinsahel.ir \
  --webroot /srv/yamata/docker/nginx/public --server letsencrypt

# Install certs to /etc/letsencrypt (same path as old server)
~/.acme.sh/acme.sh --install-cert -d $DOMAIN \
  --cert-file /etc/letsencrypt/live/$DOMAIN/cert.pem \
  --key-file /etc/letsencrypt/live/$DOMAIN/privkey.pem \
  --ca-file /etc/letsencrypt/live/$DOMAIN/chain.pem \
  --fullchain-file /etc/letsencrypt/live/$DOMAIN/fullchain.pem \
  --reloadcmd "docker exec yamata-nginx-beta nginx -s reload"
Option B: Copy certs from old server (for zero-downtime, before DNS cutover)

# On old server — tar the entire letsencrypt directory
sudo tar -czf /tmp/letsencrypt-$(date +%Y%m%d).tar.gz /etc/letsencrypt

# On new server — restore
sudo tar -xzf /tmp/letsencrypt-*.tar.gz -C /
# Also copy acme.sh account + domain config
scp -r old-server:~/.acme.sh/ ~/

# Then configure auto-renewal on new server:
~/.acme.sh/acme.sh --install-cronjob
⚠️ If you copy private keys, delete them from old server after migration is confirmed complete.

11. DNS Records
11.1 Pre-migration: Lower TTL
48 hours before migration, reduce TTL on all records to 60–300 seconds so the cutover propagates fast.


# Example DNS records to update (actual values depend on your registrar)
# Do this 48h early:
$DOMAIN        A   OLD_IP  TTL=300  → change TTL to 60
www.$DOMAIN    A   OLD_IP  TTL=300  → change TTL to 60
api.$DOMAIN    A   OLD_IP  TTL=300  → change TTL to 60
monitoring.$DOMAIN A OLD_IP TTL=300 → change TTL to 60
sentry.$DOMAIN A   OLD_IP  TTL=300  → change TTL to 60
landing.$DOMAIN A  OLD_IP  TTL=300  → change TTL to 60
jaazebeh.ir    A   OLD_IP  TTL=300  → change TTL to 60
www.jaazebeh.ir A  OLD_IP  TTL=300  → change TTL to 60
jo1n.ir        A   OLD_IP  TTL=300  → change TTL to 60
www.jo1n.ir    A   OLD_IP  TTL=300  → change TTL to 60
joinsahel.ir   A   OLD_IP  TTL=300  → change TTL to 60
www.joinsahel.ir A OLD_IP  TTL=300  → change TTL to 60
11.2 Cutover: Point all records to new server

$DOMAIN           A   NEW_IP  TTL=60
www.$DOMAIN       A   NEW_IP  TTL=60
api.$DOMAIN       A   NEW_IP  TTL=60
monitoring.$DOMAIN A  NEW_IP  TTL=60
sentry.$DOMAIN    A   NEW_IP  TTL=60
landing.$DOMAIN   A   NEW_IP  TTL=60
jaazebeh.ir       A   NEW_IP  TTL=60
www.jaazebeh.ir   A   NEW_IP  TTL=60
jo1n.ir           A   NEW_IP  TTL=60
www.jo1n.ir       A   NEW_IP  TTL=60
joinsahel.ir      A   NEW_IP  TTL=60
www.joinsahel.ir  A   NEW_IP  TTL=60
Verify propagation:


# From multiple locations:
dig +short $DOMAIN @1.1.1.1
dig +short $DOMAIN @8.8.8.8
curl -I https://$DOMAIN/api/v1/health
11.3 After migration is stable: Restore TTL

# After 24h of confirmed stable operation, raise TTL back:
$DOMAIN  A  NEW_IP  TTL=3600
# ... all domains
12. docker/ Directory & docker-compose
All files in docker/ are in version control — they transfer with the git clone. But several generated/runtime files are NOT in git:

Files to verify exist on new server:

# These are generated by scripts/deploy-beta.sh — re-run after clone:
ls docker/nginx/sites-available/generated/beta/
# Expected: yamata-beta.conf (generated from yamata-beta.conf template via envsubst)

ls docker/postgres/init-beta-processed.sql
ls docker/postgres/init-database-beta-processed.sql
# Expected: both exist (generated by docker/postgres/process-init-beta.sh)
Regenerate them:


cd /srv/yamata
source .env.beta

# Regenerate processed SQL files
bash docker/postgres/process-init-beta.sh

# Regenerate nginx config
mkdir -p docker/nginx/sites-available/generated/beta
envsubst '$DOMAIN $API_DOMAIN $MONITORING_DOMAIN $SENTRY_UI_DOMAIN' \
  < docker/nginx/sites-available/yamata-beta.conf \
  > docker/nginx/sites-available/generated/beta/yamata-beta.conf
docker-compose.beta.yml — items to verify:
Item	Check
subnet: 172.30.0.0/24	Ensure no conflict with existing Docker networks on new server
ipv4_address assignments	All unique, no collisions
Volume names	Match what you created during data migration
/etc/letsencrypt bind mount	Path exists on new server
image: yamata-no-orochi	Built locally on new server
image: yamata-cert-monitor-beta	Built locally on new server
image: yamata-frontend-beta	Built locally on new server

# Check for subnet conflicts
docker network ls
ip route show | grep 172.30
13. Scripts Directory
All scripts are in version control. But verify these runtime dependencies on the new server:

Script	Dependencies	Notes
deploy-beta.sh	acme.sh, docker, envsubst, openssl	Main deploy script — run this last
init-beta-database.sh	docker running with postgres	Run only for fresh DB; skip since you're restoring from dump
cert_monitor.py	Python 3, requests	Runs as container — check image
nginx_sentry_forwarder.py	Python 3, sentry-sdk	Runs as container
resend_torobpay_sms.py	Python 3, requests	Admin utility — runs on-demand
export_uid_campaign_participation.py	Python 3, psycopg2	Admin utility — runs on-demand

# Verify python deps
pip3 install --break-system-packages requests sentry-sdk psycopg2-binary

# Make scripts executable
chmod +x scripts/deploy-beta.sh scripts/init-beta-database.sh
14. Database Migration Tracker (golang-migrate)
Your app uses golang-migrate. The migration state is stored in the schema_migrations table in PostgreSQL.

After restore, verify state:

docker exec yamata-postgres-beta psql \
  -U "$DB_USER" -d "$DB_NAME" \
  -c "SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1;"
Expected output:


 version | dirty 
---------+-------
      NN | f
(1 row)
dirty = f (false) means the last migration completed cleanly ✅
dirty = t (true) means a migration was interrupted — do not start the app — investigate first
If you need to apply new migrations on the new server:
The app itself runs migrations on startup (check main.go for AutoMigrate or explicit migration call). If it does, just start the app and it applies any pending migrations automatically.

If you run migrations manually:


# Check what version the DB is at vs what migrations exist
ls migrations/ | grep -v down | sort -n

docker exec yamata-postgres-beta psql -U "$DB_USER" -d "$DB_NAME" \
  -c "SELECT version FROM schema_migrations;"
15. First Boot & Smoke Tests
15.1 Startup sequence

cd /srv/yamata

# 1. Start infra first
docker compose -f docker-compose.beta.yml up -d \
  postgres-beta sentry-postgres-beta \
  redis-beta sentry-redis-beta

# 2. Wait for healthy
sleep 15
docker compose -f docker-compose.beta.yml ps

# 3. Start monitoring stack
docker compose -f docker-compose.beta.yml up -d \
  prometheus-beta grafana-beta \
  postgres-exporter-beta node-exporter-beta cadvisor-beta

# 4. Start GlitchTip
docker compose -f docker-compose.beta.yml up -d sentry-beta

# 5. Start app
docker compose -f docker-compose.beta.yml up -d app-beta

# 6. Wait for app healthy
sleep 20
docker compose -f docker-compose.beta.yml ps

# 7. Start Nginx + remaining services
docker compose -f docker-compose.beta.yml up -d \
  nginx-beta nginx-sentry-forwarder-beta cert-monitor-beta frontend-beta

# 8. Start backup service last
docker compose -f docker-compose.beta.yml up -d postgres-backup-beta
15.2 Smoke tests

# Health check
curl -f https://$DOMAIN/api/v1/health
curl -f https://$DOMAIN/health

# Check all containers are running
docker compose -f docker-compose.beta.yml ps --format \
  "table {{.Name}}\t{{.Status}}\t{{.Health}}"

# App logs — look for errors
docker logs yamata-app-beta --tail 50

# Postgres connection
docker exec yamata-postgres-beta psql -U $DB_USER -d $DB_NAME \
  -c "SELECT COUNT(*) FROM customers;"    # adjust table name

# Redis connectivity
docker exec yamata-redis-beta redis-cli PING

# Nginx config test
docker exec yamata-nginx-beta nginx -t

# Cert validity
docker exec yamata-cert-monitor-beta python /app/cert_monitor.py --check-only 2>/dev/null || \
  openssl s_client -connect $DOMAIN:443 -servername $DOMAIN </dev/null 2>/dev/null | \
  openssl x509 -noout -dates

# Short-link domains
curl -I https://jo1n.ir/s/testUID 2>/dev/null | head -5
curl -I https://joinsahel.ir/s/testUID 2>/dev/null | head -5

# Grafana
curl -f https://$MONITORING_DOMAIN/grafana/api/health

# Sentry
curl -f https://$SENTRY_UI_DOMAIN/api/0/projects/ -H "Authorization: Bearer $SENTRY_TOKEN"
15.3 Campaign scheduler check

# Verify scheduler is running (check logs for "campaign execution" lines)
docker logs yamata-app-beta 2>&1 | grep -i "campaign\|scheduler" | tail -20

# Verify CAMPAIGN_EXECUTION_ENABLED=true in env
docker exec yamata-app-beta env | grep CAMPAIGN
16. Monitoring & Rollback Plan
16.1 Active monitoring during migration window

# In a separate terminal, watch all container health
watch -n 5 'docker compose -f docker-compose.beta.yml ps'

# Watch error rate in nginx logs
docker exec yamata-nginx-beta tail -f /var/log/nginx/access.log | \
  grep -E '" [45][0-9][0-9] '

# Watch app logs for panics/errors
docker logs -f yamata-app-beta 2>&1 | grep -E 'ERROR|PANIC|FATAL'
16.2 Rollback criteria
Roll back to old server if, within 1 hour of DNS cutover:

Health endpoint /api/v1/health returning non-200
Database connection errors in app logs
Error rate > 1% of requests (above baseline)
Campaign scheduler not processing jobs
Certificate errors on any domain
16.3 Rollback procedure

# Rollback = repoint DNS back to old server IP
# Do this at your registrar immediately — TTL is 60s so propagation is fast

$DOMAIN  A  OLD_IP
# ... all domains → OLD_IP

# Restart old server's stack (if you stopped it)
# On old server:
docker compose -f docker-compose.beta.yml up -d
Keep the old server running and accessible for at least 48 hours after DNS cutover. Do not decommission it until you are fully confident.

17. Decommission Old Server
Only after 48+ hours of stable production on the new server:


# 1. Take one final backup from new server (confirm it's current)
docker exec yamata-postgres-beta pg_dump \
  -U "$DB_USER" -d "$DB_NAME" -Fc \
  -f /tmp/final-confirmed-$(date +%Y%m%dT%H%M%SZ).dump

# 2. Upload to S3
aws s3 cp /tmp/final-confirmed-*.dump s3://$BACKUP_S3_BUCKET/confirmed-migration/

# 3. On old server — stop everything
docker compose -f docker-compose.beta.yml down

# 4. Revoke old server's SSH keys / API tokens from external services
#    (SMS provider, crypto providers, Bale, Rubika, etc.)

# 5. After final confirmation — delete old server
# (via your hosting provider's control panel)

# 6. Raise DNS TTL back to normal
$DOMAIN  A  NEW_IP  TTL=3600
Migration Checklist Summary

PRE-MIGRATION (48h before):
[ ] Lower all DNS TTLs to 60s
[ ] Run pre-migration backup & verify dumps are valid
[ ] Audit inventory (volumes, certs, domains, image names)
[ ] Provision new server & install Docker

NEW SERVER SETUP:
[ ] OS updated, deploy user created
[ ] SSH hardened, root login disabled
[ ] UFW firewall configured (80, 443, 22)
[ ] fail2ban enabled
[ ] system/tune-server.sh applied + extra sysctl
[ ] Docker installed and tuned (daemon.json)
[ ] acme.sh installed

MIGRATION WINDOW:
[ ] Stop app + backup service on old server
[ ] Final pg_dump of main DB (verify row counts)
[ ] Final pg_dump of Sentry DB
[ ] Redis BGSAVE + copy dump.rdb + appendonly.aof
[ ] rsync uploads volume
[ ] Grafana volume migrated
[ ] TLS certs copied or reissued
[ ] Git clone (or rsync) code to /srv/yamata
[ ] .env.beta copied & verified (checksum)
[ ] Docker images built on new server
[ ] Generated nginx conf + processed SQL files recreated
[ ] PostgreSQL restored & schema_migrations verified (dirty=false)
[ ] Redis data loaded & DBSIZE verified
[ ] Uploads volume restored & file count verified

STARTUP:
[ ] Start infra (postgres, redis)
[ ] Start monitoring stack
[ ] Start GlitchTip
[ ] Start app — no migration errors in logs
[ ] Start nginx + cert-monitor + sentry forwarder
[ ] Start backup service

SMOKE TESTS:
[ ] /api/v1/health returns 200
[ ] All containers healthy (docker compose ps)
[ ] Short-link domains proxying correctly
[ ] Uploads accessible via API
[ ] Grafana dashboards loading
[ ] GlitchTip receiving events
[ ] Campaign scheduler active

DNS CUTOVER:
[ ] Point all A records to new server IP
[ ] Verify propagation (dig @1.1.1.1, @8.8.8.8)
[ ] Test from external network (not old server)
[ ] Monitor error logs for 1 hour

POST-MIGRATION (48h+):
[ ] Raise DNS TTLs back to 3600
[ ] Remove old server SSH keys from services
[ ] Stop old server
[ ] Decommission old server
[ ] Document new server IP, specs, and config changes
Estimated total migration time:

Setup new server: ~2 hours (can be done days ahead)
Data transfer (depending on volume sizes): 10–45 minutes
DNS propagation: 1–5 minutes (with 60s TTL)
Smoke testing: 15–20 minutes
Total active downtime: ~15–30 minutes
