# Customer campaign lifecycle

This document explains the customer-facing campaign lifecycle as one business process. It covers the campaign flows in `business_flow/`, the repository operations they invoke, and the important work performed between those operations. It deliberately treats the application and PostgreSQL database as single instances: availability replication and single-point-of-failure concerns are therefore out of scope. Contention, long transactions, large scans, and local-file work still matter and are called out below.

## The lifecycle at a glance

```text
customer draft ──> edit / calculate ──> finalize + freeze ──> waiting-for-approval
     │                    │                       │                    │
     │                    │                 Smart Targeting             admin approves
     │                    │                 preview/reservation              │
     │                    │                       │                         approved
     │                    └── test send ──────────┘                           │
     ├── cancel / expire <───────────────────────────────────────────────┐    │
     └── clone                                                        scheduler │
                                                                            v
                                           bot lists ready -> running -> executed
                                                                            │
                                                       reports / clicks / delayed refund
```

The central record is `campaigns`. Its mutable request specification is JSON (`spec`), while state, customer, bundle, Smart Targeting fields, statistics, and audience count are columns. A customer can see only its own campaigns; the bot and administrator flows operate by numeric campaign ID.

### Status and phase

Normal progression is `initiated` (editable draft) -> `waiting-for-approval` -> `approved` -> `running` -> `executed`. `in-progress` is also treated as an editable/test-send state. `rejected`, `cancelled`, `cancelled-by-admin`, `expired`, and `interrupted` are alternate/terminal states. A campaign additionally has a Smart Targeting `test` or `execution` phase when that targeting method is used.

Only the flow methods enforce the transition rules; repository `Update` persists the supplied full record. Therefore callers must not bypass the flows.

## 1. Draft creation, retrieval, and mutation

### CreateCampaign

The flow validates request shape, customer/account type, supported platform, short-link domain, agency category/job, targeting method, budget constraints, bundle/phase compatibility, ownership of media and Excel audience files, active platform settings, line number, and standard segment references. It then creates a campaign in `initiated` state. For Smart Targeting it creates the ordered selected-tag set in the *same transaction*.

Repository work:

| Repository operation | Query/effect and reason |
| --- | --- |
| `CustomerRepository.ByID` (via `getCustomer`) | Fetches the customer and preloaded account information required for authorization and category/job rules. |
| `BundleRepository.ByID` | Reads the referenced bundle; the flow verifies that it belongs to the customer and is valid for the requested phase. |
| `MultimediaAssetRepository.ByUUID` / `ByFilter` | Looks up supplied media or Excel assets and verifies customer ownership/type. |
| `PlatformSettingsRepository.ByFilter` | Finds the exact active settings row for the selected platform/customer context. |
| `LineNumberRepository.ByValue` | Verifies an SMS sender line and later supplies its pricing/provider attributes. |
| `CampaignRepository.Save` | `INSERT`s the campaign within the transaction. |
| `CampaignSelectedTagRepository.Replace` | Locks/validates the campaign and selected tags against the bundle's authoritative tag source, clears prior rows, and inserts ordered selection rows. It also invalidates any test preview. |
| `AuditLogRepository.Save` | Inserts a success or failure audit record after the business transaction; audit failure is intentionally non-fatal. |

### UpdateCampaign and finalization

An editable campaign may be saved as a draft or finalized. Updates merge sparse fields into the effective campaign rather than validating omitted fields as zero values. Changing a Smart Targeting input (method, bundle, phase, sample size, tag order, grades, platform/line-derived permitted colours) invalidates its test preview and supersedes an active sampling job.

Draft update query sequence:

1. `CampaignRepository.ByUUID` reads the campaign using UUID and the flow confirms `customer_id`.
2. Reference reads validate the post-update effective specification (bundle, media/Excel file, platform settings, line number).
3. A transaction locks the campaign row (`LockCampaignForUpdate`, `SELECT ... FOR UPDATE`), re-reads it with `CampaignRepository.ByID`, compares sampling inputs, and calls `CampaignRepository.Update`.
4. If required, `CampaignTargetingTestSamplingRepository.ActiveByCampaignID` reads the active job and `Supersede` conditionally changes an active job to stale. `CampaignSelectedTagRepository.Replace` performs the ordered replacement if new tag IDs were supplied.

Finalization has two intentionally separated stages:

* Outside the money transaction it computes capacity and cost, reads price configuration, and for Smart Targeting requires a current test preview or a ready execution calculation. This prevents DB locks being held during Redis, file, or heavy audience-query work.
* Inside a short transaction it locks the campaign again, rejects a stale `updated_at` revision, freezes the exact budget, stores immutable price inputs in transaction metadata, sets `num_audience`, changes status to `waiting-for-approval`, and creates/validates the correct audience reservation. It then sends administrator notification after commit and writes a best-effort audit entry.

The financial part reads the customer's wallet and newest balance snapshot while the wallet is locked (`LockWalletForUpdate`). It writes a new immutable `balance_snapshots` row that transfers available funds to frozen funds, then writes a completed `transactions` row with the same correlation ID. It does not update balances in place.

### Clone, list, hide, and summary

`CloneCampaign` locks and rereads the source, copies only permitted configuration into a new editable draft with a new UUID, then copies selected tags in their stored order. It never copies status, financial transactions, calculated audience, reservations, or statistics.

`ListCampaigns` / administrator `ListCampaigns` first run `CampaignRepository.Count`, then `ByFilter` with pagination and allowed ordering. The campaign read projection excludes potentially huge `statistics.trackingResults`, preloads customer/account/bundle, and supports filters including customer, status, platform, phase, dates, and schedule time. `AggregateClickCountsByCampaignIDs` then groups `short_link_clicks` by campaign and counts distinct UIDs after filtering automated/test-like traffic. Hide/unhide first query `ByCustomerIDAndIDs` to establish ownership and then issue a customer-scoped bulk `UPDATE` through `MarkHidden`/`MarkVisible`. The summary runs two filtered `COUNT`s for approved and running campaigns.

## 2. Capacity and pricing before finalization

`CalculateCampaignCapacity` is a read-only preflight. Standard targeting obtains audience capacity from the configured data source; Excel targeting parses the owned file; Smart Targeting uses the chosen tags, score classes and line/provider colour eligibility. `CalculateCampaignCost` derives message parts, price per message, total target count, and cost. The V2 form accepts effective input but follows the same pricing policy.

Pricing reads use:

| Repository operation | Query/effect and business meaning |
| --- | --- |
| `PlatformBasePriceRepository.LatestByPlatform` | Reads the most recent base unit price for a platform. |
| `PagePriceRepository.LatestByPlatform` | Reads the current per-message-page price. |
| `SegmentPriceFactorRepository.LatestByLevel3sForPlatform` | Reads latest factors for the selected standard audience segments; the flow combines them and reports missing factor cases. |
| `LineNumberRepository.ByValue` | Reads SMS-line price factor and provider/colour eligibility. |
| `CampaignSelectedTagRepository.Summary` and `Validate` | Gets tag count/order and verifies that selections still belong to the campaign/bundle before Smart Targeting use. |
| `SmartTargetingAudienceRepository.CalculateCapacity` | Runs the audience population CTE and returns raw tagged-union count and eligible unique count after phone, colour, already-used, exclusion, test-reservation, execution-reservation, and committed-calculation exclusions. |

After finalization the calculation used at the time is preserved in the freeze transaction metadata. Later displays prefer that frozen metadata for base price, line factor, and segment factor, avoiding accidental repricing when an administrator changes a price table.

## 3. Smart Targeting: selection, test preview, and execution reservation

Smart Targeting is not a simple filter. A campaign stores an ordered list of tag IDs. The order is meaningful: it determines first matching tag attribution for candidates that have more than one selected tag.

