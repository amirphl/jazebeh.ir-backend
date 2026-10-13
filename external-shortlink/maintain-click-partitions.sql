-- Click-store partition maintenance.
--
-- Run by deploy/external-shortlink-partitions.service as the PostgreSQL
-- administrator, because the runtime role deliberately has no DDL privileges.
-- `retention_days` and `precreate_days` are supplied with psql --set so that one
-- acknowledged-retention setting governs the whole click store: day partitions,
-- deduplication rows, and (in the service) the legacy click rows.
--
-- Responsibilities:
--   1. keep the clicks_v2 default partition present. It is the safety net that
--      stops a missing day partition from rejecting clicks, which would be a
--      total redirect outage because the redirect path is fail-closed.
--   2. create the day partitions in the rolling window, relocating any rows the
--      default partition already holds for those days: PostgreSQL refuses to
--      attach a range while its default partition holds rows in that range.
--   3. drop only partitions whose rows production has acknowledged and whose
--      day is already outside the retention window.
--   4. prune deduplication rows on the same retention.
--   5. report rows still stranded in the default partition, which is the signal
--      that the partition window fell behind.
--   6. re-assert the runtime role's privileges on the click store.

\set ON_ERROR_STOP on

\o /dev/null
SELECT set_config('yamata.partition_retention_days', :'retention_days', false);
SELECT set_config('yamata.partition_precreate_days', :'precreate_days', false);
SELECT set_config('yamata.runtime_role', :'runtime_role', false);
\o

DO $maintenance$
DECLARE
    retention_days integer := current_setting('yamata.partition_retention_days')::integer;
    precreate_days integer := current_setting('yamata.partition_precreate_days')::integer;
    runtime_role text := current_setting('yamata.runtime_role');
    acknowledged_id bigint;
    partition_day date;
    partition_name text;
    partition_max_id bigint;
    partition record;
    default_rows bigint;
    default_oldest timestamptz;
