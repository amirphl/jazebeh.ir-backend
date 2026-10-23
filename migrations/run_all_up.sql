-- run_all_up.sql
-- Convenience script to apply all migrations in correct order

\set ON_ERROR_STOP on

\echo 'Starting database migration...'

\echo 'Running 0001_create_account_types.sql...'
\i migrations/0001_create_account_types.sql

\echo 'Running 0002_create_customers.sql...'
\i migrations/0002_create_customers.sql

\echo 'Running 0003_create_otp_verifications.sql...'
\i migrations/0003_create_otp_verifications.sql

\echo 'Running 0004_create_customer_sessions.sql...'
\i migrations/0004_create_customer_sessions.sql

\echo 'Running 0005_create_audit_log.sql...'
\i migrations/0005_create_audit_log.sql

\echo 'Running 0006_update_customer_fields.sql...'
\i migrations/0006_update_customer_fields.sql

\echo 'Running 0007_add_missing_audit_actions.sql...'
\i migrations/0007_add_missing_audit_actions.sql

\echo 'Running 0008_update_audit_log_success_field.sql...'
\i migrations/0008_update_audit_log_success_field.sql

\echo 'Running 0009_add_correlation_ids.sql...'
\i migrations/0009_add_correlation_ids.sql

\echo 'Running 0010_add_customer_uuid_and_agency_id.sql...'
\i migrations/0010_add_customer_uuid_and_agency_id.sql

\echo 'Running 0011_add_new_audit_actions.sql...'
\i migrations/0011_add_new_audit_actions.sql

\echo 'Running 0012_update_timestamp_defaults_to_utc.sql...'
\i migrations/0012_update_timestamp_defaults_to_utc.sql

\echo 'Running 0013_relax_name_validation.sql...'
\i migrations/0013_relax_name_validation.sql

\echo 'Running 0014_create_sms_campaigns.sql...'
\i migrations/0014_create_sms_campaigns.sql

\echo 'Running 0015_add_sms_campaign_audit_actions.sql...'
\i migrations/0015_add_sms_campaign_audit_actions.sql

\echo 'Running 0016_add_comment_to_sms_campaigns.sql...'
\i migrations/0016_add_comment_to_sms_campaigns.sql

\echo 'Running 0017_create_wallet_models.sql...'
\i migrations/0017_create_wallet_models.sql

\echo 'Running 0018_create_agency_commission_models.sql...'
\i migrations/0018_create_agency_commission_models.sql

\echo 'Running 0019_add_payment_audit_actions.sql...'
\i migrations/0019_add_payment_audit_actions.sql

\echo 'Running 0020_create_tax_wallet.sql...'
\i migrations/0020_create_tax_wallet.sql

\echo 'Running 0021_change_agency_referer_code_to_varchar.sql...'
\i migrations/0021_change_agency_referer_code_to_varchar.sql

\echo 'Running 0022_create_agency_discounts.sql...'
\i migrations/0022_create_agency_discounts.sql

\echo 'Running 0023_add_credit_balance_to_balance_snapshots.sql...'
\i migrations/0023_add_credit_balance_to_balance_snapshots.sql

\echo 'Running 0024_create_sheba_number_on_customers.sql...'
\i migrations/0024_create_sheba_number_on_customers.sql

\echo 'Running 0024_create_system_company_and_wallet.sql...'
\i migrations/0024_create_system_company_and_wallet.sql

\echo 'Running 0025_add_indexes_on_transactions_metadata.sql...'
\i migrations/0025_add_indexes_on_transactions_metadata.sql

\echo 'Running 0026_add_index_on_transactions_metadata_agency_discount_id.sql...'
\i migrations/0026_add_index_on_transactions_metadata_agency_discount_id.sql

\echo 'Running 0027_add_discount_audit_actions.sql...'
\i migrations/0027_add_discount_audit_actions.sql

\echo 'Running 0028_add_launch_campaign_transaction_type.sql...'
\i migrations/0028_add_launch_campaign_transaction_type.sql

\echo 'Running 0029_create_admins.sql...'
\i migrations/0029_create_admins.sql

\echo 'Running 0030_create_line_numbers.sql...'
\i migrations/0030_create_line_numbers.sql

\echo 'Running 0031_remove_hardcoded_system_tax_data.sql...'
\i migrations/0031_remove_hardcoded_system_tax_data.sql

