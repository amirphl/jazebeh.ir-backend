# Jazebeh audience CSV import runbook

Use this runbook to import a large `audience_profiles.csv` export that contains
both new and existing audience profiles. It is intended for the Yamata Compose
stack on the production host, where the project directory is `/srv/yamata`.

The checked-in importer is a detached systemd operation. It streams the host
CSV into a PostgreSQL temporary table, validates it, then upserts
`public.audience_profiles` by `id` in one transaction. Existing profiles keep
their original `created_at`; new profiles are inserted; identical rows are not
updated.

## Preconditions and safeguards

- Schedule a maintenance window. `yamata-app-beta` and
  `yamata-campaign-scheduler-beta` must remain stopped from before the import
  starts until it has been verified.
- Do not run two CSV imports at once, or import the accompanying reference-data
  CSVs concurrently.
- Keep `yamata-postgres-beta` running.
- Allow sufficient space for the extracted CSV, a verified pre-import backup,
  PostgreSQL staging data and indexes, WAL, and the final table growth.
- The importer requires this exact CSV header:

  ```text
  id,uid,phone_number,tags,color,created_at,updated_at,normalized_score
  ```

- It refuses duplicate source IDs, UIDs, or non-null phone numbers. It also
  fails before the merge if a CSV UID or phone number belongs to a different
  target ID. Resolve those source-data identity conflicts; do not bypass them.

## 1. Inspect and safely extract the archive

This example uses the export received on 2026-10-12. First inspect it; do not
extract an archive whose member list is unexpected.

```bash
cd /srv/yamata/10-12
sudo tar --zstd -tvf jazebeh_export_20261012_153728.tar.zst
```

The archive should contain only the expected regular files at its root, such as
`audience_profiles.csv`, `tags.csv`, `src_reference.csv`, and
`src_layer_all_stats.csv`. Reject paths beginning with `/`, containing `../`,
symlinks, or unexpected members.

Create a unique private extraction directory. Never overwrite a previous run.

```bash
sudo install -d -m 700 /srv/yamata/10-12/jazebeh-import-20261012
sudo tar --zstd --no-same-owner --no-same-permissions \
  -xf /srv/yamata/10-12/jazebeh_export_20261012_153728.tar.zst \
  -C /srv/yamata/10-12/jazebeh-import-20261012
sudo chmod 600 /srv/yamata/10-12/jazebeh-import-20261012/*.csv
head -n 1 /srv/yamata/10-12/jazebeh-import-20261012/audience_profiles.csv
du -h /srv/yamata/10-12/jazebeh-import-20261012/audience_profiles.csv
```

Check capacity on both the host filesystem and PostgreSQL volume:

```bash
df -h /srv/yamata/10-12
sudo docker volume inspect -f '{{.Mountpoint}}' postgres_data_beta
df -h "$(sudo docker volume inspect -f '{{.Mountpoint}}' postgres_data_beta)"
```

## 2. Verify schema and create a rollback backup

From the project directory, verify migrations without changing the schema:

```bash
cd /srv/yamata
sudo ./scripts/apply-yamata-required-migrations.sh --verify-only
```

Create a custom-format table backup before stopping writers, validate it, and
retain it until normal operation has been confirmed. Choose a backup location
with enough free capacity.

```bash
sudo mkdir -p /srv/yamata/backups
sudo docker exec yamata-postgres-beta sh -c \
  'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc -t public.audience_profiles' \
  | sudo tee /srv/yamata/backups/audience_profiles-pre-jazebeh-20261012.dump >/dev/null
pg_restore --list /srv/yamata/backups/audience_profiles-pre-jazebeh-20261012.dump >/dev/null
```

Record the original table row count and the archive name in the maintenance
ticket or change log.

## 3. Stop writers and launch the detached import

Stop the API and scheduler. This causes a write outage for audience profiles;
leave PostgreSQL up.

```bash
sudo docker stop --time 60 yamata-campaign-scheduler-beta
sudo docker stop --time 60 yamata-app-beta
```

Start the importer through systemd. The explicit unit name makes monitoring and
post-run audit unambiguous. An SSH disconnect does not stop this job.

```bash
cd /srv/yamata
sudo ./scripts/audience-profile-csv-import/run-yamata-audience-profiles-csv-import.sh \
  /srv/yamata/10-12/jazebeh-import-20261012/audience_profiles.csv \
  /srv/yamata \
  yamata-audience-csv-import-20261012 \
  --confirm-maintenance-window
```

The importer stages the CSV, creates temporary identity indexes, validates
source/target identities, emits planned insert/update/unchanged counts, then
acquires an `EXCLUSIVE` table lock for the atomic upsert and ID-sequence reset.
It runs `VACUUM (ANALYZE)` after the committed merge.

## 4. Monitor progress and errors

Follow the durable systemd journal:

```bash
sudo journalctl -u yamata-audience-csv-import-20261012 -f -o cat
```

`Ctrl-C` exits only the journal viewer. It does not interrupt the systemd job.

For a five-second status and database-progress view:

```bash
watch -n 5 'sudo /srv/yamata/scripts/audience-profile-csv-import/monitor-yamata-audience-profiles-csv-import.sh /srv/yamata yamata-audience-csv-import-20261012'
```

During staging, `COPY progress` reports bytes, tuples, and percent. PostgreSQL
does not provide an equivalent reliable percentage for index building,
validation, merge, or vacuum. Use these journal phase markers:

```text
=== audience CSV staging started ===
=== audience CSV staged; validating source keys ===
=== validation passed; atomic merge starting ===
=== audience merge committed ===
=== vacuum/analyze started ===
=== audience CSV import complete ===
```

Check final unit status:

```bash
sudo systemctl show yamata-audience-csv-import-20261012 \
  -p ActiveState -p SubState -p Result -p ExecMainStatus
```

While `ActiveState=active` and `SubState=running`, the import is still in
progress. `Result=success` is only conclusive once the service has exited. A
successful run must have `ExecMainStatus=0` and both `audience merge committed`
and `audience CSV import complete` in its journal.

## 5. Failure handling and completion

Do not restart application writers when the unit fails. Inspect the full log:

```bash
sudo journalctl -u yamata-audience-csv-import-20261012 -o cat --no-pager
```

If it fails before `=== audience merge committed ===`, the target table was not
modified: staging and validation happen before the transaction. Correct the
reported input, capacity, or lock problem and start a new unit after verifying
the failed unit is no longer running.

If `=== audience merge committed ===` is present but vacuum fails, the import
itself has succeeded. Keep writers stopped, investigate the vacuum error, and
run `VACUUM (ANALYZE) public.audience_profiles` through a controlled `psql`
session before reopening the application.

Before restarting writers, inspect the final table count and maintenance
statistics in a PostgreSQL session:

```bash
sudo docker exec -it yamata-postgres-beta sh -lc \
  'psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

```sql
SELECT count(*) AS audience_profiles_total
FROM public.audience_profiles;

SELECT n_live_tup, n_dead_tup, last_analyze, last_vacuum
FROM pg_stat_all_tables
WHERE relid = 'public.audience_profiles'::regclass;
```

Compare journaled planned totals with the expected export totals. After the
successful completion marker and verification, restart only writers that were
previously running:

```bash
sudo docker start yamata-app-beta
sudo docker start yamata-campaign-scheduler-beta
```

Keep the archive, extraction directory, journal, and pre-import backup until
application health and campaign targeting have been confirmed. The accompanying
`tags.csv`, `src_reference.csv`, and `src_layer_all_stats.csv` are a separate
reference-data import, documented in `scripts/README.md`; do not run it during
the audience-profile import.
