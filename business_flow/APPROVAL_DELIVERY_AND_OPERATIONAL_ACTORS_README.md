# Approval, delivery, and operational actors

This reference explains the actors that take a finalized customer campaign from `waiting-for-approval` to delivery, reporting, and (when necessary) refund. It covers the administrator flow, bot-facing flow, schedulers that perform the operational work, and their repository-level queries. It assumes one application instance and one database instance. Availability replication and single-point-of-failure analysis are intentionally excluded; contention, query cost, local-file work, and external-provider latency are not.

The preceding customer flow owns draft creation, pricing, and finalization. It atomically freezes the campaign budget and sets `waiting-for-approval`. Operational actors must preserve that frozen financial and audience intent rather than recalculate it from current configuration.

```text
waiting-for-approval
  ├─ administrator reject/cancel ──> refund + release audience reservations
  └─ administrator approve ──> consume frozen amount -> approved
                                              │
                         platform scheduler claims ready campaign
                                              v
                                         running / checkpoint
                                              │
                    provider submission, status polling, statistics patches
                                              v
                                           executed
                                              │
       durable refund job ──> no-op | partial refund | manual review / retry
```

## 1. Administration: review and decision

### Administrative list and detail

`AdminCampaignFlow.ListCampaigns` normalizes pagination and optional title, bundle, customer, status, and date filters. It calls:

| Repository call | Query and purpose |
| --- | --- |
| `CampaignRepository.Count(filter)` | `COUNT(*)` using the same filter to supply total pages. |
| `CampaignRepository.ByFilter(filter, schedule order, limit, offset)` | Reads the selected campaign projection, preloading customer/account/bundle and omitting large `statistics.trackingResults`. The custom ordering places editable campaigns last, otherwise prioritizes unscheduled/upcoming/past schedules safely even for malformed legacy timestamps. |
| `CampaignRepository.AggregateClickCountsByCampaignIDs(ids)` | Groups `short_link_clicks` by campaign and counts distinct audience UIDs after excluding test/automated traffic. |
| `PlatformBasePriceRepository.LatestByPlatform` | Reads the newest base-price history row per platform, cached within the list request. |
| `TransactionRepository.ByFilter` | For an individual display, locates the finalization freeze transaction and extracts price factors from its immutable metadata when present. |

`GetCampaign` performs the same `ByID`, click aggregation, and frozen-price enrichment for one campaign. Live current price tables are a fallback for legacy rows only. This distinction is important: an administrator changing current prices must not change the explanation of what a finalized campaign cost.

### Approval

`ApproveCampaign` is one database transaction because it changes status, financial ledger, and (indirectly) dispatch eligibility together.

1. `LockCampaignForUpdate` issues `SELECT ... FOR UPDATE`, then `CampaignRepository.ByID` reloads the current row. The flow requires `waiting-for-approval` and a future schedule.
2. Non-Excel campaigns also call `LockBundleForUpdate`. This serializes new shared-audience commitments with capacity workers and prevents a campaign from being approved against concurrently consumed bundle capacity.
3. A Smart Targeting test campaign resolves its frozen sampling intent and reads `CampaignTargetingTestSampleSelectionRepository.ActiveReservedForCampaign`; exact member count and active selection ID must still match. Non-Smart/Excel campaigns do not take this route. `validateApprovalPlatformSettings` uses `PlatformSettingsRepository.ByID` to confirm ownership, platform identity, and platform-specific metadata (Bale bot ID, Rubika service ID, or SPlus bot ID).
4. `CustomerRepository.ByID` and `WalletRepository.ByCustomerID` obtain parties and wallet. `TransactionRepository.ByFilter` searches for exactly one completed `freeze` transaction with source `campaign_update`, operation `reserve_budget`, and this campaign/customer. Zero or multiple rows reject the operation.
5. `LockWalletForUpdate` serializes balance changes for all of the customer's campaigns. `WalletRepository.GetCurrentBalance` reads the latest append-only snapshot. The flow confirms that frozen balance covers the frozen transaction amount.
6. `BalanceSnapshotRepository.Save` inserts a new snapshot moving that amount from `FrozenBalance` to `SpentOnCampaign`; `TransactionRepository.Save` inserts the matching completed `fee` transaction sharing the freeze correlation ID. `CampaignRepository.Update` sets `approved` and stores an optional comment.