\echo 'Running 0032_add_updated_at_to_customer_sessions.sql...'
\i migrations/0032_add_updated_at_to_customer_sessions.sql

\echo 'Running 0033_create_bots.sql...'
\i migrations/0033_create_bots.sql

\echo 'Running 0034_create_audience_profiles.sql...'
\i migrations/0034_create_audience_profiles.sql

\echo 'Running 0035_add_running_executed_to_sms_campaign_status.sql...'
\i migrations/0035_add_running_executed_to_sms_campaign_status.sql

\echo 'Running 0036_create_processed_campaigns.sql...'
\i migrations/0036_create_processed_campaigns.sql

\echo 'Running 0037_create_sent_sms.sql...'
\i migrations/0037_create_sent_sms.sql

\echo 'Running 0038_create_tags.sql...'
\i migrations/0038_create_tags.sql

\echo 'Running 0039_add_num_target_after_finalize_to_sms_campaigns.sql...'
\i migrations/0039_add_num_target_after_finalize_to_sms_campaigns.sql

\echo 'Running 0040_create_tickets.sql...'
\i migrations/0040_create_tickets.sql

\echo 'Running 0041_add_replied_by_admin_to_tickets.sql...'
\i migrations/0041_add_replied_by_admin_to_tickets.sql

\echo 'Running 0042_add_provider_fields_to_sent_sms.sql...'
\i migrations/0042_add_provider_fields_to_sent_sms.sql

\echo 'Running 0043_create_short_links.sql...'
\i migrations/0043_create_short_links.sql

\echo 'Running 0044_update_short_links_and_create_clicks.sql...'
\i migrations/0044_update_short_links_and_create_clicks.sql

\echo 'Running 0045_add_scenario_id_to_short_links.sql...'
\i migrations/0045_add_scenario_id_to_short_links.sql

\echo 'Running 0046_remove_user_agent_ip_from_short_links.sql...'
\i migrations/0046_remove_user_agent_ip_from_short_links.sql

\echo 'Running 0047_add_scenario_id_to_short_link_clicks.sql...'
\i migrations/0047_add_scenario_id_to_short_link_clicks.sql

\echo 'Running 0048_add_scenario_name_to_short_links.sql...'
\i migrations/0048_add_scenario_name_to_short_links.sql

\echo 'Running 0049_create_crypto_payments.sql...'
\i migrations/0049_create_crypto_payments.sql

\echo 'Running 0050_remove_short_links_indexes.sql...'
\i migrations/0050_remove_short_links_indexes.sql

\echo 'Running 0051_add_job_category_to_customers.sql...'
\i migrations/0051_add_job_category_to_customers.sql

\echo 'Running 0052_rename_segment_to_level1_and_add_level3.sql...'
\i migrations/0052_rename_segment_to_level1_and_add_level3.sql

\echo 'Running 0053_denormalize_short_link_clicks.sql...'
\i migrations/0053_denormalize_short_link_clicks.sql

\echo 'Running 0054_backfill_short_link_clicks_from_short_links.sql...'
\i migrations/0054_backfill_short_link_clicks_from_short_links.sql

\echo 'Running 0055_create_sms_status_jobs.sql...'
\i migrations/0055_create_sms_status_jobs.sql

\echo 'Running 0056_create_sms_status_results.sql...'
\i migrations/0056_create_sms_status_results.sql

\echo 'Running 0057_add_statistics_to_processed_campaigns.sql...'
\i migrations/0057_add_statistics_to_processed_campaigns.sql

\echo 'Running 0058_add_statistics_to_sms_campaigns.sql...'
\i migrations/0058_add_statistics_to_sms_campaigns.sql

\echo 'Running 0059_add_campaign_and_agency_balances_to_balance_snapshots.sql...'
\i migrations/0059_add_campaign_and_agency_balances_to_balance_snapshots.sql

\echo 'Running 0060_add_new_transaction_types.sql...'
\i migrations/0060_add_new_transaction_types.sql

\echo 'Running 0061_create_segment_price_factors.sql...'
\i migrations/0061_create_segment_price_factors.sql

\echo 'Running 0062_add_cancelled_status_to_sms_campaigns.sql...'
\i migrations/0062_add_cancelled_status_to_sms_campaigns.sql

