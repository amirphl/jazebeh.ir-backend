# Bundle, Campaign, and Smart Targeting review guide

This is a code-review map for the business flows whose files begin with
`bundle_`, `bundles_`, `campaign_`, or `smart_`.  It also names the adjacent
flows that change their inputs, money, or runtime outcome.  Start at the
entrypoints in each group; the helpers lower in the same file implement their
invariants.

## Recommended reading path

```text
Bundle definition
  -> tag evaluation (optional AI scoring)
  -> Smart Targeting tag selection
  -> capacity or test-sample calculation
  -> campaign creation/update and approval
  -> execution-audience reservation
  -> platform scheduler sends and reports delivery
  -> refund reconciliation / reports
```

The campaign state machine is in [models/campaign.go](../models/campaign.go):
`initiated` / `in-progress` -> `waiting-for-approval` -> `approved` ->
`running` -> `executed`. Rejection, cancellation, interruption, and expiry
are alternative paths. Review state transitions and wallet/reservation release
together, rather than as isolated methods.

## 1. Bundle lifecycle and enrichment

Read these first to understand the shared object that Smart Targeting campaigns
use.

| Flow / file | Entry functions | What to review |
| --- | --- | --- |
| [bundles_flow.go](bundles_flow.go) | `CreateBundle`, `UpdateBundle`, `GetBundle`, `ListBundles` | Ownership, validation, mutation rules when campaigns exist, audit records, and evaluation status included in reads. |
| [bundle_tag_evaluation_flow.go](bundle_tag_evaluation_flow.go) | `RequestBundleTagEvaluation`, `GetBundleTagEvaluationStatus`, `ListBundleTagScores`, `ExecuteBundleTagEvaluationRun` | Durable AI run lifecycle: snapshot configuration, daily/concurrent limits, persona analysis, batch/retry/pacing behavior, response validation, and current-score publication. Worker entrypoint is `ExecuteBundleTagEvaluationRun`. |
| [bundle_action_flow.go](bundle_action_flow.go) | `Upload`, `Get`, `List`, `Delete`, `Summary`, `TagMetrics`, `CampaignMetric`, `ExecuteBundleActionFile` | Owner-scoped action-file intake, XLSX UID parsing, leased worker execution, and its bundle/campaign metrics. |

The evaluation output is consumed by `smart_targeting_flow.go` for tag ranking.
The `BundleTagEvaluation*` repositories hold the run, events, batches,
attempts, scores, and read model; review them with the flow when checking
durability or retry behavior.

## 2. Smart Targeting configuration and calculations

These flows are the Smart Targeting control plane. They validate inputs and
persist durable calculation jobs; schedulers execute the expensive queries.

| Flow / file | Entry functions | What to review |
| --- | --- | --- |
| [smart_targeting_flow.go](smart_targeting_flow.go) | `ListTags`, `ListBundleTags`, `GetSelection`, `ReplaceSelection`, `AutoSelect` | Campaign/bundle ownership; evaluation-aware sorting; pagination; and atomic, ordered selection replacement. Selected-tag order is semantically significant. |
| [smart_targeting_capacity_flow.go](smart_targeting_capacity_flow.go) | `Start`, `GetCurrent`, `GetByID`, `ExecuteCampaignTargetingCapacityCalculation` | Exact capacity generations, input/allocation fingerprints, expiry, idempotent active requests, leases, and status polling. |
| [smart_targeting_sampling_flow.go](smart_targeting_sampling_flow.go) | `StartSmartTargetingTestSampling`, `GetCurrentSmartTargetingTestSampling`, `GetSmartTargetingTestSamplingByID`, `ExecuteSmartTargetingTestSamplingCalculation` | Test-phase sample sizing, selection/input hash freshness, candidate selection, and sample reservation. These are methods on `CampaignFlowImpl`. |
| [smart_targeting_execution_calculation_flow.go](smart_targeting_execution_calculation_flow.go) | `StartSmartTargetingExecutionCalculation`, `GetCurrentSmartTargetingExecutionCalculation`, `GetSmartTargetingExecutionCalculation`, `ExecuteSmartTargetingExecutionCalculation` | Execution-phase candidate calculation, immutable eligibility contract, generation freshness, and the reservation header/items that prevent overlapping sends. |
| [smart_targeting_delivery_eligibility.go](smart_targeting_delivery_eligibility.go) | Internal eligibility helpers | Line-number color eligibility and delivery constraints used by the preceding calculation paths. |
| [smart_targeting_execution_reservation_metrics.go](smart_targeting_execution_reservation_metrics.go) | Internal metric helpers | Reservation finalization metrics and reason accounting. Review with execution failure/retry cases. |