Only after commit does the flow send the customer notification and write an administrative audit log. Notification/audit delivery does not determine whether approval succeeded.

### Rejection and administrator cancellation

`RejectCampaign` is limited to `waiting-for-approval` and requires a non-empty reason. It repeats the campaign lock, unique freeze-transaction lookup, wallet lock, and latest-snapshot read. It then inserts a compensating balance snapshot and refund transaction, releases both kinds of Smart Targeting reservation, changes the status to `rejected`, and records the comment.

`AdminCampaignFlow.CancelCampaign` has two financial branches:

* before approval, it locates the finalization freeze transaction and returns its amount from frozen balance;
* after approval, it locates the administrator approval `fee` transaction and returns the amount from `SpentOnCampaign`.

Both branches write a new snapshot plus completed refund transaction in the same transaction, then call `CampaignTargetingTestSampleSelectionRepository.ReleaseForCampaign` and `CampaignTargetingExecutionReservationRepository.ReleaseForCampaign` before saving `cancelled-by-admin`. This release is essential: recipient reservations must not permanently block another campaign after money is refunded.

### Reschedule and page-price administration

`RescheduleCampaign` uses `CampaignRepository.ByID`, checks status and a UTC timestamp, treats certain missed approval campaigns specially, enforces a two-minute lead time, updates `spec.schedule_at`, then uses `CampaignRepository.Update`. It is an administrative scheduling correction, not a financial recalculation.

`UpdatePagePrice` inserts a new `page_prices` history row with `PagePriceRepository.Insert`; it does not overwrite earlier rows. `GetPagePrices` calls `ListLatest`, a one-row-per-platform latest-price query. Finalized campaigns retain their price evidence in the freeze transaction metadata.

## 2. Bot contract: the boundary between business state and schedulers

Schedulers do not write campaign status directly. `BotCampaignFlow` presents a small authenticated API/contract.

### Finding runnable campaigns

`ListReadyCampaigns(platform)` calls `CampaignRepository.ByFilter` with:

* `status = approved`;
* schedule at or before current UTC time;
* schedule no more than one hour in the past; and
* optional validated platform.

For every returned campaign it applies important interpretation logic before returning the DTO:

| Read | Why it is needed |
| --- | --- |
| `CampaignSelectedTagRepository.Validate` + `ListSelected` | Verifies Smart Targeting choices still belong to the campaign's bundle and returns tag IDs in persisted selection order. |
| frozen test selection resolution | For Smart Targeting test, obtains the immutable satisfied-tag intent and effective audience count rather than trusting mutable draft fields. |
| `TransactionRepository.ByFilter` | Looks up finalization's `freeze/reserve_budget` transaction and reads the frozen base price from metadata. |
| `PlatformBasePriceRepository.LatestByPlatform` | Legacy fallback only if frozen metadata is unavailable. |
| `PlatformSettingsRepository.ByID` | Reads each distinct platform-settings row and puts its required provider configuration in the bot DTO. |

This returns configuration only; it does not claim a campaign. The scheduler must call `MoveCampaignToRunning` before sending.

### State transitions and data handoff

`MoveCampaignToRunning` and `MoveCampaignToExecuted` both use the same short transaction:

1. lock the campaign row;
2. reread it with `CampaignRepository.ByID`;
3. require exactly `approved` -> `running`, or `running` -> `executed`;
4. persist with `CampaignRepository.Update`.

The second transition relies on a database trigger to enqueue a `campaign_refund_reconciliation_jobs` row within that status transaction. Therefore an executed campaign obtains durable reconciliation work even if the scheduler process exits immediately afterwards.

`DownloadTargetAudienceExcelFile` reads the campaign then `MultimediaAssetRepository.ByUUID`, validates Excel targeting and sanitizes the stored path before reading bytes. `UpdateCampaignStatistics` locks/reloads the campaign and uses `CampaignRepository.MergeStatistics`: PostgreSQL JSONB concatenation updates only provided keys, preserving refund proof/markers against concurrent status reporting. `PushCampaignAudienceUIDs` appends UID/code chunks to the campaign's file-backed audience store; that file is later the source for audience reports.

## 3. Platform schedulers and delivery durability

