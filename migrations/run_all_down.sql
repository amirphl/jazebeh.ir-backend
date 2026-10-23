-- run_all_down.sql
-- Convenience script to rollback all migrations in reverse order

\set ON_ERROR_STOP on

\echo 'Starting database rollback...'

\echo 'Running 0148_add_short_link_allocation_idempotency_down.sql...'
\i migrations/0148_add_short_link_allocation_idempotency_down.sql

\echo 'Running 0147_include_interrupted_campaigns_in_capacity_index_down.sql...'
\i migrations/0147_include_interrupted_campaigns_in_capacity_index_down.sql

\echo 'Running 0146_harden_execution_reservation_headers_down.sql...'
\i migrations/0146_harden_execution_reservation_headers_down.sql

\echo 'Running 0145_add_interrupted_campaign_status_down.sql...'
\i migrations/0145_add_interrupted_campaign_status_down.sql

\echo 'Running 0144_guard_cross_phase_bundle_audience_claims_down.sql...'
\i migrations/0144_guard_cross_phase_bundle_audience_claims_down.sql

\echo 'Running 0143_version_execution_reservations_and_capacity_phase_down.sql...'
\i migrations/0143_version_execution_reservations_and_capacity_phase_down.sql

\echo 'Running 0142_create_smart_targeting_execution_reservations_down.sql...'
\i migrations/0142_create_smart_targeting_execution_reservations_down.sql

\echo 'Running 0141_snapshot_smart_targeting_allowed_colors_down.sql...'
\i migrations/0141_snapshot_smart_targeting_allowed_colors_down.sql

\echo 'Running 0140_persist_smart_targeting_test_sample_selections_down.sql...'
\i migrations/0140_persist_smart_targeting_test_sample_selections_down.sql

\echo 'Running 0139_normalize_short_links_and_exclude_test_clicks_down.sql...'
\i migrations/0139_normalize_short_links_and_exclude_test_clicks_down.sql

\echo 'Running 0138_extend_tag_performance_to_execution_down.sql...'
\i migrations/0138_extend_tag_performance_to_execution_down.sql

\echo 'Running 0137_add_sms_provider_selection_down.sql...'
\i migrations/0137_add_sms_provider_selection_down.sql

\echo 'Running 0136_version_smart_targeting_capacity_eligibility_down.sql...'
\i migrations/0136_version_smart_targeting_capacity_eligibility_down.sql

\echo 'Running 0135_external_short_link_sync_down.sql...'
\i migrations/0135_external_short_link_sync_down.sql

\echo 'Running 0134_create_bundle_audience_exclusions_down.sql...'
\i migrations/0134_create_bundle_audience_exclusions_down.sql

\echo 'Running 0133_decouple_smart_targeting_test_sampling_down.sql...'
\i migrations/0133_decouple_smart_targeting_test_sampling_down.sql

\echo 'Running 0132_create_tag_test_performance_reports_down.sql...'
\i migrations/0132_create_tag_test_performance_reports_down.sql

\echo 'Running 0131_optimize_postgres_audience_maintenance_down.sql...'
\i migrations/0131_optimize_postgres_audience_maintenance_down.sql

\echo 'Running 0130_create_campaign_targeting_test_sampling_calculations_down.sql...'
\i migrations/0130_create_campaign_targeting_test_sampling_calculations_down.sql

\echo 'Running 0129_create_payam_sms_send_responses_down.sql...'
\i migrations/0129_create_payam_sms_send_responses_down.sql

\echo 'Running 0128_smart_targeting_phase_preparation_down.sql...'
\i migrations/0128_smart_targeting_phase_preparation_down.sql

\echo 'Running 0127_normalize_bundle_audience_allocations_down.sql...'
\i migrations/0127_normalize_bundle_audience_allocations_down.sql

\echo 'Running 0126_optimize_campaign_audience_selection_down.sql...'
\i migrations/0126_optimize_campaign_audience_selection_down.sql

\echo 'Running 0125_create_src_reference_down.sql...'
\i migrations/0125_create_src_reference_down.sql

\echo 'Running 0124_index_smart_targeting_capacity_reservations_down.sql...'
\i migrations/0124_index_smart_targeting_capacity_reservations_down.sql

\echo 'Running 0123_add_bundle_audience_selection_to_processed_campaigns_down.sql...'
\i migrations/0123_add_bundle_audience_selection_to_processed_campaigns_down.sql

\echo 'Running 0122_create_campaign_targeting_capacity_calculations_down.sql...'
\i migrations/0122_create_campaign_targeting_capacity_calculations_down.sql