Supporting repositories worth reading alongside this group are
`campaign_selected_tag_repository.go`, `smart_targeting_audience_repository.go`,
`campaign_targeting_capacity_repository.go`,
`campaign_targeting_test_sampling_repository.go`, and
`campaign_targeting_execution_{calculation,reservation}_repository.go`.

## 3. Customer campaign lifecycle

[campaign_flow.go](campaign_flow.go) is the primary orchestration flow. It
owns customer validation, campaign edits, pricing, wallet freeze/release,
auditing, status changes, reporting, and Smart Targeting finalization. Its
main entry functions are:

- `CreateCampaign`, `UpdateCampaign`, `CloneCampaign`
- `CalculateCampaignCapacity`, `CalculateCampaignCost`, `CalculateCampaignCostV2`
- `CancelCampaign`, `HideCampaigns`, `UnhideCampaigns`
- `ListCampaigns`, `GetLastInitiatedCampaign`, `GetApprovedRunningSummary`
- `ExportCampaignReport`, `ExportCampaignClickReport`,
  `ExportCampaignAudienceClickReport`, `CountTargetAudienceFromExcelFile`, and
  `GetPagePrices`
- `ReconcileUndeliveredCampaignRefund` (leased worker entrypoint)

Review `CreateCampaign` and `UpdateCampaign` first, then the calculation
functions they call. In particular, trace the transaction that validates Smart
Targeting prerequisites, commits the execution calculation/reservation,
freezes funds, and creates audit records. Follow cancellation, expiry, admin
rejection, and refund reconciliation to confirm every reservation and frozen
balance has a matching release/refund path.

Related campaign files:

| File | Entry functions | Purpose |
| --- | --- | --- |
| [campaign_test_send.go](campaign_test_send.go) | `SendCampaignTestMessage` | Pre-flight send with recipient cooldown, platform settings/media validation, optional short-link allocation, and async provider dispatch. |
| [campaign_audience_report_job_flow.go](campaign_audience_report_job_flow.go) | `Create`, `Get`, `RecordDownload`, `ExecuteNext`, `Cleanup` | Customer-owned, durable audience-report export job; includes lease handling, output-size limits, safe output cleanup, and audit events. |
| [campaign_audience_uid_store.go](campaign_audience_uid_store.go) | Internal append/read/visit helpers | Per-campaign UID staging used by bot statistics ingestion. Review file locking, bounds, and cleanup assumptions with the caller. |

`ListAudienceSpec` is implemented in [audience_spec.go](audience_spec.go), even
though it belongs to `CampaignFlow`; include it when reviewing campaign input
validation and filtering.

## 4. Approval, delivery, and operational actors

These flows form the other actors around the customer lifecycle.

| Flow / file | Entry functions | Review focus |
| --- | --- | --- |
| [campaign_admin_flow.go](campaign_admin_flow.go) | `ListCampaigns`, `GetCampaign`, `ApproveCampaign`, `RejectCampaign`, `RescheduleCampaign`, `CancelCampaign`, `UpdatePagePrice`, `GetPagePrices` | Administrative state transitions, scheduling window, platform/price validation, wallet effects, notifications, and release of Smart Targeting sample/execution reservations on terminal paths. |
| [campaign_bot_flow.go](campaign_bot_flow.go) | `ListReadyCampaigns`, `MoveCampaignToRunning`, `MoveCampaignToExecuted`, `DownloadTargetAudienceExcelFile`, `UpdateCampaignStatistics`, `PushCampaignAudienceUIDs` | Worker-facing handoff: ready-campaign filtering, status transitions, target-file access, result/statistics ingestion, and UID staging. |

The actual platform sending loops are intentionally outside `business_flow`:
`app/scheduler/{sms,bale,rubika,splus}_campaign_scheduler.go`. They call the
bot flow and use Smart Targeting selection/reservations, so inspect them after
the two files above. Scheduler wiring and feature gates live in
[main.go](../main.go).

## 5. Scheduler components in scope

Read these after their corresponding business flow. They are selected because
they execute, materialize, finalize, or compensate the bundle/campaign/Smart
Targeting state—not merely because they live in `app/scheduler`.