SMS, Bale, Rubika, and SPlus schedulers follow the same broad sequence, with platform-specific provider clients and status repositories. The actual scheduler implementations reside in `app/scheduler/` because they own timing, HTTP calls, provider semantics, and retry behavior; their persistence contracts are summarized here.

1. On each interval, a scheduler recovers stale running claims, authenticates to the bot flow, asks for ready campaigns for its platform, validates the DTO, and skips campaigns that already have a `ProcessedCampaignRepository.ByCampaignID` checkpoint.
2. A shared `CampaignExecutionLimiter` constrains concurrent campaign execution. The scheduler claims the campaign through `MoveCampaignToRunning`; only the winner can begin.
3. It creates/updates a current `processed_campaigns` checkpoint and materializes the audience. Standard and legacy Smart Targeting selection is inserted through `BundleAudienceSelectionRepository.InsertForCampaign`, which locks the bundle, rejects duplicate/invalid IDs, and inserts normalized selection members in batches of 1,000. A retry reads the original allocation with `ByCampaignID` rather than allocating recipients again.
4. Modern Smart Targeting is fail-closed. Test uses `CampaignTargetingTestSampleSelectionRepository.ActiveReservedForCampaign`; execution uses `CampaignTargetingExecutionReservationRepository.ActiveReservedForCampaign`. The scheduler creates the bundle selection from exactly those persisted member IDs, writes attribution rows, then calls `Materialize`. It will not substitute a fresh audience for a reservation-aware campaign. Only explicit pre-migration execution campaigns may take the legacy fresh-selection path.
5. Before an external provider call, the scheduler writes sent-message intent/audit data (`sent_sms`, `sent_bale_messages`, `sent_rubika_messages`, or `sent_splus_messages`) and periodically calls `TouchRunningCampaign`. This durable intent is the no-replay boundary: a process crash after possible provider acceptance cannot safely resend.
6. Delivery status workers store provider results (`SMSStatusResultRepository.SaveBatch` for SMS and equivalent platform repositories), aggregate results, and patch campaign statistics through the bot flow. They use durable `campaign_status_jobs` with retry/lease handling rather than relying on an in-memory timer.
7. On success, scheduler calls `MoveCampaignToExecuted`. A stale run with no persisted send intent is returned to `approved`; one with any real sent-message intent becomes terminal `interrupted` for operator action. This deliberately favors no duplicate messages over automatic retry.

## 4. Audience and click report operations

Small exports use synchronous campaign/click queries, but the audience-click export uses a durable job because its input may be huge.

### Job creation and customer access

`CampaignAudienceReportJobFlow.Create` validates one to 100 distinct nonzero campaign IDs, extracts authenticated customer ID, and calls `CampaignRepository.ByCustomerIDAndIDs`. It refuses the request unless every requested ID belongs to that customer. `CampaignAudienceReportJobRepository.CreateWithinCustomerLimit` takes a transaction-scoped advisory lock keyed by customer, counts pending/processing/unexpired completed jobs, and inserts only if below the per-customer limit. This avoids a concurrent count-then-insert limit race.

`Get` calls `ByIDForCustomer` and repeats ownership verification with `ByCustomerIDAndIDs`, so a queued job cannot export a campaign that has since changed owner. Downloads only add a best-effort audit entry.

### Worker, staging, output, and cleanup

`ClaimDue` executes a transaction with `FOR UPDATE SKIP LOCKED`: it selects one pending or expired-lease job, assigns a fresh lease token/expiry, increments attempts, and returns the claimed row. The worker then:

1. rechecks ownership and reads each campaign UID/code file in streaming batches;
2. upserts `campaign_audience_report_job_rows` with `INSERT ... SELECT unnest(...) ON CONFLICT (job_id, lease_token, campaign_id, audience_uid) DO UPDATE`, deduplicating repeated UID records while allowing later codes to replace earlier ones;
3. runs page queries joining staged UIDs to campaign, short-link, and click data, writes streaming XLSX sheets, and enforces configured row and byte limits;
4. writes to a lease-specific `.partial` file, atomically publishes the XLSX attempt, then calls `UpdateClaimed`, which conditionally clears only the matching lease token and records completion. A worker that lost its lease cannot overwrite a newer result.