\echo 'Running 0121_backfill_campaign_audience_targeting_method_down.sql...'
\i migrations/0121_backfill_campaign_audience_targeting_method_down.sql

\echo 'Running 0120_create_campaign_selected_tags_down.sql...'
\i migrations/0120_create_campaign_selected_tags_down.sql

\echo 'Running 0119_convert_bundle_tag_evaluation_ids_to_bigserial_down.sql...'
\i migrations/0119_convert_bundle_tag_evaluation_ids_to_bigserial_down.sql

\echo 'Running 0118_scope_campaign_status_jobs_by_platform_down.sql...'
\i migrations/0118_scope_campaign_status_jobs_by_platform_down.sql

\echo 'Running 0117_create_smart_tag_evaluation_down.sql...'
\i migrations/0117_create_smart_tag_evaluation_down.sql

\echo 'Running 0116_add_bundle_update_audit_actions_down.sql...'
\i migrations/0116_add_bundle_update_audit_actions_down.sql

\echo 'Running 0115_add_hidden_to_campaigns_down.sql...'
\i migrations/0115_add_hidden_to_campaigns_down.sql

\echo 'Running 0114_add_normalized_score_to_audience_profiles_down.sql...'
\i migrations/0114_add_normalized_score_to_audience_profiles_down.sql

\echo 'Running 0113_create_src_layer_all_stats_down.sql...'
\i migrations/0113_create_src_layer_all_stats_down.sql

\echo 'Running 0112_create_audience_scores_down.sql...'
\i migrations/0112_create_audience_scores_down.sql

\echo 'Running 0111_create_bundle_audience_selections_down.sql...'
\i migrations/0111_create_bundle_audience_selections_down.sql

\echo 'Running 0110_add_bundle_id_and_phase_to_campaigns_down.sql...'
\i migrations/0110_add_bundle_id_and_phase_to_campaigns_down.sql

\echo 'Running 0109_backfill_bundles_from_campaigns_down.sql...'
\i migrations/0109_backfill_bundles_from_campaigns_down.sql

\echo 'Running 0108_add_bundle_audit_actions_down.sql...'
\i migrations/0108_add_bundle_audit_actions_down.sql

\echo 'Running 0107_create_bundles_down.sql...'
\i migrations/0107_create_bundles_down.sql

\echo 'Running 0106_add_admin_preview_wallet_charge_impact_audit_actions_down.sql...'
\i migrations/0106_add_admin_preview_wallet_charge_impact_audit_actions_down.sql

\echo 'Running 0105_create_rubika_status_results_down.sql...'
\i migrations/0105_create_rubika_status_results_down.sql

\echo 'Running 0104_create_splus_status_results_down.sql...'
\i migrations/0104_create_splus_status_results_down.sql

\echo 'Running 0104_create_sent_rubika_messages_down.sql...'
\i migrations/0104_create_sent_rubika_messages_down.sql

\echo 'Running 0103_add_business_license_and_website_to_platform_settings_down.sql...'
\i migrations/0103_add_business_license_and_website_to_platform_settings_down.sql

\echo 'Running 0102_add_campaign_test_send_audit_action_down.sql...'
\i migrations/0102_add_campaign_test_send_audit_action_down.sql

\echo 'Running 0101_rename_campaign_status_jobs_customer_ids_to_tracking_ids_down.sql...'
\i migrations/0101_rename_campaign_status_jobs_customer_ids_to_tracking_ids_down.sql

\echo 'Running 0100_rename_sms_status_results_customer_id_to_tracking_id_down.sql...'
\i migrations/0100_rename_sms_status_results_customer_id_to_tracking_id_down.sql

\echo 'Running 0099_create_bale_status_results_down.sql...'
\i migrations/0099_create_bale_status_results_down.sql

\echo 'Running 0098_rename_sms_status_jobs_to_campaign_status_jobs_down.sql...'
\i migrations/0098_rename_sms_status_jobs_to_campaign_status_jobs_down.sql

\echo 'Running 0097_add_invoice_issue_requested_audit_action_down.sql...'
\i migrations/0097_add_invoice_issue_requested_audit_action_down.sql

\echo 'Running 0096_add_campaign_refund_reconcile_failed_audit_action_down.sql...'
\i migrations/0096_add_campaign_refund_reconcile_failed_audit_action_down.sql

\echo 'Running 0095_add_admin_page_price_audit_actions_down.sql...'
\i migrations/0095_add_admin_page_price_audit_actions_down.sql

