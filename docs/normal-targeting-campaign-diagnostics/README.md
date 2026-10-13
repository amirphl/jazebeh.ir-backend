# Normal-targeting campaign diagnostics

These standalone PostgreSQL queries primarily inspect standard
(normal-targeting) campaign selection, execution, and delivery. The reporting
folder also contains an explicitly named Smart Targeting bundle-capacity
report. They are grouped by intent:

- [`reporting/`](reporting/) contains descriptive counts, capacity reports, and
  reduction funnels.
- [`validation/`](validation/) contains invariant checks and integrity audits.

Before running a query, update the `params` CTE near the top of that file. Most
queries accept a `campaign_id` or `bundle_id`; the pre-execution availability
report accepts both and can automatically select the latest standard SMS
campaign in a bundle.

The queries intentionally derive normal-targeting inputs from `campaigns.spec`:

- `tags` determine audience membership.
- `level1`, `level2s`, and `level3s` resolve `p33` and `p66` in
  `src_layer_all_stats`.
- `audience_grades` determines the `normalized_score` predicate.

For the standard reports, `campaign_selected_tags` and
`campaign_audience_tag_attributions` are not the source of truth. Normal
targeting does not persist a per-audience assigned tag or score snapshot, so
membership and grade checks use the current `audience_profiles` rows. The
explicitly named Smart Targeting report instead reads its ordered selection
from `campaign_selected_tags` and its actual allocation from the shared bundle
ledger.

## Reporting queries

| Query | Scope |
|---|---|
| [`selected-audience-by-tag-and-level.sql`](reporting/selected-audience-by-tag-and-level.sql) | Selected audience counts by configured tag, level, color, and grade |
| [`final-selected-audience-color-counts.sql`](reporting/final-selected-audience-color-counts.sql) | Final processed audience counts for white, pink, and black profiles |
| [`completely-delivered-sms-count.sql`](reporting/completely-delivered-sms-count.sql) | SMS messages to the final audience for which all parts were delivered |
| [`bundle-audience-reduction-funnel.sql`](reporting/bundle-audience-reduction-funnel.sql) | Bundle-wide capacity reduction for each standard campaign |
| [`smart-targeting-bundle-audience-reduction-funnel.sql`](reporting/smart-targeting-bundle-audience-reduction-funnel.sql) | Bundle-wide capacity and white/pink/black reduction for each Smart Targeting campaign |
| [`available-audience-by-grade-combination.sql`](reporting/available-audience-by-grade-combination.sql) | Available white/pink audience for hypothetical grade combinations |
| [`pre-execution-audience-availability.sql`](reporting/pre-execution-audience-availability.sql) | Scheduler-equivalent availability before execution |
| [`customer-financial-flow.sql`](reporting/customer-financial-flow.sql) | Immutable customer wallet audit trail from a timestamp |
| [`refunded-campaigns.sql`](reporting/refunded-campaigns.sql) | Campaign refunds, original charges, and refund metadata from a timestamp |
| [`campaigns-awaiting-refund.sql`](reporting/campaigns-awaiting-refund.sql) | Charged cancelled/rejected or eligible under-delivered campaigns without a refund |
| [`exact-count-allocation-failure/`](reporting/exact-count-allocation-failure/) | Focused investigation of exact-count allocation failures |

## Validation queries

| Query | Scope |
|---|---|
| [`campaign-configuration-and-score-bounds.sql`](validation/campaign-configuration-and-score-bounds.sql) | Campaign configuration, tag scope, and percentile bounds |
| [`selected-profile-eligibility.sql`](validation/selected-profile-eligibility.sql) | Selected profiles against scheduler eligibility rules |
| [`campaign-audience-count-reconciliation.sql`](validation/campaign-audience-count-reconciliation.sql) | Requested, selected, processed, and sent totals |
| [`selection-ledger-integrity.sql`](validation/selection-ledger-integrity.sql) | Ledger scope, uniqueness, order, and bundle reuse |
| [`processed-campaign-checkpoint.sql`](validation/processed-campaign-checkpoint.sql) | Current processed-campaign checkpoint consistency |
| [`provider-send-integrity.sql`](validation/provider-send-integrity.sql) | Provider status, recipient sets, and duplicates |
| [`standard-campaign-selection-audit.sql`](validation/standard-campaign-selection-audit.sql) | Full selected-audience audit across a bundle |
| [`bundle-selection-integrity-audit.sql`](validation/bundle-selection-integrity-audit.sql) | Faster ledger/checkpoint audit without `audience_profiles` |
