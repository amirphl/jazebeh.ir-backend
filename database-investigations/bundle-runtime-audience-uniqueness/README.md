# Bundle runtime audience uniqueness checks

These PostgreSQL/psql queries verify the audiences dedicated to campaigns at
runtime. They do not read Excel uploads, sent-message tables, or phone-number
values. An audience is identified by `audience_id` and is a conflict only when
it is claimed by more than one campaign in the same bundle.

The checks combine every source the runtime uses to exclude an audience:

- `bundle_audience_selection_members`: permanent, materialized allocations for
  both test and execution campaigns;
- `campaign_targeting_test_sample_reservations`: active test-phase reservations;
- `campaign_targeting_execution_reservations`: active execution reservations;
- `campaign_targeting_execution_calculation_members`: committed execution
  calculations, which the runtime treats as active reservations for legacy and
  transition paths.

Set the bundle ID before running a query:

```sql
\set bundle_id 123
```

Run `01_cross_campaign_duplicate_audiences.sql` first. Zero rows means no
audience is currently dedicated to multiple campaigns in the tracked runtime
claim sources. Use
`02_cross_campaign_duplicate_audience_details.sql` to inspect the sources and
campaigns behind a conflict. `03_runtime_audience_uniqueness_assertion.sql`
also reports `has_runtime_claims`; when it is false, the uniqueness result is
null because there were no tracked claims to validate. `04_runtime_claim_data_integrity.sql`
finds malformed claim rows that would not safely map to a campaign.
`05_sent_campaign_claim_coverage.sql` compares sent campaigns with their
available claim evidence; use it when sent-message history exists but the other
runtime queries return no claim rows.