`Cleanup` calls `ExpiredOutputPaths`, deletes each corresponding output, then `MarkExpiredOutput`; it removes old unreferenced attempt files and finally `PurgeTerminalBefore` deletes only expired/failed metadata older than 30 days. The scheduler runs cleanup before dispatching bounded parallel jobs and ensures lease exceeds job timeout.

## 5. Executed-campaign refund reconciliation

The database trigger normally enqueues future executed campaigns. `CampaignRefundReconciliationRepository.DiscoverPending` is a bounded historical backfill: it locks one scheduler-state cursor row, scans only the next bounded ID window of executed campaigns, inserts missing jobs with `ON CONFLICT DO NOTHING`, and advances the cursor. Malformed legacy schedules become `manual_review`, not a scan failure.

`ClaimPending` atomically selects eligible pending/retry or expired-processing jobs with `FOR UPDATE SKIP LOCKED`, increments attempt count, records `started_at`/lease expiry, and returns jobs. The scheduler limits concurrent work and, based on the executor result, uses guarded `Complete`, `Retry` (exponential backoff), or `ManualReview`; all transitions require the same `started_at`, so a stale worker cannot complete a reclaimed lease.

For each claimed campaign, `CampaignFlow.ReconcileUndeliveredCampaignRefund` does the financial decision in a transaction:

1. locks and rereads the campaign, requires `executed`, an eligible scheduled time (96-hour default delay), a nonzero finalized audience count, and obtains a transaction advisory lock by campaign;
2. uses `TransactionRepository.HasCompletedCampaignPartialRefund` as durable idempotency; requires `statistics.aggregatedTotalSent` and exits if all intended recipients were sent;
3. derives missing recipients and the frozen cost-per-message from finalization metadata; reads exactly one completed approval fee with `TransactionRepository.ByFilter` and rejects inconsistent amounts for manual review;
4. locks the wallet, reads latest balance, inserts a snapshot reducing `SpentOnCampaign` and increasing credit, inserts a completed refund transaction, and JSONB-merges durable refund evidence/marker with `CampaignRepository.MergeStatistics`.

The queue classifies no refund as complete/no-op, impossible/inconsistent accounting as manual review, and transient failures as retryable.

## Repository query map

| Repository / primitive | Operational role |
| --- | --- |
| `LockCampaignForUpdate`, `LockBundleForUpdate`, `LockWalletForUpdate` | Explicit row locks used inside transactions to serialize state, recipient allocation, and ledger changes respectively. |
| `CampaignRepository.ByFilter`, `ByID`, `Count`, `Update`, `MergeStatistics` | Ready/admin lists, locked state rechecks and transitions, metadata-safe statistics updates. |
| `CampaignRepository.AggregateClickCountsByCampaignIDs` | Distinct human-click reporting aggregate. |
| `TransactionRepository.ByFilter`, `Save`, `HasCompletedCampaignPartialRefund` | Unique freeze/fee evidence, append-only fee/refund records, and partial-refund idempotency. |
| `WalletRepository.ByCustomerID`, `GetCurrentBalance`; `BalanceSnapshotRepository.Save` | Finds wallet/latest ledger state and appends the next authoritative balance state. |
| `CampaignSelectedTagRepository.Validate`, `ListSelected` | Ensures the scheduler receives current valid Smart Targeting tag order. |
| Test/execution reservation repositories | Read exact frozen recipients, validate/materialize their transition to bundle selection, and release them on terminal pre-delivery paths. |
| `BundleAudienceSelectionRepository.ByCampaignID`, `InsertForCampaign` | Idempotent campaign audience allocation and normalized ordered-member persistence under the bundle lock. |
| `ProcessedCampaignRepository.ByCampaignID` and sent/status repositories | Deduplicate scheduler attempts, establish durable no-replay send intent, and persist provider delivery evidence. |
| `CampaignAudienceReportJobRepository` methods | Atomic customer job limit, leased work claim, lease-token compare-and-set finish/fail, and retention cleanup. |
| `CampaignRefundReconciliationRepository` methods | Bounded job discovery, leased claim, guarded complete/retry/manual-review state transitions. |

## Logical issues and correctness gaps