BEGIN
    IF retention_days < 1 THEN
        RAISE EXCEPTION 'click retention must be at least one day, got %', retention_days;
    END IF;
    IF precreate_days < 1 THEN
        RAISE EXCEPTION 'partition pre-creation must cover at least one day, got %', precreate_days;
    END IF;

    -- Step 1: the default partition. Installations predating it get it here so a
    -- stale deployment cannot turn a missing day partition into an outage.
    IF to_regclass('public.clicks_v2_default') IS NULL THEN
        BEGIN
            CREATE TABLE IF NOT EXISTS clicks_v2_default PARTITION OF clicks_v2 DEFAULT;
            RAISE WARNING 'click partition maintenance created the missing clicks_v2 default partition';
        EXCEPTION WHEN OTHERS THEN
            RAISE WARNING 'click partition maintenance could not create the clicks_v2 default partition: %', SQLERRM;
        END;
    END IF;

    SELECT through_click_id INTO acknowledged_id
    FROM click_acknowledgements
    WHERE singleton = TRUE;

    -- Staging table for rows that have to leave the default partition before a
    -- day partition can be attached. It lives for this transaction only.
    CREATE TEMP TABLE IF NOT EXISTS click_partition_overflow (
        LIKE clicks_v2
    ) ON COMMIT DROP;

    -- Step 2: every day in the rolling window, plus every day already stranded
    -- in the default partition, so a window that fell behind catches up.
    FOR partition_day IN
        SELECT needed.day
        FROM (
            SELECT generate_series(
                current_date - 8,
                current_date + precreate_days,
                interval '1 day'
            )::date AS day
            UNION
            SELECT DISTINCT clicked_at::date AS day FROM clicks_v2_default
        ) AS needed
        ORDER BY needed.day
    LOOP
        partition_name := format('clicks_v2_%s', to_char(partition_day, 'YYYYMMDD'));
        CONTINUE WHEN to_regclass(format('public.%I', partition_name)) IS NOT NULL;

        BEGIN
            -- Stage the rows the default partition holds for this day, create the
            -- partition, then route them back into it. The staging table exists
            -- for this transaction only, and any failure rolls the whole step
            -- back with the rows still safely in the default partition.
            DELETE FROM click_partition_overflow;
            WITH moved AS (
                DELETE FROM clicks_v2_default
                WHERE clicked_at >= partition_day
                  AND clicked_at < partition_day + 1
                RETURNING *
            )
            INSERT INTO click_partition_overflow
            SELECT * FROM moved;

            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS %I PARTITION OF clicks_v2 FOR VALUES FROM (%L) TO (%L)',
                partition_name,
                partition_day,
                partition_day + 1
            );

            WITH moved AS (
                DELETE FROM click_partition_overflow
                RETURNING *
            )
            INSERT INTO clicks_v2
            SELECT * FROM moved;

            RAISE NOTICE 'click partition maintenance ensured partition %', partition_name;
        EXCEPTION WHEN OTHERS THEN
            -- One bad day must not stop the rest of the window or retention.
            RAISE WARNING 'click partition maintenance deferred partition %: %', partition_name, SQLERRM;
        END;
    END LOOP;

    -- Step 3: retention. A partition is dropped only once every row in it has
    -- been acknowledged by production, so retention can never outrun the import.
    FOR partition IN
        SELECT child.relname AS name
        FROM pg_inherits
        JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
        JOIN pg_class child ON child.oid = pg_inherits.inhrelid
        WHERE parent.relname = 'clicks_v2'
          AND child.relname ~ '^clicks_v2_[0-9]{8}$'
          AND to_date(right(child.relname, 8), 'YYYYMMDD') < current_date - retention_days
        ORDER BY child.relname
    LOOP
        EXECUTE format('SELECT max(click_id) FROM %I', partition.name) INTO partition_max_id;
        IF partition_max_id IS NULL OR partition_max_id <= acknowledged_id THEN
            EXECUTE format('DROP TABLE %I', partition.name);
            RAISE NOTICE 'click partition maintenance dropped acknowledged partition %', partition.name;
        ELSE
            RAISE WARNING 'click partition maintenance retained partition % because production has not acknowledged it yet', partition.name;
        END IF;
    END LOOP;

    -- Step 4: deduplication rows follow the retention of the click rows they
    -- guard, and are only removed once the guarded row is gone.
    DELETE FROM click_event_dedup AS dedup
    WHERE dedup.clicked_at < CURRENT_TIMESTAMP - make_interval(days => retention_days)
      AND NOT EXISTS (
          SELECT 1 FROM clicks_v2 AS click WHERE click.event_id = dedup.event_id
      );

    -- Step 5: rows that could not be moved into a day partition are never
    -- dropped, because production may not have imported them yet. They are the
    -- alertable signal that the partition window is behind.
    IF to_regclass('public.clicks_v2_default') IS NOT NULL THEN
        SELECT count(*), min(clicked_at)
        INTO default_rows, default_oldest
        FROM clicks_v2_default;
        IF default_rows > 0 THEN
            RAISE WARNING
                'click partition maintenance left % click(s) in the clicks_v2 default partition (oldest %); the partition window is behind',
                default_rows,
                default_oldest;
        END IF;
    END IF;

    -- Step 6: the runtime role's privileges. Every query names the partitioned
    -- parent, and PostgreSQL checks privileges on the table named in the
    -- statement, so a partition created by any role - including a manual
    -- `CREATE TABLE ... PARTITION OF` run as the bootstrap superuser - needs no
    -- grant of its own. What must never go missing is the parent grant and the
    -- sequence the click writer calls directly, so re-assert exactly what the
    -- deployment grants. This is the only repair available without a deployment,
    -- and it deliberately reuses the same object set as postgres-init.
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = runtime_role) THEN
            RAISE WARNING 'click partition maintenance cannot reconcile privileges because role % does not exist', runtime_role;
        ELSE
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON clicks_v2 TO %I', runtime_role);
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON clicks TO %I', runtime_role);
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON click_event_dedup TO %I', runtime_role);
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON click_acknowledgements TO %I', runtime_role);
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON links TO %I', runtime_role);
            EXECUTE format('GRANT USAGE, SELECT ON SEQUENCE clicks_click_id_seq TO %I', runtime_role);
        END IF;
    EXCEPTION WHEN OTHERS THEN
        RAISE WARNING 'click partition maintenance could not reconcile privileges for %: %', runtime_role, SQLERRM;
    END;
END
$maintenance$;