`SmartTargetingFlow.ListTags` and `ListBundleTags` use `CampaignSelectedTagRepository.ListAvailable`, which builds the bundle's tag source from completed evaluation scores when available, otherwise the active tag catalogue, joins test/overall performance summaries and optional action metrics, applies escaped search and capacity filtering, then pages/sorts. `ReplaceSelection` and `AutoSelect` lock the editable campaign and call `Replace`; auto-select first queries `ListAvailableTagIDs` in the requested ranking.

### Test phase

`StartSmartTargetingTestSampling` locks the editable test campaign, validates its selected tags, derives a hash of all relevant inputs, and either reuses an unchanged active/calculated job or saves a new pending `campaign_targeting_test_sampling_calculations` row. The scheduler claims it; the request itself never scans the audience population.

The worker:

1. reads the calculation (`ByID`) and campaign (`CampaignRepository.ByID`), validates the selection;
2. uses `SmartTargetingAudienceRepository.CalculateScoreBounds` once to compute p33/p66 score boundaries when grades restrict the audience;
3. calls `SelectForTag` for each selected tag, excluding already chosen IDs, existing bundle selection, exclusions, and both kinds of active reservation;
4. under the bundle lock verifies the calculation still matches current inputs and writes its tag results, effective count, cost, and allocation fingerprint with conditional `Complete` (only the worker lease that claimed it can complete it).

When finalizing a test campaign, the flow requires that calculated preview, locks the bundle, invalidates any execution reservation, and calls `CampaignTargetingTestSampleSelectionRepository.ReserveForCampaign`. This persists the exact preview members so a scheduler cannot silently choose different recipients later.

### Execution phase

`StartSmartTargetingExecutionCalculation` follows the same durable-job pattern. Its worker selects the requested number of candidates from the frozen input, assigns each candidate to the first selected matching tag, stores ordered members in `campaign_targeting_execution_calculation_members`, and stores an allocation fingerprint. `ClaimPending` uses `FOR UPDATE SKIP LOCKED`, so one worker owns a calculation lease at a time; stale leases can be reclaimed.

Finalization requires a named ready calculation. Under the bundle lock, `ReadyForUpdate` locks the calculation and `Members` locks ordered member rows. `CampaignTargetingExecutionReservationRepository.ReserveForCampaign` checks the exact candidate rows: they must still exist, have a phone number, retain their assigned tag and permitted colour, and not appear in a bundle selection, exclusion, test reservation, or execution reservation. It then inserts one active reservation header and members in batches. The campaign and calculation are marked committed. `Materialize` later turns a validated active reservation into the bundle audience selection used by dispatch; release/invalidate are used by cancellation/rejection/configuration changes.

## 4. Customer cancellation, expiry, and test delivery

Customer `CancelCampaign` locks the campaign and permits cancellation only in allowed non-running states. It locates the unique freeze transaction for a pre-approval campaign, or the approval debit for a post-approval campaign, locks the wallet, appends a compensating balance snapshot and refund transaction, releases test/execution reservations, changes the campaign status, then audits after commit. `expireCustomerCampaigns` finds overdue waiting-for-approval campaigns and performs the same compensation per campaign in transactions.

`SendCampaignTestMessage` is available only to editable campaigns. It reads customer and owned campaign, verifies content/platform, then uses Redis `SETNX` for a 30-second customer cooldown. If the campaign has an ad link, it substitutes a fake UID; if it has a short-link domain, it reads `ShortLinkRepository.ByAllocationKey` and, in a transaction, rechecks it, obtains exactly one code with `ReserveSequentialUIDs`, and inserts a `short_links` row. This makes retries reuse the same test link. It reads the line number/media/platform settings as needed, dispatches the provider call asynchronously/best-effort, and records the attempt in the audit log. A provider soft error is returned as a warning because an attempted delivery may still succeed.

## 5. Administrator review and finance

