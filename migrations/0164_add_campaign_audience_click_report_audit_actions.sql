-- Migration: 0164_add_campaign_audience_click_report_audit_actions.sql
-- Description: Add audit actions emitted by campaign audience click-report exports.
--
-- The application already emits these values.  Keep this corrective migration
-- idempotent so it is safe for databases where either value was added manually.

ALTER TYPE audit_action_enum ADD VALUE IF NOT EXISTS 'campaign_audience_click_report_exported';
ALTER TYPE audit_action_enum ADD VALUE IF NOT EXISTS 'campaign_audience_click_report_export_failed';
