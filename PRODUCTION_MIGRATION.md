# Production migration runbook

This is the canonical procedure for moving the existing beta production stack
to a fresh server. The API always has `CAMPAIGN_EXECUTION_ENABLED=false`,
`SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true`, and smart-tag evaluation
scheduling enabled. All campaign schedulers run only in
`yamata-campaign-scheduler-beta` with `CAMPAIGN_SCHEDULER_ROLE=all`.

## 1. Prepare the destination

Use the same CPU architecture as the source. Install Docker Engine, Docker
Compose v2, and the restore utilities:

```bash
sudo apt-get update
sudo apt-get install -y gettext-base openssl pv python3
docker info
docker compose version
```

The destination must not already contain Yamata containers, the target project
directory, or the source Compose-prefixed volumes. Port 80/443 and Docker subnet
`172.30.0.0/24` must be available. Keep enough free space for the compressed
backup, its extracted copy, restored Docker volumes, and PostgreSQL WAL.

Before cutover, reduce the DNS TTL. Do not point DNS to the destination yet.

## 2. Create the two external PostgreSQL 17 dumps

On the Windows PostgreSQL 17 source, configure and run:

```text
scripts\backup-yamata-audience-profiles.bat
scripts\backup-yamata-scheduler-runtime-data.bat
```

The first dump contains only `audience_profiles`. The second contains exactly:

```text
audience_selections
bundle_audience_selections
bundle_audience_selection_members
campaign_audience_tag_attributions
bale_status_results
rubika_status_results
campaign_status_jobs
processed_campaigns
sent_bale_messages
sent_sms
sent_splus_messages
sms_status_results
splus_status_results
sequence_counters
src_layer_all_stats
src_reference
```

`sent_rubika_messages` is intentionally excluded. Transfer both plain SQL
dumps to the destination. The restore scripts remove PostgreSQL 17's
`transaction_timeout` statement before loading into PostgreSQL 15 and verify
the exact table/column set first.

Create and transfer these large dumps before the production downtime begins.

## 3. Create the online source backup

Copy the current scripts to the source project, then run:

```bash
cd /srv/yamata
chmod +x backup-yamata-server.sh
./backup-yamata-server.sh \
  --project-dir /srv/yamata \
  --output-dir "$HOME"
```

This does not stop, restart, or otherwise interrupt production. It requests
background Redis snapshots (never synchronous `SAVE`), copies the newest completed dump from `postgres_backups_beta` as the
main database backup (so it never captures the current state of
`yamata-postgres-beta`), takes a consistent online dump of Sentry, and archives
the remaining Docker volumes. It also includes uploads, logs, Grafana,
Prometheus, pgAdmin data, certificates, the
complete project working tree (including `.env.beta` and Git-ignored files),
file-backed secrets/certificates referenced by the environment, Docker images,
manifests, `/etc/hosts`, and the source Docker daemon configuration when present.

To use a particular verified nightly dump instead of the automatic selection,
pass its absolute path:

```bash
./backup-yamata-server.sh --project-dir /srv/yamata \
  --output-dir "$HOME" \
  --main-db-backup /absolute/path/to/DB_NAME-YYYYMMDDTHHMMSSZ.dump
```

The resulting archive is an online point-in-time migration artifact, not an
atomically frozen whole-server image: writes made after last night's main
database dump are intentionally absent, and frequently changing non-database
volumes (for example Prometheus data and logs) are not globally atomic. Do not
use `--leave-stopped`; it is kept only for compatibility and is ignored.

The seven-day `postgres_backups_beta` history is intentionally not copied by
default: the selected, validated nightly dump is already stored as
`db/yamata-main.dump`. Add `--include-postgres-backup-history` only when enough
extra space is available and retaining every historical dump is required.

Transfer these three files to the destination:

```text
yamata-backup-YYYYMMDDTHHMMSSZ.tar.gz
yamata-backup-YYYYMMDDTHHMMSSZ.tar.gz.sha256
restore-yamata-backup.sh
```

Verify the transfer:

```bash
sha256sum -c yamata-backup-YYYYMMDDTHHMMSSZ.tar.gz.sha256
```

Keep the source running while the archive is copied, restored, and validated.
Only stop it during the final DNS cutover, after accepting the intentional
main-database recovery point from the nightly dump.

## 4. Restore the server backup