\echo 'Running 0063_create_audience_selection_cache.sql...'
\i migrations/0063_create_audience_selection_cache.sql

\echo 'Running 0064_create_sequence_counters.sql...'
\i migrations/0064_create_sequence_counters.sql

\echo 'Running 0065_change_sent_sms_tracking_id_type.sql...'
\i migrations/0065_change_sent_sms_tracking_id_type.sql

\echo 'Running 0066_allow_empty_audit_log_ip_address.sql...'
\i migrations/0066_allow_empty_audit_log_ip_address.sql

\echo 'Running 0067_create_multimedia_assets.sql...'
\i migrations/0067_create_multimedia_assets.sql

\echo 'Running 0068_create_platform_settings.sql...'
\i migrations/0068_create_platform_settings.sql

\echo 'Running 0069_add_platform_to_platform_settings.sql...'
\i migrations/0069_add_platform_to_platform_settings.sql

\echo 'Running 0070_add_platform_to_segment_price_factors.sql...'
\i migrations/0070_add_platform_to_segment_price_factors.sql

\echo 'Running 0071_add_cancelled_by_admin_status_to_sms_campaigns.sql...'
\i migrations/0071_add_cancelled_by_admin_status_to_sms_campaigns.sql

\echo 'Running 0072_add_metadata_to_platform_settings.sql...'
\i migrations/0072_add_metadata_to_platform_settings.sql

\echo 'Running 0073_create_sent_bale_messages.sql...'
\i migrations/0073_create_sent_bale_messages.sql

\echo 'Running 0074_replicate_sms_segment_price_factors_to_other_platforms.sql...'
\i migrations/0074_replicate_sms_segment_price_factors_to_other_platforms.sql

\echo 'Running 0075_create_sent_splus_messages.sql...'
\i migrations/0075_create_sent_splus_messages.sql

\echo 'Running 0076_rename_sms_campaigns_to_campaigns.sql...'
\i migrations/0076_rename_sms_campaigns_to_campaigns.sql

\echo 'Running 0077_drop_audit_log_customer_fk.sql...'
\i migrations/0077_drop_audit_log_customer_fk.sql

\echo 'Running 0078_drop_agency_commissions.sql...'
\i migrations/0078_drop_agency_commissions.sql

\echo 'Running 0079_drop_otp_verifications.sql...'
\i migrations/0079_drop_otp_verifications.sql

\echo 'Running 0080_add_lang_to_payment_requests.sql...'
\i migrations/0080_add_lang_to_payment_requests.sql

\echo 'Running 0081_add_admin_audit_actions.sql...'
\i migrations/0081_add_admin_audit_actions.sql

\echo 'Running 0082_create_deposit_receipts.sql...'
\i migrations/0082_create_deposit_receipts.sql

\echo 'Running 0083_add_deposit_receipt_audit_action.sql...'
\i migrations/0083_add_deposit_receipt_audit_action.sql

\echo 'Running 0084_add_invoice_number_to_deposit_receipts.sql...'
\i migrations/0084_add_invoice_number_to_deposit_receipts.sql

\echo 'Running 0085_add_admin_deposit_receipt_review_action.sql...'
\i migrations/0085_add_admin_deposit_receipt_review_action.sql

\echo 'Running 0086_create_platform_base_prices.sql...'
\i migrations/0086_create_platform_base_prices.sql

\echo 'Running 0087_create_acl_change_requests.sql...'
\i migrations/0087_create_acl_change_requests.sql

\echo 'Running 0088_add_admin_permissions.sql...'
\i migrations/0088_add_admin_permissions.sql

\echo 'Running 0089_add_unique_customer_name_to_platform_settings.sql...'
\i migrations/0089_add_unique_customer_name_to_platform_settings.sql

\echo 'Running 0090_add_expired_status_to_sms_campaigns.sql...'
\i migrations/0090_add_expired_status_to_sms_campaigns.sql

\echo 'Running 0091_add_admin_platform_base_price_audit_actions.sql...'
\i migrations/0091_add_admin_platform_base_price_audit_actions.sql

\echo 'Running 0092_create_page_prices.sql...'
\i migrations/0092_create_page_prices.sql

\echo 'Running 0093_add_admin_payment_audit_actions.sql...'
\i migrations/0093_add_admin_payment_audit_actions.sql

