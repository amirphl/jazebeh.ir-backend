BEGIN;

ALTER TABLE line_numbers DROP CONSTRAINT IF EXISTS chk_line_numbers_provider;
ALTER TABLE line_numbers ADD CONSTRAINT chk_line_numbers_provider CHECK (provider IN ('payamsms', 'candoo', 'asiatech'));
ALTER TABLE sent_sms DROP CONSTRAINT IF EXISTS chk_sent_sms_provider;
ALTER TABLE sent_sms ADD CONSTRAINT chk_sent_sms_provider CHECK (provider IN ('payamsms', 'candoo', 'asiatech'));
ALTER TABLE campaign_status_jobs DROP CONSTRAINT IF EXISTS chk_campaign_status_jobs_sms_provider;
ALTER TABLE campaign_status_jobs ADD CONSTRAINT chk_campaign_status_jobs_sms_provider CHECK (provider IS NULL OR provider IN ('payamsms', 'candoo', 'asiatech'));
ALTER TABLE sms_status_results DROP CONSTRAINT IF EXISTS chk_sms_status_results_provider;
ALTER TABLE sms_status_results ADD CONSTRAINT chk_sms_status_results_provider CHECK (provider IN ('payamsms', 'candoo', 'asiatech'));
ALTER TABLE sms_provider_send_attempts DROP CONSTRAINT IF EXISTS chk_sms_provider_send_attempts_provider;
ALTER TABLE sms_provider_send_attempts ADD CONSTRAINT chk_sms_provider_send_attempts_provider CHECK (provider IN ('payamsms', 'candoo', 'asiatech'));

CREATE TABLE asiatech_sms_messages (
 id BIGSERIAL PRIMARY KEY, sent_sms_id BIGINT NOT NULL UNIQUE REFERENCES sent_sms(id) ON DELETE CASCADE,
 provider_message_id VARCHAR(128) NOT NULL UNIQUE, source_address VARCHAR(64) NOT NULL,
 destination_address VARCHAR(32) NOT NULL, udh VARCHAR(128) NOT NULL, api_version VARCHAR(16) NOT NULL,
 part_count INTEGER NOT NULL DEFAULT 0, upstream_gateway VARCHAR(64) NOT NULL DEFAULT '', operator_group VARCHAR(16) NOT NULL DEFAULT 'other',
 submitted_at TIMESTAMPTZ NOT NULL, next_poll_at TIMESTAMPTZ NOT NULL, poll_count INTEGER NOT NULL DEFAULT 0,
 last_polled_at TIMESTAMPTZ NULL, finalized_at TIMESTAMPTZ NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_asiatech_sms_messages_due ON asiatech_sms_messages(next_poll_at) WHERE finalized_at IS NULL;
CREATE TABLE asiatech_sms_dlr_polls (
 id BIGSERIAL PRIMARY KEY, asiatech_sms_message_id BIGINT NOT NULL REFERENCES asiatech_sms_messages(id) ON DELETE CASCADE,
 polled_at TIMESTAMPTZ NOT NULL, overall_status_code INTEGER NOT NULL, overall_status_text VARCHAR(64) NOT NULL, is_final BOOLEAN NOT NULL, raw_response TEXT NOT NULL
);
CREATE INDEX idx_asiatech_sms_dlr_polls_message ON asiatech_sms_dlr_polls(asiatech_sms_message_id, id);
CREATE TABLE asiatech_sms_dlr_parts (
 id BIGSERIAL PRIMARY KEY, asiatech_sms_dlr_poll_id BIGINT NOT NULL REFERENCES asiatech_sms_dlr_polls(id) ON DELETE CASCADE,
 part_number INTEGER NOT NULL, status_code INTEGER NOT NULL, status_text VARCHAR(64) NOT NULL, provider_status_at TIMESTAMPTZ NULL, chargeback_eligible BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX idx_asiatech_sms_dlr_parts_poll ON asiatech_sms_dlr_parts(asiatech_sms_dlr_poll_id, part_number);
COMMIT;