\echo 'Running 0094_add_campaign_report_export_audit_actions_down.sql...'
\i migrations/0094_add_campaign_report_export_audit_actions_down.sql

\echo 'Running 0093_add_admin_payment_audit_actions_down.sql...'
\i migrations/0093_add_admin_payment_audit_actions_down.sql

\echo 'Running 0092_create_page_prices_down.sql...'
\i migrations/0092_create_page_prices_down.sql

\echo 'Running 0091_add_admin_platform_base_price_audit_actions_down.sql...'
\i migrations/0091_add_admin_platform_base_price_audit_actions_down.sql

\echo 'Running 0090_add_expired_status_to_sms_campaigns_down.sql...'
\i migrations/0090_add_expired_status_to_sms_campaigns_down.sql

\echo 'Running 0089_add_unique_customer_name_to_platform_settings_down.sql...'
\i migrations/0089_add_unique_customer_name_to_platform_settings_down.sql

\echo 'Running 0088_add_admin_permissions_down.sql...'
\i migrations/0088_add_admin_permissions_down.sql

\echo 'Running 0087_create_acl_change_requests_down.sql...'
\i migrations/0087_create_acl_change_requests_down.sql

\echo 'Running 0086_create_platform_base_prices_down.sql...'
\i migrations/0086_create_platform_base_prices_down.sql

\echo 'Running 0085_add_admin_deposit_receipt_review_action_down.sql...'
\i migrations/0085_add_admin_deposit_receipt_review_action_down.sql

\echo 'Running 0084_add_invoice_number_to_deposit_receipts_down.sql...'
\i migrations/0084_add_invoice_number_to_deposit_receipts_down.sql

\echo 'Running 0083_add_deposit_receipt_audit_action_down.sql...'
\i migrations/0083_add_deposit_receipt_audit_action_down.sql

\echo 'Running 0082_create_deposit_receipts_down.sql...'
\i migrations/0082_create_deposit_receipts_down.sql

\echo 'Running 0081_add_admin_audit_actions_down.sql...'
\i migrations/0081_add_admin_audit_actions_down.sql

\echo 'Running 0080_add_lang_to_payment_requests_down.sql...'
\i migrations/0080_add_lang_to_payment_requests_down.sql

\echo 'Running 0079_drop_otp_verifications_down.sql...'
\i migrations/0079_drop_otp_verifications_down.sql

\echo 'Running 0078_drop_agency_commissions_down.sql...'
\i migrations/0078_drop_agency_commissions_down.sql

\echo 'Running 0077_drop_audit_log_customer_fk_down.sql...'
\i migrations/0077_drop_audit_log_customer_fk_down.sql

\echo 'Running 0076_rename_sms_campaigns_to_campaigns_down.sql...'
\i migrations/0076_rename_sms_campaigns_to_campaigns_down.sql

\echo 'Running 0075_create_sent_splus_messages_down.sql...'
\i migrations/0075_create_sent_splus_messages_down.sql

\echo 'Running 0074_replicate_sms_segment_price_factors_to_other_platforms_down.sql...'
\i migrations/0074_replicate_sms_segment_price_factors_to_other_platforms_down.sql

\echo 'Running 0073_create_sent_bale_messages_down.sql...'
\i migrations/0073_create_sent_bale_messages_down.sql

\echo 'Running 0072_add_metadata_to_platform_settings_down.sql...'
\i migrations/0072_add_metadata_to_platform_settings_down.sql

\echo 'Running 0071_add_cancelled_by_admin_status_to_sms_campaigns_down.sql...'
\i migrations/0071_add_cancelled_by_admin_status_to_sms_campaigns_down.sql

\echo 'Running 0070_add_platform_to_segment_price_factors_down.sql...'
\i migrations/0070_add_platform_to_segment_price_factors_down.sql

\echo 'Running 0069_add_platform_to_platform_settings_down.sql...'
\i migrations/0069_add_platform_to_platform_settings_down.sql

\echo 'Running 0068_create_platform_settings_down.sql...'
\i migrations/0068_create_platform_settings_down.sql

\echo 'Running 0067_create_multimedia_assets_down.sql...'
\i migrations/0067_create_multimedia_assets_down.sql

\echo 'Running 0066_allow_empty_audit_log_ip_address_down.sql...'
\i migrations/0066_allow_empty_audit_log_ip_address_down.sql

\echo 'Running 0065_change_sent_sms_tracking_id_type_down.sql...'
\i migrations/0065_change_sent_sms_tracking_id_type_down.sql

\echo 'Running 0064_create_sequence_counters_down.sql...'
\i migrations/0064_create_sequence_counters_down.sql