\echo 'Running 0094_add_campaign_report_export_audit_actions.sql...'
\i migrations/0094_add_campaign_report_export_audit_actions.sql

\echo 'Running 0095_add_admin_page_price_audit_actions.sql...'
\i migrations/0095_add_admin_page_price_audit_actions.sql

\echo 'Running 0096_add_campaign_refund_reconcile_failed_audit_action.sql...'
\i migrations/0096_add_campaign_refund_reconcile_failed_audit_action.sql

\echo 'Running 0097_add_invoice_issue_requested_audit_action.sql...'
\i migrations/0097_add_invoice_issue_requested_audit_action.sql

\echo 'Running 0098_rename_sms_status_jobs_to_campaign_status_jobs.sql...'
\i migrations/0098_rename_sms_status_jobs_to_campaign_status_jobs.sql

\echo 'Running 0099_create_bale_status_results.sql...'
\i migrations/0099_create_bale_status_results.sql

\echo 'Running 0100_rename_sms_status_results_customer_id_to_tracking_id.sql...'
\i migrations/0100_rename_sms_status_results_customer_id_to_tracking_id.sql

\echo 'Running 0101_rename_campaign_status_jobs_customer_ids_to_tracking_ids.sql...'
\i migrations/0101_rename_campaign_status_jobs_customer_ids_to_tracking_ids.sql

\echo 'Running 0102_add_campaign_test_send_audit_action.sql...'
\i migrations/0102_add_campaign_test_send_audit_action.sql

\echo 'Running 0103_add_business_license_and_website_to_platform_settings.sql...'
\i migrations/0103_add_business_license_and_website_to_platform_settings.sql

\echo 'Running 0104_create_sent_rubika_messages.sql...'
\i migrations/0104_create_sent_rubika_messages.sql

\echo 'Running 0104_create_splus_status_results.sql...'
\i migrations/0104_create_splus_status_results.sql

\echo 'Running 0105_create_rubika_status_results.sql...'
\i migrations/0105_create_rubika_status_results.sql

\echo 'Running 0106_add_admin_preview_wallet_charge_impact_audit_actions.sql...'
\i migrations/0106_add_admin_preview_wallet_charge_impact_audit_actions.sql

\echo 'Running 0107_create_bundles.sql...'
\i migrations/0107_create_bundles.sql

\echo 'Running 0108_add_bundle_audit_actions.sql...'
\i migrations/0108_add_bundle_audit_actions.sql

\echo 'Running 0109_backfill_bundles_from_campaigns.sql...'
\i migrations/0109_backfill_bundles_from_campaigns.sql

\echo 'Running 0110_add_bundle_id_and_phase_to_campaigns.sql...'
\i migrations/0110_add_bundle_id_and_phase_to_campaigns.sql

\echo 'Running 0111_create_bundle_audience_selections.sql...'
\i migrations/0111_create_bundle_audience_selections.sql

\echo 'Running 0112_create_audience_scores.sql...'
\i migrations/0112_create_audience_scores.sql

\echo 'Running 0113_create_src_layer_all_stats.sql...'
\i migrations/0113_create_src_layer_all_stats.sql

\echo 'Running 0114_add_normalized_score_to_audience_profiles.sql...'
\i migrations/0114_add_normalized_score_to_audience_profiles.sql

\echo 'Running 0115_add_hidden_to_campaigns.sql...'
\i migrations/0115_add_hidden_to_campaigns.sql

\echo 'Running 0116_add_bundle_update_audit_actions.sql...'
\i migrations/0116_add_bundle_update_audit_actions.sql

\echo 'Running 0117_create_smart_tag_evaluation.sql...'
\i migrations/0117_create_smart_tag_evaluation.sql

\echo 'Running 0118_scope_campaign_status_jobs_by_platform.sql...'
\i migrations/0118_scope_campaign_status_jobs_by_platform.sql

\echo 'Running 0119_convert_bundle_tag_evaluation_ids_to_bigserial.sql...'
\i migrations/0119_convert_bundle_tag_evaluation_ids_to_bigserial.sql

\echo 'Running 0120_create_campaign_selected_tags.sql...'
\i migrations/0120_create_campaign_selected_tags.sql

