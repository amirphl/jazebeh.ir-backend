# Selective production backup and safe local restore

This is a specialized developer-data workflow, not the full production
migration procedure. For a server move or application recovery, use
[`../PRODUCTION_MIGRATION.md`](../PRODUCTION_MIGRATION.md) and its checked-in
backup/restore scripts. The bundle produced here intentionally contains only
the four tables below and cannot start a complete Yamata deployment by itself.

This runbook backs up these PostgreSQL tables:

- `public.tags`
- `public.src_reference`
- `public.audience_profiles`
- `public.src_layer_all_stats`

It is verified against the current aggregate schema head
`0170_add_bundle_action_file_staging_status.sql`, where the sensitive field is
`public.audience_profiles.phone_number` and the table has these columns, in
order:

```text
id, uid, phone_number, tags, color, created_at, updated_at, normalized_score
```

The recommended path anonymizes every phone number **inside the production
query**. A real phone number is therefore never written to the backup, sent to
the local machine, or briefly restored locally. Every local phone number is a
unique, digits-only, 20-character value computed from the row's unique `id`:

```sql
(70000000000000000000::numeric + id)::text
```

Twenty digits is longer than an E.164 telephone number, so the generated value
cannot be a dialable real phone number. A local check constraint also rejects
anything that is not exactly the expected fake value during restore.

## Important operational properties

- Run the export from a separate transfer machine, and use a read replica when
  one is available. Compression then consumes the transfer machine's CPU, not
  the database server's CPU.
- The export takes `ACCESS SHARE` locks. Normal reads and writes continue, but
  DDL requiring `ACCESS EXCLUSIVE` waits. A five-second `lock_timeout` makes the
  export fail instead of waiting indefinitely for a table lock.
- A long snapshot can delay dead-tuple cleanup. Run off-peak, monitor replica
  lag/I/O and autovacuum health, and end the snapshot immediately after both
  exports finish.
- The commands use one exported snapshot for all four tables. Do not commit or
  close the snapshot-holder session until both export workers are complete.
- `zstd --ultra -20 -T0` is intentionally CPU- and memory-intensive. It is a
  reasonable ultra-compression setting; level 22 is usually much slower and
  conflicts with the requirement that the job finish quickly. If elapsed time
  matters more than the last few percent of size, use level 10 instead.
- The source role needs `SELECT` on all four tables and must see all rows. If
  row-level security is enabled, use an approved backup role that can bypass it.
- Use PostgreSQL client tools from the same major version as production, or a
  newer major version. The local server should be the same or a newer major
  version than the source.
- The backup directory is sensitive even though phone numbers are removed:
  `uid` and all other audience columns remain unchanged. Keep permissions at
  `0700`, encrypt it at rest/in transit, and apply normal backup retention.

## 1. Install/check client tools

The transfer machine needs `psql`, `pg_dump`, `pg_restore`, `zstd`, and
`sha256sum`.

```bash
psql --version
pg_dump --version
pg_restore --version
zstd --version
```

For unattended and parallel connections, put the production and local
credentials in `~/.pgpass` with mode `0600`; do not put passwords in commands or
shell history.

```text
production-host:5432:production-db:backup-user:PRODUCTION_PASSWORD
localhost:5432:somedb:postgres:LOCAL_PASSWORD
```

```bash
chmod 600 ~/.pgpass
```

## 2. Set source connection and create the destination directory

Run this on the transfer machine. Replace the production placeholders. Point
`PGHOST` at the read replica if one is available.

```bash
export PGHOST='production-host'
export PGPORT='5432'
export PGDATABASE='production-db'
export PGUSER='backup-user'
export PGAPPNAME='yamata_masked_backup'
export PGOPTIONS='-c lock_timeout=5s -c statement_timeout=0 -c idle_in_transaction_session_timeout=0'

umask 077
export BACKUP_DIR="$PWD/yamata_masked_$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 700 "$BACKUP_DIR"
printf '%s\n' "$BACKUP_DIR"
```

Do not set `PGPASSWORD` in a shared shell and do not embed it in a URI.

## 3. Production preflight

Confirm the four objects, exact audience column list, row-level-security state,
and positive `bigserial` IDs. The `min(id)` query uses the primary-key index and
does not scan 95 million rows.