`ApproveCampaign` locks the campaign, verifies `waiting-for-approval`, schedule window, current platform settings, frozen Smart Targeting test intent/reservation, then obtains the unique freeze transaction. With the wallet row locked it creates a balance snapshot that moves the frozen amount into campaign spend, inserts a completed fee/debit transaction, changes the campaign to `approved`, and keeps required reservations. Approval uses the metadata frozen during finalization for price evidence rather than live price tables.

`RejectCampaign`, administrator `CancelCampaign`, and customer cancellation share the compensating pattern: lock campaign and wallet, find exactly one matching freeze/debit transaction, create a new balance snapshot and refund transaction, release both Smart Targeting reservations, and set the appropriate terminal status/comment. `RescheduleCampaign` updates an approved campaign only after its lead-time and schedule validation. `UpdatePagePrice` inserts a new price-history row rather than mutating old campaign evidence; `ListLatest` returns one latest row per platform.

## 6. Dispatch, delivery results, and delayed reconciliation

The platform scheduler is outside this directory, but its contract is exposed by `BotCampaignFlow`:

* `ListReadyCampaigns` uses `CampaignRepository.ByFilter` for approved campaigns scheduled no later than now and no more than one hour ago, optionally restricted by platform. It verifies selected tags and Smart Targeting test intent, reads platform settings, and resolves the frozen platform base price from the finalization transaction metadata (falling back to current base price for legacy campaigns).
* `MoveCampaignToRunning` and `MoveCampaignToExecuted` each lock, reread, verify the exact predecessor status, and update it. The executed transition relies on a DB trigger to enqueue refund reconciliation durably.
* `DownloadTargetAudienceExcelFile` reads campaign and owned multimedia metadata, sanitizes the stored path, then reads the file bytes for Excel-targeted dispatch.
* `UpdateCampaignStatistics` locks the campaign and calls `MergeStatistics`, a JSONB `statistics = statistics || patch` update. This preserves financial-refund fields while delivery reporters add sent/delivery fields.
* `PushCampaignAudienceUIDs` appends scheduler chunks to the local durable campaign UID/code store. Those records are later report input, not the campaign table.

`ReconcileUndeliveredCampaignRefund` is executed from the durable queue after the configured delay (96 hours by default), never from a read request. It locks the campaign and takes a transaction advisory lock, requires `executed`, a schedule, audience count, completed approval debit, and delivery statistic `aggregatedTotalSent`. If sent < finalized audience, it derives the original cost-per-message from transaction metadata, locks the wallet, appends one refund snapshot/transaction, and JSONB-merges an idempotency marker into statistics. `HasCompletedCampaignPartialRefund` prevents duplicate financial compensation.

## 7. Reports and audience-export jobs

`ExportCampaignReport` and click exports first prove ownership with campaign queries. `ShortLinkClickRepository.DistinctShortLinkUIDsByCampaignID(s)` returns distinct clicked link UIDs; campaign click aggregation consistently excludes known automated/test traffic. The report flow compares clicked IDs against active bundle-action UIDs where applicable and writes an in-memory XLSX/CSV response.

For large audience-click exports, `CampaignAudienceReportJobFlow` provides a durable asynchronous path:

1. `Create` validates at most 100 unique campaign IDs, reads them with `CampaignRepository.ByCustomerIDAndIDs`, and atomically inserts a pending job through `CreateWithinCustomerLimit` (per-customer retained-job limit).
2. `ClaimDue` atomically claims one pending or expired-lease job with a lease token. The worker rechecks ownership, streams each campaign's UID/code JSONL file, and upserts batches into `campaign_audience_report_job_rows` keyed by job/lease/campaign/UID.
3. It joins staged rows to campaigns/short links/clicks, writes XLSX sheets with row and byte caps, publishes an attempt-specific output path, and conditionally records completion through `UpdateClaimed`.
4. `ExpiredOutputPaths`, `MarkExpiredOutput`, `ReferencedOutputPaths`, and `PurgeTerminalBefore` support safe cleanup. Download is audited but does not mutate the job.