| Scheduler file(s) | Connected flow | Review focus |
| --- | --- | --- |
| [bundle_tag_evaluation_scheduler.go](../app/scheduler/bundle_tag_evaluation_scheduler.go) | `BundleTagEvaluationFlow.ExecuteBundleTagEvaluationRun` | Pending-run discovery, in-process de-duplication, parallelism bound, timeout, and restart behavior. |
| [bundle_action_file_scheduler.go](../app/scheduler/bundle_action_file_scheduler.go) | `BundleActionFlow.ExecuteBundleActionFile` | Claim/lease duration versus job timeout, parallel execution, and recovery of abandoned action-file jobs. |
| [smart_targeting_capacity_scheduler.go](../app/scheduler/smart_targeting_capacity_scheduler.go), [smart_targeting_test_sampling_scheduler.go](../app/scheduler/smart_targeting_test_sampling_scheduler.go), [smart_targeting_execution_calculation_scheduler.go](../app/scheduler/smart_targeting_execution_calculation_scheduler.go) | The three durable Smart Targeting calculation worker entrypoints | Claim semantics, lease ownership, maximum parallelism, and what happens after failure or process restart. |
| [smart_targeting_capacity_candidates.go](../app/scheduler/smart_targeting_capacity_candidates.go) | Campaign dispatch / persisted test and execution selections | Under the bundle lock, materializes the persisted sample or execution reservation into a bundle audience selection. Its legacy fallback is the only scheduler-time fresh-selection path; check that reservation-aware campaigns fail closed instead. |
| [sms_campaign_scheduler.go](../app/scheduler/sms_campaign_scheduler.go), [bale_campaign_scheduler.go](../app/scheduler/bale_campaign_scheduler.go), [rubika_campaign_scheduler.go](../app/scheduler/rubika_campaign_scheduler.go), [splus_campaign_scheduler.go](../app/scheduler/splus_campaign_scheduler.go) | `BotCampaignFlow` and approved campaigns | Ready-campaign claim/order, shared [campaign_execution_limiter.go](../app/scheduler/campaign_execution_limiter.go), audience materialization, external-provider retries, campaign status transitions, and delivery-status jobs. |
| [campaign_audience_report_scheduler.go](../app/scheduler/campaign_audience_report_scheduler.go) | `CampaignAudienceReportJobFlow.ExecuteNext` | Job timeout and lease behavior for report generation and cleanup. |
| [campaign_refund_reconciliation_scheduler.go](../app/scheduler/campaign_refund_reconciliation_scheduler.go) | `CampaignFlow.ReconcileUndeliveredCampaignRefund` | Discovery cursor, eligibility delay, claim/lease ownership, retry/backoff, and atomic/idempotent refund execution. |
| [tag_test_performance_scheduler.go](../app/scheduler/tag_test_performance_scheduler.go) | Smart Targeting test/execution outcome data | Recomputes attribution-driven tag performance after clicks, sends, or delivery updates; inspect its lease and stale-data behavior with tag ordering/scoring review. |

## 6. Cross-cutting flows needed for a complete review

These do not match the requested filename prefixes, but materially affect the
flows above:

| Flow(s) | Why it belongs in the review |
| --- | --- |
| `payment_flow.go`, `payment_admin_flow.go`, `crypto_payment_flow.go` | Wallet funding, transaction/balance snapshots, and payment evidence that make campaign freeze/refund accounting meaningful. |
| `platform_settings_flow.go`, `platform_settings_admin_flow.go`, `platform_base_price_{,admin_}flow.go`, `segment_price_factor_{,admin_}flow.go`, `line_number_flow.go`, `line_number_admin_flow.go`, `audience_spec.go` | Provider configuration, price factors, sender eligibility/colors, and audience inputs used by cost/capacity/Smart Targeting and test sends. |
| `multimedia_flow.go`, `multimedia_admin_flow.go`, `multimedia_bot_flow.go` | Campaign media ownership and the worker download path. |
| `short_link_visit_flow.go`, `bot_short_link_flow.go`, `admin_short_link_flow.go`, `external_short_link_publication.go` | Per-recipient/test link allocation, publication, and click attribution used by campaign reports. |
| `audit_logging.go`, `errors.go`, `business_flow.go` | Common audit, error, customer lookup, and DTO behavior that determine consistency across all flows. |

## Suggested review checklist

For every mutation or worker entrypoint, verify: authorization/ownership,
allowed status and phase, transaction and lock scope, idempotency/lease guard,
audit trail, and compensation for funds, reservations, files, or external
provider calls. For Smart Targeting specifically, also verify that tag order,
input hashes, allowed colors, allocation fingerprints, and calculation expiry
are checked before results are reused or committed.

Tests are co-located as `*_test.go`; the Smart Targeting and reservation tests
are particularly useful as executable statements of the freshness and
concurrency contracts.