```bash
psql -X -v ON_ERROR_STOP=1 <<'SQL'
SELECT current_setting('server_version') AS server_version,
       pg_is_in_recovery() AS running_on_replica;

SELECT to_regclass('public.tags') AS tags,
       to_regclass('public.src_reference') AS src_reference,
       to_regclass('public.audience_profiles') AS audience_profiles,
       to_regclass('public.src_layer_all_stats') AS src_layer_all_stats;

SELECT string_agg(column_name, ', ' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'audience_profiles';

SELECT c.relname, c.relrowsecurity
FROM pg_class AS c
JOIN pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relname IN
      ('tags', 'src_reference', 'audience_profiles', 'src_layer_all_stats')
ORDER BY c.relname;

SELECT min(id) AS minimum_audience_id
FROM public.audience_profiles;
SQL
```

Stop if a table is missing, the audience column list differs from the list at
the top of this file, `minimum_audience_id` is negative, or row-level security
would hide rows. Update the explicit `COPY` column lists only after reviewing a
schema change.

Also confirm there is enough free space on the transfer machine. Compression
ratios vary too much to infer the required space from the row count alone.

## 4. Export one shared snapshot

Open **terminal 1** using the same `PG*` environment and run:

```bash
psql -X -qAt -v ON_ERROR_STOP=1
```

At the `psql` prompt, run:

```sql
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT pg_export_snapshot();
```

Copy the returned value, which looks like `00000003-0000001B-1`, into terminal
2 and terminal 3:

```bash
export SNAPSHOT_ID='paste-the-returned-value-here'
```

Also export the exact absolute `BACKUP_DIR` printed in step 2 in each new
terminal. Shell variables are not automatically shared between terminals.

Leave terminal 1 open in that transaction. If it disconnects or commits, the
snapshot becomes invalid and both exports must be restarted into a new empty
backup directory.

## 5. Export schema plus the three non-sensitive tables

In **terminal 2**, using the same `PG*`, `BACKUP_DIR`, and `SNAPSHOT_ID`, run:

```bash
pg_dump \
  --format=directory \
  --file="$BACKUP_DIR/table_archive" \
  --jobs=3 \
  --compress='zstd:level=20,long' \
  --snapshot="$SNAPSHOT_ID" \
  --strict-names \
  --no-owner \
  --no-privileges \
  --no-tablespaces \
  --table='public.tags' \
  --table='public.src_reference' \
  --table='public.audience_profiles' \
  --table='public.src_layer_all_stats' \
  --table='public.tags_id_seq' \
  --table='public.audience_profiles_id_seq' \
  --exclude-table-data='public.audience_profiles' \
  --verbose \
  2>"$BACKUP_DIR/pg_dump.log"
```

The archive includes the definitions of all four tables and data for the three
safe tables. It deliberately contains no `audience_profiles` rows. Three jobs
means four `pg_dump` connections (leader plus workers); lower `--jobs` if the
production system is I/O constrained.

## 6. Stream an anonymized audience export

At the same time as step 5, run the following in **terminal 3**. It imports the
same shared snapshot and compresses the transformed `COPY` stream on the client.

```bash
cd "$BACKUP_DIR"

PGAPPNAME='yamata_masked_audience_export' \
psql -X -v ON_ERROR_STOP=1 --set=snapshot_id="$SNAPSHOT_ID" <<'SQL'
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET TRANSACTION SNAPSHOT :'snapshot_id';
\copy (SELECT id, uid, (70000000000000000000::numeric + id)::text AS phone_number, tags, color, created_at, updated_at, normalized_score FROM public.audience_profiles) TO PROGRAM 'zstd -T0 --ultra -20 -o audience_profiles.copy.zst'
COMMIT;
SQL
```

Do not use `ORDER BY`; it can make the 95-million-row export slower. PostgreSQL
`COPY` text format safely escapes tabs, newlines, backslashes, arrays, and nulls.

To observe progress from a fourth connection without touching data:

```sql
SELECT pid, command, type, bytes_processed, tuples_processed
FROM pg_stat_progress_copy;
```

## 7. Release the snapshot and validate the bundle

Only after both terminal 2 and terminal 3 exit successfully, return to terminal
1 and run:

```sql
COMMIT;
\q
```

Then validate the archive and compressed stream:

```bash
pg_restore --list "$BACKUP_DIR/table_archive" >/dev/null
zstd --test "$BACKUP_DIR/audience_profiles.copy.zst"

if pg_restore --list "$BACKUP_DIR/table_archive" \
  | grep -Eq 'TABLE DATA[[:space:]]+public[[:space:]]+audience_profiles[[:space:]]'; then
  echo 'ERROR: archive unexpectedly contains audience_profiles table data' >&2
  exit 1
fi

(
  cd "$BACKUP_DIR"
  find table_archive audience_profiles.copy.zst pg_dump.log \
    -type f -print0 \
    | sort -z \
    | xargs -0 sha256sum > SHA256SUMS
)
```

Inspect `pg_dump.log` for warnings. Transfer the entire directory, including
`SHA256SUMS`. The directory-format archive already contains individually
zstd-compressed data files; wrapping it in another high-level compressor wastes
time with little size benefit. A plain `tar` may be used if a single transport
file is required.

## 8. Restore locally

Use `psql` and `pg_restore`, not `pgcli`, for the bulk load. The connection is
the same database the question supplied:

```text
host=localhost port=5432 user=postgres dbname=somedb
```

### Target safety check

The fast path below requires the four target tables not to exist. Prefer a new,
empty local database. If the current `somedb` contains anything important,
create a separate database such as `somedb_restore`; do not use `--clean`,
`DROP ... CASCADE`, or `TRUNCATE ... CASCADE` without separately deciding what
local data and dependent objects may be destroyed.

Stop the local application/workers so they cannot read partially restored data.
Then check the target:

```bash
export PGHOST='localhost'
export PGPORT='5432'
export PGDATABASE='somedb'
export PGUSER='postgres'

psql -X -W -v ON_ERROR_STOP=1 <<'SQL'
SELECT to_regclass('public.tags') AS tags,
       to_regclass('public.src_reference') AS src_reference,
       to_regclass('public.audience_profiles') AS audience_profiles,
       to_regclass('public.src_layer_all_stats') AS src_layer_all_stats;
SQL
```

All four results must be null for the commands below. If they are not, stop and
use a new database or deliberately prepare the existing database first.

Verify the files after transfer:

```bash
(cd "$BACKUP_DIR" && sha256sum --check SHA256SUMS)
```

### Create tables without indexes

Restoring `pre-data` first creates tables and sequences but postpones indexes
and most constraints, which makes the 95-million-row `COPY` substantially
faster.

```bash
pg_restore \
  --host=localhost --port=5432 --username=postgres --dbname=somedb --password \
  --section=pre-data \
  --no-owner --no-privileges --exit-on-error --verbose \
  "$BACKUP_DIR/table_archive"
```

Install a local-only guard before loading any audience row. Because the table is
empty, this is instant; it makes the restore reject real, null, malformed, or
otherwise unexpected phone values.

```bash
psql -X -W -v ON_ERROR_STOP=1 \
  --host=localhost --port=5432 --username=postgres --dbname=somedb <<'SQL'
ALTER TABLE public.audience_profiles
ADD CONSTRAINT local_audience_profiles_fake_phone_guard
CHECK (
  phone_number IS NOT NULL
  AND phone_number = (70000000000000000000::numeric + id)::text
);
SQL
```

### Load table data

Load the three small/safe tables and sequence state:

```bash
pg_restore \
  --host=localhost --port=5432 --username=postgres --dbname=somedb --password \
  --section=data --jobs=3 \
  --no-owner --no-privileges --exit-on-error --verbose \
  "$BACKUP_DIR/table_archive"
```

Load `audience_profiles` directly from the compressed file, without creating an
uncompressed intermediate file:

```bash
cd "$BACKUP_DIR"

psql -X -W -v ON_ERROR_STOP=1 \
  --host=localhost --port=5432 --username=postgres --dbname=somedb \
  --command="\copy public.audience_profiles (id, uid, phone_number, tags, color, created_at, updated_at, normalized_score) FROM PROGRAM 'zstd -dc audience_profiles.copy.zst'"
```

The single `COPY` statement is transactional: a failed load does not leave a
partially loaded `audience_profiles` table.

Finally create the original indexes, unique constraints, and other post-data
objects in parallel:

