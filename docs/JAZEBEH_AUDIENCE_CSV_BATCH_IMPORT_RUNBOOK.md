# Resumable Jazebeh audience CSV batch import

Use this procedure when `audience_profiles.csv` is too large for the
single-transaction import window. The Fish runner commits exactly 100,000 CSV
records per PostgreSQL transaction and can resume from its state file on a
later night.

This is an `audience_profiles.csv` importer only. `tags.csv`,
`src_reference.csv`, and `src_layer_all_stats.csv` have different schemas and
must use the separate reference-data importer; never run that importer at the
same time.

## Semantics and safety guarantees

- Each batch is staged, validated, and committed atomically. A failed,
  interrupted, or time-limited batch is rolled back and is not marked complete.
- The state file advances only after PostgreSQL has committed the batch. A
  restart may safely replay a just-committed batch if the host failed before
  state was written; the upsert is idempotent for identical source rows.
- The state is bound to the CSV's SHA-256, byte count, and header. The runner
  refuses a changed file; use a new state file for a different export.
- The source uses one physical line per CSV record. This is valid for the
  Jazebeh audience export: its typed fields do not permit embedded newlines.
- The runner does **not** stop `yamata-app-beta` or
  `yamata-campaign-scheduler-beta`. It uses serializable transactions. A live
  target row with a later `updated_at` than the source is preserved and counted
  as `skipped_newer`; source identity conflicts and serialization/lock errors
  stop the run without advancing state.
- While it runs, no other process may insert `audience_profiles` or consume
  that table's ID sequence. The importer supplies explicit CSV IDs and advances
  the sequence after each committed batch; concurrent `nextval` calls could
  otherwise make a sequence reset unsafe. Keeping the app service running is
  appropriate only when its overnight behavior has this property.
- This is consistency-oriented, not a promise of zero application impact. A
  concurrent write to the same profile can cause a batch to wait briefly or
  fail safely. Do not auto-retry a failed batch until its journal error is
  understood.

## Start a nightly run

The runner permits starts only from 23:00 through 07:59, Asia/Tehran. It will
not start another batch unless at least three minutes remain before 08:00; this
is a safety margin above the observed two-minute batch duration. It logs
`WINDOW_CLOSED` and exits successfully, leaving the next unstarted batch for a
later night. PostgreSQL's per-batch deadline remains a final safety net if a
batch is unexpectedly slow.

Choose one unique state file per CSV export. Run this command as root through
`sudo`; it starts a detached systemd unit, so the terminal can disconnect.

```bash
cd /srv/yamata
sudo ./scripts/audience-profile-csv-import/run-yamata-audience-profiles-batched.fish \
  --start \
  /srv/yamata/10-12/jazebeh-import-20261012/audience_profiles.csv \
  /srv/yamata \
  /srv/yamata/var/jazebeh-20261012-audience-batch.state \
  yamata-audience-batch-import-20261012
```

The run uses 100,000 rows per batch and processes sequentially. Do not launch
another unit using the same state file; the runner takes an exclusive lock on
it.

## Monitor progress and errors

Follow the journal:

```bash
sudo journalctl -u yamata-audience-batch-import-20261012 -f -o cat
```

Every committed batch produces a durable line like:

```text
BATCH_SUCCESS batch=17 rows=100000 completed_rows=1700000 next_source_line=1700002 BATCH_PLAN rows=100000 existing=… inserts=… updates=… unchanged=… skipped_newer=…
```

The state file is also a concise progress checkpoint:

```bash
sudo cat /srv/yamata/var/jazebeh-20261012-audience-batch.state
```

Inspect the unit outcome without following logs:

```bash
sudo systemctl show yamata-audience-batch-import-20261012 \
  -p ActiveState -p SubState -p Result -p ExecMainStatus
```

`WINDOW_CLOSED` means the run paused before 08:00 at a batch boundary; it is an
intentional pause, not a completed import. `IMPORT_COMPLETE` means the final
partial or full batch has committed.

## Stop and resume

To stop manually, stop the systemd unit. Do not use `kill -9` unless the host
is already unhealthy: normal systemd stop signals the active `psql` client,
which causes PostgreSQL to roll back the active transaction.

```bash
sudo systemctl stop yamata-audience-batch-import-20261012
```

The journal records the interruption and the state remains at the last
successful batch. Resume on a later night by invoking the same `--start`
command with the same CSV and state file. Use a new unit name for each attempt
if the old unit still exists.

If `BATCH_FAILED` appears, do not resume immediately. Read the complete journal
first:

```bash
sudo journalctl -u yamata-audience-batch-import-20261012 -o cat --no-pager
```

Identity conflicts, duplicate identities within a batch, malformed CSV, a
serialization failure, a lock timeout, and insufficient space are intentional
fail-stop conditions. Fix or explicitly resolve the reported cause, then start
again with the same state file. The failed batch is replayed; prior successful
batches are not replayed.
