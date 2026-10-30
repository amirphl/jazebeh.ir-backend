-- Database initialization for Yamata no Orochi
-- This script runs when the PostgreSQL container starts for the first time

-- Enable required extensions in the postgres database
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Create pg_stat_statements extension (requires shared_preload_libraries)
-- This will only work if the extension was loaded at server startup
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_available_extensions 
        WHERE name = 'pg_stat_statements' AND default_version IS NOT NULL
    ) THEN
        CREATE EXTENSION IF NOT EXISTS "pg_stat_statements";
    END IF;
END $$;

-- Grant necessary permissions to the main application user
-- (Database is created by POSTGRES_DB env var in docker-compose; never create it here)
GRANT CONNECT ON DATABASE "${DB_NAME:-yamata_no_orochi}" TO "${DB_USER:-yamata_user}";

-- Database settings for production
-- Note: pg_stat_statements extension is loaded via shared_preload_libraries in postgresql.conf
ALTER SYSTEM SET pg_stat_statements.track = 'all';
ALTER SYSTEM SET pg_stat_statements.max = 10000;

-- Configure connection pooling parameters
ALTER SYSTEM SET max_prepared_transactions = 0;
-- Keep this aligned with postgresql.conf. Migration 0149 makes execution
-- reservations use a single Bundle advisory lock, but retain headroom for
-- mixed-version deployments and other large transactions.
ALTER SYSTEM SET max_locks_per_transaction = 4096;

-- Set timezone
SET timezone = 'UTC';
