# Bundle sent-phone duplicate root-cause checks

These queries investigate duplicate sent phone numbers after the allocation
checks pass. They distinguish three separate failure modes:

- one phone is present on multiple `audience_profiles` rows, so unique audience
  IDs can still send to the same phone;
- the same selected audience ID is associated with more than one campaign;
- duplicate sends come from different campaigns, different processing attempts,
  or multiple send records in one processing attempt.

The queries use `347 -- :'bundle_id'::integer` in every bundle filter. Replace
`347` to investigate another bundle while leaving the commented psql variable
as a reminder of the original parameter form.

Start with `01_duplicate_phone_profile_identity.sql`, then use
`02_duplicate_phone_send_scope.sql` for retry versus cross-campaign evidence.
Use `03_trace_one_duplicate_phone.sql` on representative numbers to establish
whether the profile ID was in each processed campaign's selected audience.
`04_duplicate_phone_targeting_mix_summary.sql` gives the bundle-wide split by
Excel, Smart Targeting, and standard campaigns.
`05_reused_excel_file_campaigns.sql` identifies re-executed uploaded files;
`06_excel_sent_overlap_with_materialized_audiences.sql` quantifies every Excel
delivery that bypassed a pre-existing materialized audience allocation.
`07_duplicate_sent_audience_attempt_report.sql` is the retained, row-per-send
audit report for every duplicated audience and every campaign attempt.
Run `../bundle-runtime-audience-uniqueness/05_sent_campaign_claim_coverage.sql`
to see whether the affected Smart Targeting campaigns have any recorded runtime
claims. Excel campaigns deliberately have no bundle allocation claims.
