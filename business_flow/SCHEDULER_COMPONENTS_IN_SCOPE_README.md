# Scheduler components in scope

## Purpose and boundary

This guide is the starting point for manually reviewing the background workers that advance bundle enrichment, Smart Targeting, campaign delivery, reporting, refunds, and tag-test measurement. It covers the scheduler components named in `BUSINESS_FLOW_REVIEW_GUIDE.md`; it does not attempt to document unrelated cron-like processes.

The deployment assumption is one application process and one PostgreSQL database, with no replicas. Therefore this document treats database claims primarily as crash recovery and protection against overlapping goroutines, not as cross-node coordination. A single point of failure is intentionally out of scope.

## Relationship map

```text
Bundle tags ──> evaluation run ──> tag results
Bundle action file ──> eligible audience refresh ──> bundle audience
                                              |
Campaign approval ──> Smart Targeting calculations ──> persisted selection/reservation
                                              |                         |
                                              +-------------------------+
                                                                        v
                                     SMS / Bale / Rubika / Splus delivery workers
                                                                        |
                  delivery rows ──> status jobs ──> campaign statistics ──> refunds
                                                                        |
                                                tag-test performance reports

Campaign audience report jobs can read/export the persisted campaign audience at any time.
```

## Common scheduler contract

Most schedulers start one polling goroutine, run immediately, then poll at a configured interval. Work executes outside the polling goroutine under a deadline. The `inFlight` maps and `CampaignExecutionLimiter` are process-local concurrency limits; they disappear on restart. Durable jobs use a `started_at` lease as their ownership token: their query claims a pending or stale row, and later completion/failure updates must include that same value.

The following distinction is important during review:

| Pattern | Components | Meaning |
| --- | --- | --- |
| Process-only de-duplication | bundle tag evaluation; campaign delivery dispatch groups | avoids two goroutines in this process doing the same work; correctness additionally relies on flow/state checks. |
| Database lease | action files, Smart Targeting calculations, refund jobs, tag-test reports, audience reports | makes interrupted work eligible again and lets writes reject an obsolete worker. |
| Immutable materialization | Smart Targeting candidate selection | turns a calculated/reserved audience into the exact `bundle_audience_selection` used by delivery. |

## 1. Bundle enrichment workers

### Bundle tag evaluation scheduler

`BundleTagEvaluationScheduler` reads up to twice its free capacity with `BundleTagEvaluationReadRepository.ListPendingRuns(limit)`. The query reads `bundle_tag_evaluation_run_status` for rows whose `run_status` is `created` or `evaluating`, ordered by `evaluation_created_at`; it returns run IDs rather than the full tagging work.

For each run, the scheduler enters its ID into `inFlight`, up to `maxParallelRuns`, and calls `ExecuteBundleTagEvaluationRun` with a 60-minute context. The flow itself acquires the evaluation lock, loads the run/tag inputs, calls the model in batches, and persists results/status. Thus the scheduler intentionally has no `ClaimPending` update: the flow's durable lock and state transitions are the business ownership boundary. A failed or interrupted run remains discoverable by the next poll.

### Bundle action-file scheduler

`BundleActionFileScheduler` is the durable-file worker. On each poll it calls `BundleActionRepository.ClaimPending(maxRuns, staleBefore, now)`. Its claim SQL uses a CTE to select the oldest eligible file for each bundle: a `pending`/`delete_pending` file, or a `processing` file whose `started_at` is older than the lease threshold. It locks candidates with `FOR UPDATE SKIP LOCKED`, then changes them to `processing` and sets `started_at = now` in the same statement. This prevents concurrent local workers from claiming the same file.

Each claimed file is given a 30-minute deadline by default. The constructor enforces that the 35-minute default lease exceeds that deadline. `ExecuteBundleActionFile(fileID, leaseStartedAt)` parses the uploaded action data, updates bundle/tag membership and metrics, then calls the repository completion methods. The important intent between claim and completion is that an action file is the audit record for a reproducible bundle-audience refresh; a delivery worker must consume the resulting persisted selection, not re-run the action-file interpretation.

Relevant repository operations include:

| Function/query | Objective |
| --- | --- |
| `ClaimPending` | Atomically find reclaimable work and mark it processing, using row locks and a lease timestamp. |
| file/member reads used by `ExecuteBundleActionFile` | Load file rows and map their UIDs/tags to the affected audience. |
| action-file result/update operations | Persist normalized UIDs, counts, result/error data, and terminal file status. |
| `Complete` | Mark the file finished after the refresh; see the issue below about its missing lease predicate. |

## 2. Smart Targeting calculation workers

Three independent durable queues keep expensive candidate scans out of campaign approval requests. All poll every five seconds by default, calculate remaining local slots, and call their queue's `ClaimPending(limit, staleBefore, now)`. Each claim query selects `pending` records or stale `calculating` records with `FOR UPDATE SKIP LOCKED`, sets `status = calculating`, increments attempts where appropriate, and assigns `started_at = now`.

| Worker | Queue and business output | Completion intent |
| --- | --- | --- |
| Capacity | `campaign_targeting_capacity_calculations`; raw, eligible, deduction, usable counts and fingerprint | tells approval whether an exact phase/tag configuration has enough audience. |
| Test sampling | `campaign_targeting_test_sampling_calculations`; per-tag sample result, satisfied tags, effective count, cost, fingerprint | creates the durable test audience choice before a test campaign is sent. |
| Execution calculation | `campaign_targeting_execution_calculations`; ordered audience/tag members and fingerprint | calculates the non-reserving execution audience before final reservation/materialization. |

The executors receive both the calculation ID and `leaseStartedAt`. Their `Complete`/`Finish` queries predicate on `id`, `status = calculating`, and `started_at`, so a worker that lost its lease cannot overwrite a newer worker. The execution scheduler additionally calls `Finish(... failed ...)` in a background 10-second context when its goroutine panics. Capacity and sampling rely on their executor's own recovery/failure path.

The calculations share the eligibility model: target tags, platform contactability, score class/bounds, color constraints, exclusions, and campaign/bundle context determine candidates. They calculate first; they do not make a sendable audience selection yet. That separation prevents an HTTP request from reserving capacity half way through an expensive scan.

## 3. Exact audience materialization before delivery

The channel schedulers use `selectAndReserveExactSmartTargetingCandidates` when a campaign is bundle/Smart-Targeting based. This is the critical bridge from calculation to delivery.

It begins a transaction and executes `LockBundleForUpdate(bundleID)`, an `UPDATE`-style row lock on the bundle. That serializes competing campaign materializations for the same bundle. It then performs these repository reads/writes in order:

1. `BundleAudienceSelectionRepository.ByCampaignID(campaignID)` queries the persisted JSON audience IDs. If it exists, it is reused. Execution campaigns also call `ValidateMaterializedForCampaign` so the stored selection must match the active execution reservation.
2. For a test campaign, `CampaignTargetingTestSampleSelectionRepository.ActiveReservedForCampaign` loads the active selection and ordered members. The function validates exact member count/order/tag attribution, calls `InsertForCampaign` to write `bundle_audience_selection`, loads phones/UIDs through `AudienceProfileRepository`, batch-inserts `campaign_audience_tag_attributions`, and calls `Materialize` to mark the test selection consumed by this campaign.
3. For a current execution campaign, `CampaignTargetingExecutionReservationRepository.ActiveReservedForCampaign` loads the exact ordered reservation. It writes the same bundle selection and attributions, then `Materialize(campaignID, expected)` marks those reservations materialized.
4. Only explicitly legacy-compatible execution campaigns may use the fallback: `CurrentForPhase` reads a non-stale capacity calculation matching campaign, bundle, platform, phase, tags, filters, calculation version, and fingerprint. Candidate queries (`CalculateScoreBounds`, `SelectForTag`, or `SelectCandidates`) select a fresh exact audience; the transaction persists selection and attributions together.

`InsertForCampaign` stores the ordered audience ID array, customer/bundle/campaign IDs, and correlation ID. `loadReservedBundleAudience` then queries profiles for phone, database ID, and UID. The code rejects short or malformed results before delivery: the selected count must equal the requested count, and the selection/attribution counts must agree. This is why delivery can safely resume without changing whom a campaign targets.

## 4. Channel delivery schedulers

`SMSCampaignScheduler`, `BaleCampaignScheduler`, `RubikaCampaignScheduler`, and `SplusCampaignScheduler` follow one campaign lifecycle with channel-specific sending/status APIs:

1. Login to the bot service and call `ListReadyCampaigns(platform)`. This remote query returns approved, due campaigns; each scheduler independently rejects a wrong platform, future schedule, invalid dates, or an already processed campaign.
2. `ProcessedCampaignRepository.ByCampaignID` queries the local processed-campaign record. Its existence is the local idempotency gate: campaigns already captured for sending are skipped.
3. Pending work is grouped by `customerID + bundleID` (or campaign ID when no bundle). Groups run concurrently, but campaigns inside one bundle group run serially. The shared `CampaignExecutionLimiter` bounds the expensive bundle-audience fetch/materialization across all channel schedulers.
4. The worker moves the remote campaign to `running`, and `TouchRunningCampaign` updates the local running heartbeat. It obtains the audience either from an Excel UID file (then queries profiles by UID) or from the persisted bundle selection described above. It preserves unmatched UIDs as explicit send outcomes rather than silently treating them as deliverable recipients.
5. In one database transaction, `ProcessedCampaignRepository.Save` inserts the campaign snapshot/metadata; `AppendAudienceData` appends audience IDs and codes in batches; `UpdateMeta` records counts and checkpoint information. This local record is written before contacting the external provider so later polls do not create a second campaign record.
6. Sender-specific loops persist planned `sent_sms`/Bale/Rubika/Splus rows and tracking identifiers, contact the provider, persist provider responses/outcomes, queue due status jobs when delivery confirmation is required, update aggregate statistics, then move the remote campaign to `executed`.

The SMS worker additionally resolves the configured sender/provider, allocates tracking (and, for Candoo, provider customer) IDs, writes each planned batch transactionally, sends ordered batches, and records immediate outcomes plus status-check jobs. The social-channel workers apply the same durable-audience and processed-campaign principles with their own recipient/message and status tables.

The status-job workers query due provider tracking jobs (for example `ListDueInterleavedByCampaign`), call the provider's status endpoint, persist the received status/result metadata, reschedule retryable jobs, and push refreshed statistics to the bot service. These are deliberately separate from the send loop so long provider-status delays do not block campaign creation.

## 5. Campaign audience-report scheduler

`CampaignAudienceReportScheduler` calls `Cleanup` on every poll, then starts up to `maxParallelRuns` calls to `ExecuteNext(timeout, lease)`. `Cleanup` removes expired report artifacts/jobs according to the flow's retention rules. `ExecuteNext` owns the queue query: it claims the next pending or expired-lease report job, loads the already-materialized campaign audience, generates/stores the export, and writes completion or retry state using the job lease.

The report is intentionally downstream of audience materialization. It reports the exact IDs selected for the campaign, rather than evaluating today's bundle membership, so an operator's download remains consistent with what was sent.

## 6. Refund-reconciliation scheduler

The refund scheduler handles money only after delivery data has settled. `DiscoverPending(now, limit)` is an upsert/discovery query over eligible campaigns/delivery records: it creates or reactivates a reconciliation job for campaigns that can have undelivered messages refunded. `ClaimPending(slots, staleBefore, eligibleBefore, now, leaseExpiresAt)` atomically leases due jobs, including stale leases, with the eligibility delay preventing premature reconciliation.

For each lease, `ReconcileUndeliveredCampaignRefund(campaignID, eligibilityDelay)` calculates/refunds eligible undelivered messages. `Complete` terminally closes a successful or no-op job and predicates on `campaign_id + started_at`. Ordinary errors call `Retry` with exponential delay; after the configured maximum attempts, or for `ErrCampaignRefundReconciliationManualReview`, `ManualReview` retains the job for human action. All three update queries use the lease token, so a stale worker cannot close or retry a newly reclaimed job.

## 7. Tag-test performance scheduler

`TagTestPerformanceScheduler` derives tag test performance from completed campaign outcomes. `DiscoverPending(now)` identifies test campaigns needing a report and inserts/refreshes report work. `ClaimPending(batchSize, staleBefore, now)` atomically leases reports. Unlike the other queue schedulers it recomputes its claimed batch serially, each under `tagTestPerformanceRecomputeTimeout`.