## Repository query reference

The following are the repository-level operations most material to this lifecycle. `Save` means an insert; `Update` means a full-row GORM save and must only follow a fresh locked read. Calls executed with `repository.WithTransaction` share that transaction through context.

| Area | Operations | What is queried or changed |
| --- | --- | --- |
| Campaign | `ByID`, `ByUUID`, `ByFilter`, `Count`, `ByCustomerIDAndIDs` | Reads campaign projection (excluding large tracking results), often preloading customer/account/bundle; filter listing and ownership checks. |
| Campaign mutation | `Save`, `Update`, `UpdateStatus`, `MarkHidden`, `MarkVisible`, `MergeStatistics` | Inserts/updates campaign state; bulk customer-scoped visibility updates; atomic JSONB patch merge. |
| Reporting | `AggregateClickCountsByCampaignIDs`, `DistinctShortLinkUIDsByCampaignID(s)` | Grouped distinct-UID clicks and per-campaign clicked-code sets, with automated traffic excluded for aggregate counts. |
| Financial evidence | `WalletRepository.ByCustomerID`, `GetCurrentBalance`; `BalanceSnapshotRepository.Save`; `TransactionRepository.ByFilter`, `Save`, `HasCompletedCampaignPartialRefund` | Finds the wallet/current snapshot; appends immutable balances and transactions; locates exactly the freeze/debit by campaign/source/operation/type/status; tests refund idempotency. |
| Prices and delivery configuration | `LatestByPlatform`, `LatestByLevel3sForPlatform`, `ByValue`, `PlatformSettings.ByID/ByFilter` | Current price histories, segment/line factors, and active sender/platform configuration for pre-finalization validation. |
| Smart tag selection | `ListAvailable`, `ListAvailableTagIDs`, `ListSelected`, `Summary`, `Validate`, `Replace` | Reads authoritative tags/performance; reads ordered selected rows; verifies bundle membership; transactionally replaces ordered choices. |
| Sampling calculations | `ActiveByCampaignID`, `Latest*`, `Save`, `ClaimPending`, `Complete`, `Fail`, `Supersede` | Durable input-hashed work records, lease claim, conditional terminal transition, and stale input invalidation. |
| Execution calculations | `Latest*`, `ReadyByInput`, `Save`, `ClaimPending`, `Members`, `Complete`, `Finish`, `ReadyForUpdate`, `MarkCommitted` | Durable candidates and lease states; member locking; conditional ready/failed/stale/committed transitions. |
| Execution reservation | `ReserveForCampaign`, `ActiveReservedForCampaign`, `ValidateMaterializedForCampaign`, `Materialize`, `ReleaseForCampaign`, `InvalidateForCampaign` | Atomically validates and persists exact recipients; later validates/materializes or releases them. |
| Smart audience SQL | `CalculateCapacity`, `CalculateScoreBounds`, `SelectCandidates`, `SelectForTag` | CTE population scans, percentile bounds, candidate selection and per-tag selection while excluding unavailable audiences. |
| Files/links/audit | `MultimediaAsset.ByUUID/ByFilter`, `ShortLink.ByAllocationKey/ReserveSequentialUIDs/Save`, `AuditLog.Save` | Verifies owned files, allocates idempotent test links, and records non-financial audit events. |
| Audience report jobs | `CreateWithinCustomerLimit`, `ByIDForCustomer`, `ClaimDue`, `UpdateClaimed`, cleanup methods | Job ownership/limit, `SKIP LOCKED`-style leasing, compare-and-set completion, and retention cleanup. |

## Logical issues and correctness gaps

These are current code observations, not availability/replication concerns.