1. **Refund classification changes the funding source.** Rejection, cancellation, and partial-undelivered refund code contains explicit TODOs to reconstruct the original free-versus-credit split. Current paths credit the entire refund to `CreditBalance`, even if the frozen amount originated in free balance.
2. **A ready campaign can become permanently invisible after one hour.** Bot readiness filters approved campaigns to schedules no older than one hour. Without a separate remediation job, a scheduler outage or validation failure lasting longer leaves campaigns approved but never returned to a platform scheduler.
3. **Scheduler-to-UID-file writes are not atomic with database delivery state.** `PushCampaignAudienceUIDs` writes local files independently of processed/sent/statistics records. A crash can create a report gap or an orphan/duplicate UID record even though delivery data is committed.
4. **Post-commit notifications and audits are best effort.** Approval/rejection/cancellation state and money are correct if commit succeeds, but notification and audit errors are ignored; there is no transactional outbox or retry evidence.
5. **Status patches have no schema/version contract at this boundary.** `MergeStatistics` accepts arbitrary JSON object keys. It prevents lost updates but cannot prevent a reporter from overwriting a semantically incompatible value or sending malformed numeric representation that later blocks reconciliation.
6. **Legacy Smart Targeting fallback still selects at dispatch time.** It is explicitly limited to pre-migration campaigns and guarded against active reservation rows, but it cannot provide the exact frozen-audience guarantee of new reservations.
7. **A delivery-intent crash terminalizes rather than automatically recovering.** This is an intentional safety choice, but it creates an operational manual-review queue: a transient provider ambiguity after intent persistence can leave a campaign `interrupted` until an operator decides what happened.
8. **Scheduler checkpoint/audience preparation still has a documented transactional-consistency concern.** The SMS scheduler explicitly flags transaction management around processed-campaign creation and audience fetching as unfinished. If those persistence boundaries are changed without one coherent transaction/idempotency contract, a retry could observe a checkpoint without its intended audience (or vice versa).
9. **An SMS provider-call crash can strand an admitted concurrent window.** The SMS pipeline deliberately does not replay persisted send intent. Its own TODO notes that a crash can leave up to the admitted window without confirmed provider result (currently up to 500 Candoo or 1,000 PayamSMS rows at the maximum window). This avoids duplicate sends but requires reconciliation/manual handling of ambiguous recipients.
10. **The administrative detail DTO has an ambiguous missing base price.** `GetCampaign` initializes platform base price to `0` when frozen metadata is absent, while a code comment notes that it needs an error/sentinel representation. A caller can confuse unknown legacy price evidence with a real zero price.

## Bottlenecks and operational limits

1. **Wallet and bundle locks serialize throughput.** All financial actions for one wallet serialize on the wallet row; non-Excel approval and audience materialization serialize on the bundle row. This is correct but restricts parallel campaign work for the same customer/bundle.
2. **Large audience materialization is write-heavy.** Allocation, reservation materialization, and attribution rows are inserted in batches of 1,000, creating significant indexes/WAL work and lengthening the bundle critical section for large campaigns.
3. **Ready-list enrichment has N+1 behavior.** Frozen-price transaction lookup is per campaign; platform settings are read per distinct ID. Large ready queues create many round trips before any dispatch starts.
4. **Provider calls and status polling dominate wall-clock time.** Send batches are bounded and externally rate-limited; a running campaign remains unrecoverable/retry-sensitive around provider acknowledgement. Status results delay `executed` and refund eligibility.
5. **Click aggregation is expensive at scale.** Distinct UID counts with automated-traffic predicates scan/group click data; list/detail pages and report generation require suitable indexes and bounded periods.
6. **Audience report jobs are database, disk, and CPU intensive.** Staging potentially millions of UID rows, conflict-upserting batches, joining click/link data, and generating up to 5 GiB XLSX files compete with the main operational database and local disk. The default single parallel report worker is a deliberate containment, not a performance cure.
7. **Refund discovery/backfill and reconciliation compete with foreground writes.** The cursor bounds discovery, but every actual refund locks campaign and wallet, reads financial evidence, and appends ledger records. A backlog can create lock pressure for active customers.
8. **Local output and audience files require host capacity monitoring.** Retention cleanup reduces report output accumulation, but campaign UID files are intentionally long-lived and do not share database quotas; disk exhaustion remains an application-level operational risk.