`RecomputeCampaign(campaignID, leaseStartedAt, now)` reads the campaign's materialized test audience and observed send/delivery outcomes, recomputes the report and the tag-level performance values, then completes only if its lease still matches. Invalid campaign state becomes terminal; transient calculation failures use `Fail` with a retry timestamp and eventually terminal failure. A lost lease is treated as non-fatal because another worker owns the report.

## Logical bugs and issues to review

1. **High — bundle action-file completion is not lease-fenced.** `ClaimPending` gives `ExecuteBundleActionFile` a `started_at` value, but `BundleActionRepository.Complete` updates by file ID without that lease predicate. A job reclaimed after a timeout can therefore be completed by the obsolete worker, overwriting the newer worker's state. This remains an issue even on one application instance: a deadline, goroutine overlap, or delayed database operation is sufficient.
2. **High — campaign delivery has an external side effect between durable checkpoints.** A provider can accept a batch after its request succeeds remotely but before the local response/outcome update commits. Retry/recovery then has insufficient proof to distinguish “not sent” from “sent.” Local `processed_campaign` records reduce duplicate campaign setup, but provider requests need an idempotency key or reconciliation by tracking ID for end-to-end exactly-once behavior.
3. **Medium — the legacy execution fallback deliberately selects a fresh audience from a capacity calculation.** Capacity proves only a count/fingerprint, not identity. Between calculation and materialization, audience eligibility can change; the code can fail exact-count checks or select people different from the earlier scan. It is limited to explicitly legacy-compatible campaigns, but is still semantically weaker than reservations.
4. **Medium — bundle tag evaluation has no scheduler-level durable claim.** Local `inFlight` avoids duplicate execution only while this process lives. The flow lock/state transition must remain correct for every failure and cancellation path; otherwise `created`/`evaluating` runs will be repeatedly retried. This is a review risk rather than proof of a defect, because durable ownership is delegated to the flow.
5. **Medium — test-sampling policy is deterministic database ordering rather than random sampling.** The calculation/materialization chain preserves exactness, but “sample” may be biased by the query order. See `SMART_TARGETING_CONFIGURATION_AND_CALCULATIONS_README.md` for the calculation-level evidence.
6. **Medium — remote state is advanced to `running` before all local preparation has completed.** Validation or local persistence failures after that transition leave the bot-service campaign running until a recovery/operations path intervenes. The scheduler logs/notifies, but this guide found no compensating remote failure transition in the normal error path.
7. **Low — scheduler panic handling is inconsistent.** The execution calculation scheduler explicitly lease-finishes a panic as failed; capacity and test sampling schedulers merely log a scheduler-level panic, relying on executor recovery. A future executor change that panics outside its recovery could leave a job calculating until lease expiry.

## Bottlenecks and operational pressure points

1. **Bundle action-file polling waits for every claimed worker.** `runOnce` calls `wg.Wait()` before the next tick can claim work. One slow file blocks replenishing unused capacity and increases queue latency.
2. **Smart Targeting scans and the bundle row lock serialize work.** Candidate filtering can scan large audience/tag/contact sets, while `LockBundleForUpdate` holds a transaction through profile loading, selection writes, and attribution batches. High-volume campaigns for one bundle queue behind it.
3. **Attribution and audience persistence scale with audience size.** `AppendAudienceData`, selection JSON arrays, profile loads, sent-row inserts, and `CreateInBatches(..., 1000)` can produce large writes, WAL pressure, and long transactions on the single database.
4. **Provider throughput dominates delivery.** Batches are deliberately ordered, status polling is separate, and the cross-channel execution limiter caps expensive work. These protect dependencies but turn provider latency/rate limits into campaign queue delay.
5. **Status polling and statistics updates add sustained database/API load.** Large sends create one or more follow-up status jobs per tracking ID; retries and bot-statistics pushes can compete with new sends.
6. **Refund and tag-test processing are eventually consistent by design.** The eligibility delay, retry backoff, and serial tag-test recomputation protect correctness but make final refund/performance data lag campaign execution, especially after a large delivery burst.
7. **Fixed polling intervals trade load for latency.** Five-second Smart Targeting/action polling and repeated discovery queries are inexpensive at low volume but become constant database work as tables grow; missing indexes on status, due time, `started_at`, and campaign keys would amplify this.