```bash
chmod +x restore-yamata-backup.sh
./restore-yamata-backup.sh \
  "$HOME/yamata-backup-YYYYMMDDTHHMMSSZ.tar.gz" \
  /srv/yamata
```

The restore discovers the original Compose project prefix, restores the actual
prefixed volumes (including pgAdmin and, when explicitly included,
`postgres_backups_beta`), restores
`/etc/hosts` when it is present in the archive, validates certificates without issuing or renewing them,
fixes prompt-file readability, applies the idempotent migrations needed through
the required subset's current endpoint (0152), and starts the API
with campaign execution disabled. The periodic PostgreSQL backup and isolated
scheduler remain stopped until data imports finish.

Empty optional log/metrics/upload archives may produce a warning. An empty
repository, certificate, or Redis archive is not acceptable and the canonical
backup/restore scripts reject it.

Install the operational helpers:

```bash
cd /srv/yamata
chmod +x scripts/*.sh
./scripts/install-yamata-operations.sh
```

Do not run `scripts/init-beta-database.sh` or `scripts/deploy-beta.sh` during
this initial restore.

## 5. Restore the two large table dumps

Check Docker disk space and enable temporary checkpoint tuning:

```bash
sudo df -h "$(sudo docker info --format '{{.DockerRootDir}}')"
sudo /usr/local/sbin/tune-yamata-restore enable
sudo docker stop yamata-campaign-scheduler-beta 2>/dev/null || true
sudo docker stop yamata-app-beta
```

Restore audience profiles first:

```bash
sudo /usr/local/sbin/run-yamata-data-restore \
  audience \
  /absolute/path/audience_profiles.dump \
  /srv/yamata \
  yamata-audience-restore-v1
```

After it succeeds, restore scheduler data:

```bash
sudo /usr/local/sbin/run-yamata-data-restore \
  scheduler \
  /absolute/path/scheduler_runtime.dump \
  /srv/yamata \
  yamata-scheduler-runtime-restore-v1
```

Monitor either unit:

```bash
sudo journalctl -u UNIT_NAME -f -o cat
sudo journalctl -u UNIT_NAME -o cat --no-pager | grep 'progress=' | tail -1
sudo systemctl show UNIT_NAME \
  -p ActiveState -p SubState -p Result -p ExecMainStatus
```

Completion means `ActiveState=inactive`, `Result=success`, and
`ExecMainStatus=0`. `Result=success` while `ActiveState=active` only means no
failure has occurred yet. `SET`, `COPY n`, and row-count lines are normal.

The API and dedicated scheduler must remain stopped so no concurrent writer can
invalidate the emptiness checks or sequence resets. Both imports are atomic; on
failure, fix the cause and rerun under a new unit name.

The scheduler restore preserves `audience_profiles`, restores
`audience_selections`, and atomically replaces `sequence_counters`. Every other
target table must be empty. Plain SQL `COPY` is single-threaded; additional CPU
cores do not make one import parallel.

Reset temporary tuning even if an import failed:

```bash
sudo /usr/local/sbin/tune-yamata-restore reset
sudo docker start yamata-app-beta
```

## 6. Validate and cut over

Run the complete read-only check after starting the scheduler in the next step.
Before DNS cutover, test Nginx directly:

```bash
curl --resolve 'jazebeh.ir:443:NEW_SERVER_IP' \
  'https://jazebeh.ir/api/v1/health'
```

Check key imported relationships:

```bash
sudo docker exec yamata-postgres-beta sh -lc \
  'psql -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "
   SELECT count(*) AS audience_profiles FROM public.audience_profiles;
   SELECT count(*) AS audience_selections FROM public.audience_selections;
   SELECT count(*) AS processed_campaigns FROM public.processed_campaigns;
   SELECT * FROM public.sequence_counters;
   SELECT count(*) AS broken_fk
   FROM public.processed_campaigns p
   LEFT JOIN public.audience_selections a ON a.id=p.audience_selection_id
   WHERE p.audience_selection_id IS NOT NULL AND a.id IS NULL;
   SELECT count(*) AS broken_bundle_fk
   FROM public.processed_campaigns p
   LEFT JOIN public.bundle_audience_selections b
     ON b.id=p.bundle_audience_selection_id
   WHERE p.bundle_audience_selection_id IS NOT NULL AND b.id IS NULL;"'
```

Both broken-FK counts must be zero.

## 7. Start isolated campaign execution