```bash
pg_restore \
  --host=localhost --port=5432 --username=postgres --dbname=somedb --password \
  --section=post-data --jobs=4 \
  --no-owner --no-privileges --exit-on-error --verbose \
  "$BACKUP_DIR/table_archive"
```

Tune `--jobs=4` to the local CPU, memory, and disk. Too many concurrent index
builds can be slower. Ensure PostgreSQL has ample free disk space because index
creation needs working space in addition to the final database size.

Now that the primary-key index exists, reset the audience sequence defensively.
`max(id)` can use the index instead of scanning the full table:

```bash
psql -X -W -v ON_ERROR_STOP=1 \
  --host=localhost --port=5432 --username=postgres --dbname=somedb <<'SQL'
SELECT setval(
  pg_get_serial_sequence('public.audience_profiles', 'id'),
  GREATEST((SELECT COALESCE(max(id), 0) + 1 FROM public.audience_profiles), 1),
  false
);
SQL
```

### Verify before starting the application

The persistent local check constraint is the primary PII guard. Confirm it is
present and validated, inspect counts, and update optimizer statistics:

```bash
psql -X -W -v ON_ERROR_STOP=1 \
  --host=localhost --port=5432 --username=postgres --dbname=somedb <<'SQL'
SELECT conname, convalidated
FROM pg_constraint
WHERE conrelid = 'public.audience_profiles'::regclass
  AND conname = 'local_audience_profiles_fake_phone_guard';

ANALYZE public.tags;
ANALYZE public.src_reference;
ANALYZE public.audience_profiles;
ANALYZE public.src_layer_all_stats;

SELECT relname AS table_name, reltuples::bigint AS estimated_rows
FROM pg_class
WHERE oid IN (
  'public.tags'::regclass,
  'public.src_reference'::regclass,
  'public.audience_profiles'::regclass,
  'public.src_layer_all_stats'::regclass
)
ORDER BY relname;

SELECT id, phone_number
FROM public.audience_profiles
TABLESAMPLE SYSTEM (0.001)
LIMIT 10;
SQL
```

Expected guard output is one row with `convalidated = t`. Keep this constraint
in the local database; it prevents a later accidental import or update from
introducing a real phone number. The displayed counts are `ANALYZE` estimates,
avoiding another full scan of 95 million rows. Run an exact `count(*)` only if
an exact count is required.

## Failure and retry rules

- If terminal 1 disconnects before both exports finish, discard that incomplete
  backup directory and restart all export steps with a new snapshot.
- If `pg_dump`, `psql`, or `zstd` fails, do not reuse the partial file. `zstd`
  refuses to overwrite by default, which helps prevent accidental reuse.
- If a table lock cannot be acquired in five seconds, investigate DDL and retry
  later; do not remove `lock_timeout` merely to force the backup through.
- Never generate an ordinary `pg_dump` containing
  `audience_profiles.phone_number` as a temporary intermediate.
- Never restore first and promise to anonymize later. The export-time transform
  and pre-load local guard remove that exposure window.
- The existing `scripts/restore-yamata-audience-profiles.sh` expects a different,
  plain SQL dump format and must not be used for this bundle.

## Why this minimizes blocking

PostgreSQL documents that `pg_dump` makes a consistent backup while concurrent
readers and writers continue. The remaining impact is read I/O, CPU for the
mask expression/text conversion, the lifetime of a repeatable-read snapshot,
and `ACCESS SHARE` locks that conflict with schema-changing DDL. A read replica,
off-peak execution, a bounded lock wait, and prompt snapshot release are the
most useful controls. There is no truly zero-impact logical export of 95 million
rows.

References:

- [PostgreSQL 17 `pg_dump`](https://www.postgresql.org/docs/17/app-pgdump.html)
- [PostgreSQL 17 `pg_restore`](https://www.postgresql.org/docs/17/app-pgrestore.html)
- [PostgreSQL 17 `COPY`](https://www.postgresql.org/docs/17/sql-copy.html)
- [PostgreSQL 17 explicit locking](https://www.postgresql.org/docs/17/explicit-locking.html)
- [PostgreSQL 17 snapshot synchronization functions](https://www.postgresql.org/docs/17/functions-admin.html#FUNCTIONS-SNAPSHOT-SYNCHRONIZATION)
