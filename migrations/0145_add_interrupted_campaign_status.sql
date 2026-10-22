-- A stale run with persisted send intent cannot be replayed safely: the
-- provider may have accepted a request before the worker died. Keep it out of
-- the runnable queue while retaining its immutable processed/delivery records.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type AS type
        JOIN pg_enum AS value ON value.enumtypid = type.oid
        WHERE type.typname = 'sms_campaign_status'
          AND value.enumlabel = 'interrupted'
    ) THEN
        ALTER TYPE sms_campaign_status ADD VALUE 'interrupted';
    END IF;
END $$;