1. **Schedule and content validation is knowingly incomplete.** `UpdateCampaign` contains TODOs for URL/length validation, segment existence, future/minimum schedule time, and line-number availability for the campaign schedule. A draft can therefore pass update validation with data that later fails operationally or conflicts with another campaign.
2. **Test sampling is neither random nor reproducible.** `SmartTargetingAudienceRepository.SelectForTag` has no `ORDER BY`; PostgreSQL returns planner/storage-dependent limited rows. The code explicitly records this TODO. Test outcomes can vary across equivalent previews and may be biased by physical layout.
3. **Restricted score grades can become semantically stale.** Execution reservation checks that each selected member's exact score did not change, but it does not preserve/recheck the population p33/p66 bounds. A change to *another* audience can shift A/B/C class boundaries and leave a member incorrectly classified; this is also documented as a TODO in the repository.
4. **Ready dispatch has a hard one-hour lateness window.** `ListReadyCampaigns` filters `schedule_after = now - 1 hour`. An approved campaign missed for more than an hour is not returned by this path and can remain approved indefinitely without an explicit recovery process.
5. **Audience UID report input is outside the database transaction.** Scheduler `PushCampaignAudienceUIDs` appends local files separately from status/database writes. A crash between them can make delivery statistics and audience reports disagree; the files are intentionally retained indefinitely, which also lacks a lifecycle/retention policy.
6. **Audit and notifications are not transactional outbox events.** Most audit writes and all customer/admin notifications happen after commit and errors are ignored. The primary business result stays correct, but an operator cannot infer notification/audit completeness solely from the campaign state.
7. **Partial undelivered refunds are always credited to `CreditBalance`.** The code contains a commented-out plan to reconstruct the original free-versus-credit funding split. Current reconciliation debits `SpentOnCampaign` and credits the entire refund to credit, which can change the customer’s balance classification from the original funding source.
8. **Reporting views intentionally omit `trackingResults`.** Campaign repository reads exclude it to protect list performance. Any new consumer that expects full tracking details from `ByID`/`ByFilter` will silently not receive them and must use a dedicated read path.

## Bottlenecks and scaling limits

1. **Smart Targeting population SQL is the dominant database cost.** Capacity, percentile, and selection CTEs scan/filter `audience_profiles` and multiple anti-join reservation/selection tables; percentile calculation sorts eligible scores. Large tag unions and restricted grades amplify CPU, memory, and index pressure.
2. **Bundle and campaign locks serialize important work.** Finalization locks campaign, wallet, and (for Smart Targeting) bundle; reservation additionally locks candidate profiles. This is correct for no-overlap guarantees but campaigns sharing a bundle or wallet queue behind one another. Keep all expensive candidate discovery outside those transactions as the current flow does.
3. **Execution reservation write volume grows linearly with audience.** Candidate members and reservation members are inserted in batches of 1,000. Large campaigns produce substantial WAL/index work and a long-enough critical section to contend with other bundle users.
4. **Ready-list enrichment has N+1 reads.** `ListReadyCampaigns` explicitly labels per-campaign frozen-price lookup as N+1; platform settings are also loaded once per distinct ID rather than in a single `IN` query. Large ready queues cause avoidable DB round trips.
5. **Excel and report work is local-disk and CPU intensive.** Excel audience counting/download reads full files; audience report jobs stage up to 10 million rows, perform batch upserts, join data, and write up to 5 GiB XLSX output. These operations compete for the single database and host disk/CPU.
6. **JSONB statistics and click reporting become hot rows/scans.** Delivery updates serialize on the campaign row to preserve refund evidence. Click counts require distinct aggregation and bot filtering; the appropriate indexes and report date bounds are essential.
7. **Financial transitions serialize per wallet.** The immutable snapshot ledger is safe, but every freeze, approval, cancellation, expiry, and refund locks the wallet and reads the latest snapshot. High activity from one customer is necessarily sequential.
8. **External provider calls and status latency gate completion.** Dispatch is asynchronous, but campaigns cannot safely complete/refund until provider reporting populates the aggregated sent statistic. The 96-hour reconciliation delay avoids premature refunds but delays customer settlement.