\echo 'Running 0063_create_audience_selection_cache_down.sql...'
\i migrations/0063_create_audience_selection_cache_down.sql

\echo 'Running 0062_add_cancelled_status_to_sms_campaigns_down.sql...'
\i migrations/0062_add_cancelled_status_to_sms_campaigns_down.sql

\echo 'Running 0061_create_segment_price_factors_down.sql...'
\i migrations/0061_create_segment_price_factors_down.sql

\echo 'Running 0060_add_new_transaction_types_down.sql...'
\i migrations/0060_add_new_transaction_types_down.sql

\echo 'Running 0059_add_campaign_and_agency_balances_to_balance_snapshots_down.sql...'
\i migrations/0059_add_campaign_and_agency_balances_to_balance_snapshots_down.sql

\echo 'Running 0058_add_statistics_to_sms_campaigns_down.sql...'
\i migrations/0058_add_statistics_to_sms_campaigns_down.sql

\echo 'Running 0057_add_statistics_to_processed_campaigns_down.sql...'
\i migrations/0057_add_statistics_to_processed_campaigns_down.sql

\echo 'Running 0056_create_sms_status_results_down.sql...'
\i migrations/0056_create_sms_status_results_down.sql

\echo 'Running 0055_create_sms_status_jobs_down.sql...'
\i migrations/0055_create_sms_status_jobs_down.sql

\echo 'Running 0054_backfill_short_link_clicks_from_short_links_down.sql...'
\i migrations/0054_backfill_short_link_clicks_from_short_links_down.sql

\echo 'Running 0053_denormalize_short_link_clicks_down.sql...'
\i migrations/0053_denormalize_short_link_clicks_down.sql

\echo 'Running 0052_rename_segment_to_level1_and_add_level3_down.sql...'
\i migrations/0052_rename_segment_to_level1_and_add_level3_down.sql

\echo 'Running 0051_add_job_category_to_customers_down.sql...'
\i migrations/0051_add_job_category_to_customers_down.sql

\echo 'Running 0049_create_crypto_payments_down.sql...'
\i migrations/0049_create_crypto_payments_down.sql

\echo 'Running 0048_add_scenario_name_to_short_links_down.sql...'
\i migrations/0048_add_scenario_name_to_short_links_down.sql

\echo 'Running 0047_add_scenario_id_to_short_link_clicks_down.sql...'
\i migrations/0047_add_scenario_id_to_short_link_clicks_down.sql

\echo 'Running 0046_remove_user_agent_ip_from_short_links_down.sql...'
\i migrations/0046_remove_user_agent_ip_from_short_links_down.sql

\echo 'Running 0045_add_scenario_id_to_short_links_down.sql...'
\i migrations/0045_add_scenario_id_to_short_links_down.sql

\echo 'Running 0044_update_short_links_and_create_clicks_down.sql...'
\i migrations/0044_update_short_links_and_create_clicks_down.sql

\echo 'Running 0043_create_short_links_down.sql...'
\i migrations/0043_create_short_links_down.sql

\echo 'Running 0042_add_provider_fields_to_sent_sms_down.sql...'
\i migrations/0042_add_provider_fields_to_sent_sms_down.sql

\echo 'Running 0041_add_replied_by_admin_to_tickets_down.sql...'
\i migrations/0041_add_replied_by_admin_to_tickets_down.sql

\echo 'Running 0040_create_tickets_down.sql...'
\i migrations/0040_create_tickets_down.sql

\echo 'Running 0039_add_num_target_after_finalize_to_sms_campaigns_down.sql...'
\i migrations/0039_add_num_target_after_finalize_to_sms_campaigns_down.sql

\echo 'Running 0038_create_tags_down.sql...'
\i migrations/0038_create_tags_down.sql

\echo 'Running 0037_create_sent_sms_down.sql...'
\i migrations/0037_create_sent_sms_down.sql

\echo 'Running 0036_create_processed_campaigns_down.sql...'
\i migrations/0036_create_processed_campaigns_down.sql

\echo 'Running 0034_create_audience_profiles_down.sql...'
\i migrations/0034_create_audience_profiles_down.sql

\echo 'Running 0035_add_running_executed_to_sms_campaign_status_down.sql...'
\i migrations/0035_add_running_executed_to_sms_campaign_status_down.sql

\echo 'Running 0033_create_bots_down.sql...'
\i migrations/0033_create_bots_down.sql

\echo 'Running 0032_add_updated_at_to_customer_sessions_down.sql...'
\i migrations/0032_add_updated_at_to_customer_sessions_down.sql

