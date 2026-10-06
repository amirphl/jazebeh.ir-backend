BEGIN;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM asiatech_sms_messages)
    OR EXISTS (SELECT 1 FROM line_numbers WHERE provider = 'asiatech')
    OR EXISTS (SELECT 1 FROM sent_sms WHERE provider = 'asiatech')
    OR EXISTS (SELECT 1 FROM campaign_status_jobs WHERE provider = 'asiatech')
    OR EXISTS (SELECT 1 FROM sms_status_results WHERE provider = 'asiatech')
    OR EXISTS (SELECT 1 FROM sms_provider_send_attempts WHERE provider = 'asiatech')
 THEN RAISE EXCEPTION 'cannot roll back 0169 while AsiaTech data exists'; END IF;
END $$;
DROP TABLE IF EXISTS asiatech_sms_dlr_parts;
DROP TABLE IF EXISTS asiatech_sms_dlr_polls;
DROP TABLE IF EXISTS asiatech_sms_messages;
ALTER TABLE line_numbers DROP CONSTRAINT IF EXISTS chk_line_numbers_provider;
ALTER TABLE line_numbers ADD CONSTRAINT chk_line_numbers_provider CHECK (provider IN ('payamsms', 'candoo'));
ALTER TABLE sent_sms DROP CONSTRAINT IF EXISTS chk_sent_sms_provider;
ALTER TABLE sent_sms ADD CONSTRAINT chk_sent_sms_provider CHECK (provider IN ('payamsms', 'candoo'));
ALTER TABLE campaign_status_jobs DROP CONSTRAINT IF EXISTS chk_campaign_status_jobs_sms_provider;
ALTER TABLE campaign_status_jobs ADD CONSTRAINT chk_campaign_status_jobs_sms_provider CHECK (provider IS NULL OR provider IN ('payamsms', 'candoo'));
ALTER TABLE sms_status_results DROP CONSTRAINT IF EXISTS chk_sms_status_results_provider;
ALTER TABLE sms_status_results ADD CONSTRAINT chk_sms_status_results_provider CHECK (provider IN ('payamsms', 'candoo'));
ALTER TABLE sms_provider_send_attempts DROP CONSTRAINT IF EXISTS chk_sms_provider_send_attempts_provider;
ALTER TABLE sms_provider_send_attempts ADD CONSTRAINT chk_sms_provider_send_attempts_provider CHECK (provider IN ('payamsms', 'candoo'));
COMMIT;