\echo 'Running 0121_backfill_campaign_audience_targeting_method.sql...'
\i migrations/0121_backfill_campaign_audience_targeting_method.sql

\echo 'Running 0122_create_campaign_targeting_capacity_calculations.sql...'
\i migrations/0122_create_campaign_targeting_capacity_calculations.sql

\echo 'Running 0123_add_bundle_audience_selection_to_processed_campaigns.sql...'
\i migrations/0123_add_bundle_audience_selection_to_processed_campaigns.sql

\echo 'Running 0124_index_smart_targeting_capacity_reservations.sql...'
\i migrations/0124_index_smart_targeting_capacity_reservations.sql

\echo 'Running 0125_create_src_reference.sql...'
\i migrations/0125_create_src_reference.sql

\echo 'Running 0126_optimize_campaign_audience_selection.sql...'
\i migrations/0126_optimize_campaign_audience_selection.sql

\echo 'Running 0127_normalize_bundle_audience_allocations.sql...'
\i migrations/0127_normalize_bundle_audience_allocations.sql

\echo 'Running 0128_smart_targeting_phase_preparation.sql...'
\i migrations/0128_smart_targeting_phase_preparation.sql

\echo 'Running 0129_create_payam_sms_send_responses.sql...'
\i migrations/0129_create_payam_sms_send_responses.sql

\echo 'Running 0130_create_campaign_targeting_test_sampling_calculations.sql...'
\i migrations/0130_create_campaign_targeting_test_sampling_calculations.sql

\echo 'Running 0131_optimize_postgres_audience_maintenance.sql...'
\i migrations/0131_optimize_postgres_audience_maintenance.sql

\echo 'Running 0132_create_tag_test_performance_reports.sql...'
\i migrations/0132_create_tag_test_performance_reports.sql

\echo 'Running 0133_decouple_smart_targeting_test_sampling.sql...'
\i migrations/0133_decouple_smart_targeting_test_sampling.sql

\echo 'Running 0134_create_bundle_audience_exclusions.sql...'
\i migrations/0134_create_bundle_audience_exclusions.sql

\echo 'Running 0135_external_short_link_sync.sql...'
\i migrations/0135_external_short_link_sync.sql

\echo 'Running 0136_version_smart_targeting_capacity_eligibility.sql...'
\i migrations/0136_version_smart_targeting_capacity_eligibility.sql

\echo 'Running 0137_add_sms_provider_selection.sql...'
\i migrations/0137_add_sms_provider_selection.sql

\echo 'Running 0138_extend_tag_performance_to_execution.sql...'
\i migrations/0138_extend_tag_performance_to_execution.sql

\echo 'Running 0139_normalize_short_links_and_exclude_test_clicks.sql...'
\i migrations/0139_normalize_short_links_and_exclude_test_clicks.sql

\echo 'Running 0140_persist_smart_targeting_test_sample_selections.sql...'
\i migrations/0140_persist_smart_targeting_test_sample_selections.sql

\echo 'Running 0141_snapshot_smart_targeting_allowed_colors.sql...'
\i migrations/0141_snapshot_smart_targeting_allowed_colors.sql

\echo 'Running 0142_create_smart_targeting_execution_reservations.sql...'
\i migrations/0142_create_smart_targeting_execution_reservations.sql

\echo 'Running 0143_version_execution_reservations_and_capacity_phase.sql...'
\i migrations/0143_version_execution_reservations_and_capacity_phase.sql

\echo 'Running 0144_guard_cross_phase_bundle_audience_claims.sql...'
\i migrations/0144_guard_cross_phase_bundle_audience_claims.sql

\echo 'Running 0145_add_interrupted_campaign_status.sql...'
\i migrations/0145_add_interrupted_campaign_status.sql

\echo 'Running 0146_harden_execution_reservation_headers.sql...'
\i migrations/0146_harden_execution_reservation_headers.sql

\echo 'Running 0147_include_interrupted_campaigns_in_capacity_index.sql...'
\i migrations/0147_include_interrupted_campaigns_in_capacity_index.sql

\echo 'Running 0148_add_short_link_allocation_idempotency.sql...'
\i migrations/0148_add_short_link_allocation_idempotency.sql

\echo 'All migrations completed successfully!'
\echo 'Database schema is now ready for the Yamata no Orochi wallet, and payment system with comprehensive audit logging and tax collection.' 
