# PostgreSQL Online Vacuum and Statistics Maintenance

This runbook is for safe, online PostgreSQL 15 `VACUUM (ANALYZE)` maintenance
of the Yamata primary database. Use it for missing statistics and dead-row
cleanup on `audience_profiles` and `short_links`.

## Scope and safety

Standard `VACUUM (ANALYZE)` permits normal application reads and writes. It
reuses dead-row space inside the table and refreshes planner statistics, but
normally does not reduce host filesystem usage. It can be cancelled safely;
only incomplete maintenance work is abandoned.

Run only one large table at a time. Two concurrent jobs are data-safe but
compete for storage I/O and can make production slower.

`VACUUM FULL`, non-concurrent `REINDEX`, and `REINDEX DATABASE` are excluded:
they require a separately approved downtime plan.

## Prerequisites

Run in Bash on the production host:

```bash
bash
DB_USER="$(docker exec yamata-postgres-beta printenv POSTGRES_USER)"
DB_NAME="$(docker exec yamata-postgres-beta printenv POSTGRES_DB)"
```

## Start an asynchronous job

The transient systemd unit survives shell disconnection. Its `PGAPPNAME`
identifies the PostgreSQL backend for monitoring and cancellation.

### `audience_profiles`

```bash
sudo systemd-run \
  --unit=yamata-vacuum-audience-profiles \
  --description='Yamata online VACUUM ANALYZE: audience_profiles' \
  --property=Type=exec --property=Restart=no \
  --property=TimeoutStartSec=infinity --property=KillSignal=SIGINT \
  /usr/bin/docker exec -e PGAPPNAME=yamata-vacuum-audience-profiles \
  yamata-postgres-beta \
  psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" \
  -c 'VACUUM (VERBOSE, ANALYZE) public.audience_profiles;'
```

### `short_links`

Start this only after the audience job is inactive:

```bash
sudo systemctl status yamata-vacuum-audience-profiles --no-pager
```

```bash
sudo systemd-run \
  --unit=yamata-vacuum-short-links \
  --description='Yamata online VACUUM ANALYZE: short_links' \
  --property=Type=exec --property=Restart=no \
  --property=TimeoutStartSec=infinity --property=KillSignal=SIGINT \
  /usr/bin/docker exec -e PGAPPNAME=yamata-vacuum-short-links \
  yamata-postgres-beta \
  psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" \
  -c 'VACUUM (VERBOSE, ANALYZE) public.short_links;'
```

## Monitor

Follow logs:

```bash
sudo journalctl -fu yamata-vacuum-audience-profiles -o cat
sudo journalctl -fu yamata-vacuum-short-links -o cat
```

PostgreSQL 15 exposes heap scanning progress, but not a reliable percentage
for index cleanup. A 100% heap value while the phase is `vacuuming indexes`
means the heap scan completed; index work remains.

```bash
watch -n 5 "docker exec yamata-postgres-beta psql -X -P pager=off -U \"$DB_USER\" -d \"$DB_NAME\" -c \"
SELECT a.application_name, p.phase, p.heap_blks_total,
       p.heap_blks_scanned, p.heap_blks_vacuumed,
       p.index_vacuum_count, p.num_dead_tuples, p.max_dead_tuples,
       CASE WHEN p.heap_blks_total > 0
            THEN round(100.0 * p.heap_blks_scanned / p.heap_blks_total, 1)
       END AS heap_scan_percent
FROM pg_stat_progress_vacuum p
JOIN pg_stat_activity a USING (pid)
WHERE a.application_name IN (
  'yamata-vacuum-audience-profiles', 'yamata-vacuum-short-links')
ORDER BY a.application_name;\""
```

`Ctrl+C` stops only `watch`; it does not stop the vacuum.

## Cancel safely

Cancel the PostgreSQL backend, not the PostgreSQL container or host. To cancel
the `short_links` job:

```bash
docker exec yamata-postgres-beta \
  psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" -c "
    SELECT pg_cancel_backend(pid)
    FROM pg_stat_activity
    WHERE application_name = 'yamata-vacuum-short-links'
      AND state <> 'idle';"
```

Replace the application name with `yamata-vacuum-audience-profiles` to cancel
that job. The corresponding unit may show `failed` with `canceling statement
due to user request`; this is expected after intentional cancellation.

## Duration and capacity expectations

The initial heap scan is only one phase. In the November 2026 production run,
`audience_profiles` scanned about 3.2 million 8 KiB blocks (about 24.6 GiB)
and `short_links` about 4.7 million blocks (about 36.2 GiB). The two jobs ran
concurrently and completed in about 11 minutes each (694.52 and 638.55
seconds), including index cleanup. Their index footprints were approximately
51 GiB and 40 GiB. Treat this as a useful baseline rather than a guarantee:
the exact duration depends on storage I/O and concurrent production load, not
primarily the 36 CPU cores.

## Prevent recurrence

The checked-in configuration is appropriate for the mixed 64 GiB production
host: 16 GiB `shared_buffers`, 48 GiB `effective_cache_size`, 2 GiB
`maintenance_work_mem`, 1 GiB `autovacuum_work_mem`, and five autovacuum
workers. Do not raise these global values simply because a manual vacuum runs.

After a successful maintenance run, set lower per-table thresholds. This is
online and does not require a PostgreSQL restart:

```bash
docker exec -i yamata-postgres-beta \
  psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" <<'SQL'
ALTER TABLE public.short_links SET (
  autovacuum_vacuum_threshold = 50000,
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_analyze_threshold = 50000,
  autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE public.audience_profiles SET (
  autovacuum_vacuum_threshold = 50000,
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_analyze_threshold = 50000,
  autovacuum_analyze_scale_factor = 0.02
);
SQL
```

For `short_links`, this makes vacuum eligible at approximately 50,000 plus 2%
of estimated live rows, instead of the normal 20% scale factor. Review
autovacuum logs and dead-row counts after one to two weeks before retuning.

## Verify completion

```bash
docker exec yamata-postgres-beta \
  psql -X -P pager=off -U "$DB_USER" -d "$DB_NAME" -c "
    SELECT relname, n_live_tup, n_dead_tup,
           last_vacuum, last_autovacuum,
           last_analyze, last_autoanalyze
    FROM pg_stat_user_tables
    WHERE relname IN ('audience_profiles', 'short_links');"
```

If actual host-space recovery is still required after this online maintenance,
assess retention, `pg_repack`, or table-specific `VACUUM FULL` as a separate
downtime project.