```bash
cd /srv/yamata
./scripts/deploy-campaign-scheduler-beta.sh all yamata-no-orochi
docker compose --env-file .env.beta -f docker-compose.beta.yml \
  up -d postgres-backup-beta
sudo /usr/local/sbin/check-yamata-production /srv/yamata
sudo docker logs -f yamata-campaign-scheduler-beta
```

The scheduler publishes no ports, restarts with `unless-stopped`, and persists
file logs in `yamata-campaign-scheduler-logs-beta`, including separate
`payamsms_scheduler.log`, `candoo_scheduler.log`, `bale_scheduler.log`,
`rubika_scheduler.log`, and `splus_scheduler.log` files. It shares PostgreSQL,
Redis, and the Compose network with the API. Its bot client uses
`http://app-beta:8080`; do not map `jazebeh.ir` to `172.30.0.20` for HTTPS.
Port 443 belongs to `yamata-nginx-beta` (`172.30.0.30`), not the app container.

After validation, point DNS to the destination and repeat the public health
checks. Keep the source server stopped until the acceptance window ends.

## 8. Future deployments and environment changes

Always deploy production with:

```bash
cd /srv/yamata
./scripts/deploy-production-beta.sh --domain jazebeh.ir
```

This recreates the API, then recreates the standalone scheduler from the same
image and the API's effective environment. A Docker restart does not reload
environment variables. After changing `.env.beta`—including `BOT_PASSWORD`—the
production wrapper must run. Keep `CAMPAIGN_EXECUTION_ENABLED=false`,
`SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true`,
`SMART_TAG_EVALUATION_ENABLED=true`, and
`SMART_TAG_EVALUATION_SCHEDULER_ENABLED=true` in `.env.beta`.

Verify the effective settings without exposing the password:

```bash
sudo docker exec yamata-app-beta printenv CAMPAIGN_EXECUTION_ENABLED
sudo docker exec yamata-campaign-scheduler-beta printenv CAMPAIGN_EXECUTION_ENABLED
sudo docker exec yamata-app-beta printenv SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED
sudo docker exec yamata-campaign-scheduler-beta printenv SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED
sudo docker exec yamata-app-beta printenv SMART_TAG_EVALUATION_SCHEDULER_ENABLED
sudo docker exec yamata-campaign-scheduler-beta printenv SMART_TAG_EVALUATION_SCHEDULER_ENABLED
sudo docker exec yamata-app-beta sh -c 'printf %s "$BOT_PASSWORD" | sha256sum'
sudo docker exec yamata-campaign-scheduler-beta sh -c 'printf %s "$BOT_PASSWORD" | sha256sum'
```

Expected campaign-execution values are `false` and `true`. Expected exact-capacity
and smart-tag scheduler values are `true` and `false`. Password hashes must match.

## Certificate policy

Migration restores and validates `/etc/letsencrypt`; it never runs `acme.sh`,
issues certificates, edits crontab, or installs renewal jobs. Check at any time:

```bash
sudo YAMATA_CERT_MIN_VALID_DAYS=7 \
  /usr/local/sbin/check-yamata-certificates \
  /srv/yamata/docker/nginx/sites-available/generated/beta/yamata.conf
```

`yamata-cert-monitor-beta` only checks expiry and alerts; it does not renew.
Configure renewal separately under an authorized system account before the
restored certificates expire.

## Common failures

- `processed_campaigns.bundle_audience_selection_id does not exist`: stop the
  scheduler, run `sudo /usr/local/sbin/apply-yamata-required-migrations
  /srv/yamata`, then start a new restore unit.
- `Migration 0119 must already be applied`: do not bypass the check. Migration
  0119 truncates smart-tag evaluation history; review and apply it deliberately,
  then rerun the required-migrations helper.
- `open SMART_TAG_...: permission denied`: run `chmod 644
  /srv/yamata/SMART_TAG_EVALUATION_*` and redeploy with the production wrapper.
- `https://jazebeh.ir ... 172.30.0.20:443: connection refused`: remove that
  host mapping and redeploy the scheduler; its canonical bot URL is
  `http://app-beta:8080`.
- An `.env.beta` change is absent from the scheduler: a restart is insufficient;
  run `./scripts/deploy-production-beta.sh --domain jazebeh.ir`.
- A failed transient restore unit cannot be reused: fix the root error and use a
  new unit name. Atomic imports roll back before returning failure.