\echo 'Running 0031_remove_hardcoded_system_tax_data_down.sql...'
\i migrations/0031_remove_hardcoded_system_tax_data_down.sql

\echo 'Running 0030_create_line_numbers_down.sql...'
\i migrations/0030_create_line_numbers_down.sql

\echo 'Running 0029_create_admins_down.sql...'
\i migrations/0029_create_admins_down.sql

\echo 'Running 0028_add_launch_campaign_transaction_type_down.sql...'
\i migrations/0028_add_launch_campaign_transaction_type_down.sql

\echo 'Running 0027_add_discount_audit_actions_down.sql...'
\i migrations/0027_add_discount_audit_actions_down.sql

\echo 'Running 0026_add_index_on_transactions_metadata_agency_discount_id_down.sql...'
\i migrations/0026_add_index_on_transactions_metadata_agency_discount_id_down.sql

\echo 'Running 0025_add_indexes_on_transactions_metadata_down.sql...'
\i migrations/0025_add_indexes_on_transactions_metadata_down.sql

\echo 'Running 0024_create_system_company_and_wallet_down.sql...'
\i migrations/0024_create_system_company_and_wallet_down.sql

\echo 'Running 0024_create_sheba_number_on_customers_down.sql...'
\i migrations/0024_create_sheba_number_on_customers_down.sql

\echo 'Running 0023_add_credit_balance_to_balance_snapshots_down.sql...'
\i migrations/0023_add_credit_balance_to_balance_snapshots_down.sql

\echo 'Running 0022_create_agency_discounts_down.sql...'
\i migrations/0022_create_agency_discounts_down.sql

\echo 'Running 0021_change_agency_referer_code_to_varchar_down.sql...'
\i migrations/0021_change_agency_referer_code_to_varchar_down.sql

\echo 'Running 0020_create_tax_wallet_down.sql...'
\i migrations/0020_create_tax_wallet_down.sql

\echo 'Running 0019_add_payment_audit_actions_down.sql...'
\i migrations/0019_add_payment_audit_actions_down.sql

\echo 'Running 0018_create_agency_commission_models_down.sql...'
\i migrations/0018_create_agency_commission_models_down.sql

\echo 'Running 0017_create_wallet_models_down.sql...'
\i migrations/0017_create_wallet_models_down.sql

\echo 'Running 0016_add_comment_to_sms_campaigns_down.sql...'
\i migrations/0016_add_comment_to_sms_campaigns_down.sql

\echo 'Running 0015_add_sms_campaign_audit_actions_down.sql...'
\i migrations/0015_add_sms_campaign_audit_actions_down.sql

\echo 'Running 0014_create_sms_campaigns_down.sql...'
\i migrations/0014_create_sms_campaigns_down.sql

\echo 'Running 0013_relax_name_validation_down.sql...'
\i migrations/0013_relax_name_validation_down.sql

\echo 'Running 0012_update_timestamp_defaults_to_utc_down.sql...'
\i migrations/0012_update_timestamp_defaults_to_utc_down.sql

\echo 'Running 0011_add_new_audit_actions_down.sql...'
\i migrations/0011_add_new_audit_actions_down.sql

\echo 'Running 0010_add_customer_uuid_and_agency_id_down.sql...'
\i migrations/0010_add_customer_uuid_and_agency_id_down.sql

\echo 'Running 0009_add_correlation_ids_down.sql...'
\i migrations/0009_add_correlation_ids_down.sql

\echo 'Running 0008_update_audit_log_success_field_down.sql...'
\i migrations/0008_update_audit_log_success_field_down.sql

\echo 'Running 0007_add_missing_audit_actions_down.sql...'
\i migrations/0007_add_missing_audit_actions_down.sql

\echo 'Running 0006_update_customer_fields_down.sql...'
\i migrations/0006_update_customer_fields_down.sql

\echo 'Running 0005_create_audit_log_down.sql...'
\i migrations/0005_create_audit_log_down.sql

\echo 'Running 0004_create_customer_sessions_down.sql...'
\i migrations/0004_create_customer_sessions_down.sql

\echo 'Running 0003_create_otp_verifications_down.sql...'
\i migrations/0003_create_otp_verifications_down.sql

\echo 'Running 0002_create_customers_down.sql...'
\i migrations/0002_create_customers_down.sql

\echo 'Running 0001_create_account_types_down.sql...'
\i migrations/0001_create_account_types_down.sql

\echo 'All migrations rolled back successfully!'
\echo 'Database schema has been completely removed.' 
