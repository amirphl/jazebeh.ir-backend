# Bundle lifecycle and enrichment

This document is the detailed review map for bundle CRUD, AI tag evaluation,
and action-file metrics. It identifies the repository calls made directly by
each flow and describes their database queries and mutations.

```text
Customer -> Bundle -> Campaigns -> persisted campaign statistics
                    -> evaluation runs -> events/attempts/batches/scores
                                      -> current score/status read models
                    -> action files -> staged UIDs -> delivery/attribution metrics
```

`BundleFlow` enriches reads from campaign and evaluation data; it does not
persist those aggregates. Tag scores feed Smart Targeting's ranking. Action
metrics use immutable campaign selection/attribution and delivery evidence.

## Transaction conventions

`repository.WithTransaction` carries a GORM transaction in the context, which
the repositories use when present. Generic `Save`/`SaveBatch` in
[repository/base.go](../repository/base.go) issue `INSERT` / batched `INSERT`
(1,000 rows per batch); absent an outer transaction, they start and commit one.
`getCustomer` reads the customer by primary key and rejects inactive accounts.
`createAuditLog` inserts the audit row supplied by a successful or failed flow.

## A. Bundle CRUD and read enrichment

Source: [bundles_flow.go](bundles_flow.go), backed by
[bundle_repository.go](../repository/bundle_repository.go).

### CreateBundle

After required-field, short-link-domain, and account-type category/job
validation, the flow calls these operations in one transaction:

| Order | Repository call | Query / mutation |
| --- | --- | --- |
| 1 | `CustomerRepository.ByID(customerID)` | Primary-key `SELECT` of the customer; the flow requires an existing active row. |
| 2 | `BundleRepository.Save(bundle)` | `INSERT INTO bundles` with validated fields, customer ID, empty metadata/statistics, and timestamps. |
| 3 | `AuditLogRepository.Save` | `INSERT INTO audit_logs` for the creation. If the transaction fails, a separate failure audit is attempted outside it. |

### UpdateBundle

After identical field validation, the flow reads the bundle and compares its
customer ID before changing it.

| Order | Repository call | Query / mutation |
| --- | --- | --- |
| 1 | `CustomerRepository.ByID` | Active-customer lookup. |
| 2 | `BundleRepository.ByID(id)` | `SELECT` one bundle by ID with its `Customer` preloaded; a missing row returns `nil`. |
| 3 | `BundleRepository.Update(bundle)` | GORM `Save`/`UPDATE` of the bundle row, explicitly refreshing `updated_at`. |
| 4 | `AuditLogRepository.Save` | Success audit insert in the same outer transaction; failure audit is outside the failed transaction. |

This flow does not create an evaluation run or invalidate/recompute historical
action metrics; reviewers should decide whether that is the intended product
policy when persona/objective changes.

### GetBundle and ListBundles

Both operations are read-only and enforce the customer/bundle relationship.

| Caller | Repository call | Query description |
| --- | --- | --- |
| Get/List | `CustomerRepository.ByID` | Reads the caller's customer and checks active state. |
| Get | `BundleRepository.ByID` | Reads one bundle plus `Customer`; flow rejects non-owner access. |
| List | `BundleRepository.Count(filter)` | `COUNT(*)` over `bundles`, always filtered by `customer_id`; optional title and target-customer-name use case-insensitive `ILIKE`. |
| List | `BundleRepository.ByFilter(filter, "updated_at DESC, id DESC", limit, offset)` | Reads a deterministic bundle page with the same filters and preloads customers. |
| Get/List | `CampaignRepository.ByCustomerIDAndBundleIDs` | Reads all campaigns owned by the customer with `bundle_id IN (...)`. The flow aggregates persisted campaign statistics in memory: total records, total sent, total campaigns, and test/execution counts. |
| Get/List | `BundleTagEvaluationReadRepository.ListByBundleIDs` | Reads current evaluation-status rows using `bundle_id IN (...)`; missing rows map to `not_evaluated`. |

Live click totals are deliberately not queried here. The disabled historical
short-link-click aggregate could be unbounded for a bundle and block listing.

## B. Bundle tag evaluation

Source: [bundle_tag_evaluation_flow.go](bundle_tag_evaluation_flow.go), backed
by [bundle_tag_evaluation_repository.go](../repository/bundle_tag_evaluation_repository.go).
The workflow snapshots persona/configuration and tag data; provider retries
resume from durable attempts instead of replacing history.

### RequestBundleTagEvaluation

The feature and scheduler must be enabled. The request verifies ownership,
enforces one active run per bundle and the per-customer UTC-day limit, then
atomically creates a run and initial event.

| Order | Repository call / SQL | Query description |
| --- | --- | --- |
| 1 | `CustomerRepository.ByID` | Active-customer primary-key lookup. |
| 2 | `BundleRepository.ByID` | Bundle read plus owner comparison. |
| 3 | `ReadRepository.ByBundleID` | Reads one current-status derived row by `bundle_id`; first an optimistic conflict check. |
| 4 | `lockBundleTagEvaluationCustomerRequest`, `lockBundleTagEvaluationRequest` | Transactional PostgreSQL locking helpers serialize daily-limit and same-bundle submission decisions. |
| 5 | `ByBundleID` again | Authoritative locked recheck; an `evaluating` row returns the existing run instead of creating another. |
| 6 | `RunRepository.CountByCustomerIDCreatedBetween` | `COUNT(*)` runs for the customer where `created_at >= UTC-day-start AND < next-day-start`; applies the daily limit. |
| 7 | `RunRepository.Save` | Inserts a run with bundle/customer IDs, target-persona snapshot, prompt/configuration snapshot, and batch size. |
| 8 | `EventRepository.Save` | Inserts the run's `created` event in the same transaction. |

### Status and score endpoints

| Entry | Repository call | Query description |
| --- | --- | --- |
| `GetBundleTagEvaluationStatus` | `CustomerRepository.ByID`, `BundleRepository.ByID` | Customer and bundle-owner reads. |
|  | `ReadRepository.ByBundleID` | Reads one current derived status; no row becomes `not_evaluated`. |
| `ListBundleTagScores` | `CustomerRepository.ByID`, `BundleRepository.ByID` | Same access reads. |
|  | `ReadRepository.CountCurrentScoresByBundleID` | Counts current score rows for the bundle, joining `tags` and retaining only `is_active = true`. |
|  | `ReadRepository.ListCurrentScoresByBundleID` | Reads active scores ordered `bundle_fit_score DESC, tag_id ASC`, with limit/offset. |

### ExecuteBundleTagEvaluationRun

The scheduler invokes this worker for pending derived statuses. A per-bundle
execution lock prevents duplicate workers; cancellation/timeout remains
resumable, while other failures append a `failed` event.

| Stage | Repository call | Query / mutation description |
| --- | --- | --- |
| Load | `RunRepository.ByID` | Reads the durable run by ID. |
| Idempotency | `ReadRepository.ByRunID` | Reads derived run status; `evaluated` exits without repeat work. |
| Start event | `EventRepository.LatestByRunID` | Reads latest event ordered `created_at DESC, id DESC`; inserts `started` only when absent/created. |
| Persona analysis | `PersonaAttemptRepository.LatestByRunID` | Reads latest attempt ordered by attempt number/time and reuses a valid stored response. |
|  | `EventRepository.ExistsByRunIDAndType` | `COUNT(*)` existence query prevents duplicate persona lifecycle events. |
|  | `PersonaAttemptRepository.Save` | Inserts every provider request/response/error, status, usage, timestamps, and extracted text. |
|  | `EventRepository.Save` | Inserts persona started/response/completed events. |
| Build plan | `BatchRepository.ListByRunID` | Reads immutable batches ordered by number/time; existing rows are reused. |
|  | `TagRepository.ListActiveAfterID(afterID, limit)` | Keyset-paginates active tags by ID. Name, title, persona, and audience count are copied into batch snapshots. |
|  | `BatchRepository.Save`, `EventRepository.Save` | In one transaction, inserts each batch and its `batch_created` event; a subsequent list reads the persisted plan. |
| Resume batch | `EventRepository.ExistsByBatchIDAndType` | Checks whether a `batch_completed` event already exists. |
|  | `ScoreRepository.CountByRunIDAndBatchID` | Counts durable scores for the batch; a complete count writes a completion event without an external call. |
|  | `BatchAttemptRepository.LatestByBatchID` | Reads last attempt; parses/reuses a successful raw response or determines next retry/non-retryable failure. |
| Validate catalog | `TagRepository.ListByIDs` | Re-reads all snapshot tag IDs and fails if any is now inactive/missing before the provider call. |
| Persist result | `BatchAttemptRepository.Save` | Inserts every provider attempt, including malformed responses and retryable errors. |
|  | `ScoreRepository.SaveBatch` | Transactionally bulk-inserts validated per-tag scores, linked to run/batch/attempt and copied tag snapshot data. |
| Finish | `ScoreRepository.CountByRunID` | Counts all stored run scores and checks expected count against configured allowed missing total. |
|  | `EventRepository.ExistsByRunIDAndType`, `Save` | Ensures exactly one terminal `completed` event; `failRun` instead inserts `failed`. |

`ReadRepository.ListPendingRuns(limit)` is the scheduler's queue query: it
reads derived statuses where `run_status IN ('created', 'evaluating')`, ordered
by evaluation creation time. This makes interrupted, nonterminal runs eligible
for retry.

## C. Bundle action files and action metrics

Source: [bundle_action_flow.go](bundle_action_flow.go), backed by
[bundle_action_repository.go](../repository/bundle_action_repository.go).
Upload stages an XLSX UID list. A leased worker parses it and derives metrics
from immutable audience membership/attribution plus delivered-message evidence.

### Customer-facing action-file operations

All except `CampaignMetric` call `BundleRepository.ByID` through `ownedBundle`
and compare `customer_id`. `CampaignMetric` calls `CampaignRepository.ByUUID`
through `getCampaign`, which verifies campaign ownership first.

| Entry | Repository call | Query / mutation description |
| --- | --- | --- |
| `Upload` | `BundleActionRepository.Create` | Inserts a pending `bundle_action_files` row with hash, uploader, action level, and temporary path. The flow writes XLSX bytes to the filesystem, then directly updates `storage_path` by ID; filesystem and insert are not atomic. |
| `Get` | `BundleActionRepository.ByID` | Reads one file by primary key; flow checks its bundle ID. |
| `List` | `BundleActionRepository.List` | Counts files for the bundle, then reads a page ordered `created_at DESC, id DESC`. |
| `Delete` | `ByID`, `RequestDelete` | Reads file/status, then conditionally updates only a `processed` matching row to `delete_pending`, recording actor/time. |
| `Summary` | `BundleActionRepository.Summary` | Reads one bundle summary; no row returns a zero-valued summary. |
| `TagMetrics` | `BundleActionRepository.TagMetrics` | Reads bundle tag metric rows joined to `tags` for display title, ordered by tag ID. |
| `CampaignMetric` | `CampaignRepository.ByUUID`, `BundleActionRepository.CampaignMetric` | Reads owned campaign then one metric by `(bundle_id, campaign_id)`; missing metrics are zero-valued. |

### Scheduler claim and worker execution

`BundleActionFileScheduler` claims files and calls `ExecuteBundleActionFile`.
The flow does not consume its `lease` argument; lease policy is in the
scheduler/repository.

| Stage | Repository call / SQL | Query / mutation description |
| --- | --- | --- |
| Claim | `ClaimPending(limit, stale, now)` | Transactional CTE chooses earliest pending/delete-pending (or stale processing) file per bundle, locks files with `FOR UPDATE SKIP LOCKED`, and marks them processing/started. One claimed file per bundle avoids concurrent metric refresh. |
| Load | `ByID` | Reads the claimed row. Delete-pending skips parsing; a row not processing is a no-op. |
| Parse error | `Fail` | Conditional `UPDATE ... WHERE id=? AND status='processing'` to failed, with bounded error fields. |
| Persist UIDs | `StoreUIDsAndResult` | Transactionally deletes existing file UIDs, bulk-inserts valid unique UIDs (chunks of 1,000), resolves each UID, and conditionally updates diagnostics only while status remains processing. |
| UID resolver | lateral/CTE update in `StoreUIDsAndResult` | Finds membership only in the same bundle; joins selection, campaign, audience, and immutable tag attribution. Four `EXISTS` branches require a current processed campaign and fully delivered message parts for SMS/Bale/Rubika/Splus. Classification: outside bundle, unassigned tag, missing delivery, or eligible. |
| Completion | `Complete` | Transaction locks the file and `SELECT id FROM bundles ... FOR UPDATE`, changes processing->processed or delete-pending->deleted, refreshes metrics, and updates `bundles.action_data_updated_at`. |

### Metric refresh

`Complete` first counts processed files and deletes prior campaign/tag metric
rows for the bundle. With no processed files it upserts a zero summary.
Otherwise it builds one transaction-local temp table from `bundleActionEligibleAudienceSQL`:

1. distinct UIDs from processed action files;
2. immutable bundle selection members, campaigns, audience profiles, and tag
   attributions for that same bundle;
3. only audiences with fully delivered messages on at least one platform; and
4. an action marker when the audience UID occurs in the processed action set.

It then inserts grouped campaign action/eligible-delivery counts, grouped tag
test/overall counts, and upserts the bundle summary. The query intentionally
does not consult mutable `audience_profiles.tags`: it preserves the tag
attribution in effect at send time.

## Review invariants

- Every customer-visible bundle operation proves customer/bundle ownership.
- Request conflict and daily-limit decisions are protected by locks.
- Evaluation configuration, tag batch snapshots, provider attempts, events,
  and scores are append-oriented and resumable.
- Current score reads exclude inactive tags; workers recheck tags before calls.
- Action metrics publish under a bundle lock and use immutable selection,
  attribution, and delivery records rather than live profile metadata.

## Logical bugs and issues identified

This register is limited to the flows documented above. The deployment is a
single application instance and a single PostgreSQL instance without replicas.
That removes cross-node races and replica-read staleness, but it does **not**
remove concurrent HTTP requests, scheduler goroutines, process restarts, or
the need to make database state and filesystem state atomic together.

| Severity | Issue | Evidence and effect | Recommended correction |
| --- | --- | --- | --- |
| High | Missing bundle can panic action-file endpoints | `BundleActionFlowImpl.ownedBundle` calls `bundleRepo.ByID` and immediately reads `b.CustomerID`. `BundleRepository.ByID` returns `(nil, nil)` for a missing row, so `Upload`, `Get`, `List`, `Delete`, `Summary`, and `TagMetrics` can dereference nil instead of returning `ErrBundleNotFound`. | Check `b == nil` before the owner comparison and return the normal not-found business error. Add endpoint-level tests for a nonexistent bundle. |
| High | Action-file worker leases are not fenced | `ClaimPending` treats an old `processing` row as reclaimable by replacing `started_at`. `ExecuteBundleActionFile` receives the claimed lease but never uses it. `StoreUIDsAndResult` and `Complete` only check `status='processing'`, not the claimed `started_at`/lease token. An over-deadline worker can therefore publish after the same row was reclaimed locally, deleting/reinserting UIDs or completing work owned by the newer worker. This remains possible on one instance when one goroutine exceeds its context deadline or a blocking operation ignores cancellation. | Add a durable lease token or require `started_at = claimedLease` in every worker mutation (`StoreUIDsAndResult`, `Fail`, and `Complete`). Return a lease-lost error when no row matches. |
| High | Upload persistence and file storage can diverge | `Upload` inserts a pending `bundle_action_files` row before creating the directory/writing the XLSX, and later updates `storage_path` separately. Any filesystem or final database-update failure leaves a pending row pointing to `pending` or a missing path; the scheduler will claim it and mark it parse-failed. A scheduler goroutine can also claim the row in the interval before `storage_path` is updated. | Stage bytes to a temporary path first; then transactionally create a row in a non-claimable state and promote it to `pending` only after the final path is durable. Compensate/clean up on failure. |
| Medium | Delete can report success after losing a race | `Delete` checks `processed`, then `RequestDelete` runs a conditional update on `id AND status='processed'` but ignores `RowsAffected`. If completion/deletion changes the status between read and update, the method returns nil despite not scheduling deletion. | Have `RequestDelete` return whether exactly one row was updated; surface a conflict/not-deletable response when it was not. |
| Medium | Bundle edits do not coordinate with evaluation input | `UpdateBundle` reads and saves without locking the bundle row. `RequestBundleTagEvaluation` reads the bundle before obtaining its `FOR UPDATE` lock and then snapshots that earlier `TargetAudiencePersona`. A concurrent update can therefore commit a new persona/objective while a newly queued run retains the old persona. More generally, a completed evaluation remains available after changing fields that define its relevance. | Lock/re-read the bundle inside the same request transaction before snapshotting. Define an explicit policy for relevant updates: invalidate current scores, mark them `update_required`, or automatically queue a new run. |
| Medium | Bundle read response is not one consistent snapshot | `GetBundle`/`ListBundles` issue separate bundle, campaign, and evaluation-status reads under normal read-committed semantics. A concurrent campaign/evaluation update can produce a response where the bundle row, aggregate statistics, and evaluation status refer to different instants. This is a local concurrency issue, not replication lag. | If an internally consistent response is required, perform the related reads in a read-only repeatable-read transaction or expose a documented as-of/version contract. Otherwise document the response as eventually/currently assembled. |
| Low | Action-file pagination is not validated in the business flow | `BundleActionFlow.List` computes `(page-1)*limit` without validating either value. Nonpositive pages/limits may create a negative offset or delegate behavior to GORM/database defaults; callers other than the HTTP handler can bypass handler validation. | Validate `page >= 1`, a bounded positive `limit`, and overflow-safe offset in the flow before calling the repository. |
| Low | `actionLevel` is stored without domain validation | `Upload` accepts the caller-provided `actionLevel` verbatim and the model stores it as unconstrained text. If it is a business classification rather than free text, invalid values become durable data and can fragment reports/filters later. | Define an enum/allowlist and validate it in the flow (or explicitly document it as free-form metadata). |

The action-file lease and storage issues have the highest operational priority:
they can corrupt or misrepresent derived action metrics even in the stated
single-instance topology. The evaluation-input issue has priority when tag
scores are used to make targeting decisions immediately after a bundle edit.

## Bottlenecks and scaling limits

These are performance/throughput constraints observed in the same flows. They
apply to the current single application and single database instance; no
replication assumption is required. Some serialization is deliberate for data
correctness, but it still defines capacity and should be monitored.

| Area | Bottleneck | Why it occurs | Practical impact / review focus |
| --- | --- | --- | --- |
| Bundle list/detail enrichment | Historical campaign fan-out | `GetBundle` and `ListBundles` call `ByCustomerIDAndBundleIDs`, which reads every campaign for every bundle in the response page, then unmarshals and aggregates each campaign's statistics in application memory. The query is not limited to a recent period or aggregated in SQL. | A bundle with long campaign history makes even a small bundle page expensive in database rows, network transfer, JSON decoding, and heap use. Consider a grouped aggregate/read model keyed by bundle, or a bounded reporting window. |
| Bundle list/detail enrichment | Multiple round trips and non-cached derived status | Listing performs count, bundle page, campaign fan-out, and evaluation-status fan-in queries. Detail also performs separate bundle, campaign, and evaluation reads. | Latency is the sum of several sequential database operations. A batch-oriented read model could combine or cache stable aggregates; retain the current ownership filter. |
| Evaluation planning | Full active-tag scan and all-batch memory retention | `ensureBatches` keyset-scans the entire active tag catalog, accumulates every tag snapshot and every batch in Go memory, then inserts every batch/event before work starts. | Evaluation startup grows with the whole catalog, not just the number of batches that can be processed now. Large catalogs cause allocation/GC pressure and a long initial delay. Stream/persist batches incrementally or bound the planning transaction. |
| Evaluation persistence | Per-batch database chatter | Each batch performs existence checks, score counts, latest-attempt lookup, active-tag revalidation, attempt/event inserts, and score inserts. | For many small batches, control-plane queries can rival provider latency and place sustained load on the single DB. Measure query count per batch; consolidate compatible checks or choose a batch size that balances provider payload limits against database overhead. |
| Evaluation execution | Global OpenAI request pacing | `bundleTagEvaluationPacedOpenAIClient` shares one request pacer for the flow. `MaxParallelBatches` can run parsing/persistence concurrently, but provider calls are deliberately throttled by the configured requests-per-minute rate. | This is an intentional external-service safety limit. Increasing scheduler workers alone will not improve provider-call throughput; tune RPM, batch size, and provider capacity together. |
| Evaluation per bundle | Long-lived advisory lock | `ExecuteBundleTagEvaluationRun` keeps a PostgreSQL advisory lock for the entire persona analysis, all provider retries, and every batch. | Work for the same bundle is serialized for the potentially long network-bound run. This protects correctness, but repeated scheduler polls can encounter a busy lock and produce retry/log churn. Monitor run duration and lock-busy frequency. |
| Action-file ingestion | Whole-file and whole-UID-set memory | `Upload` uses `io.ReadAll` for up to 50 MiB; XLSX parsing retains a `seen` map and the complete unique UID slice for up to 500,000 rows before database persistence. | A few concurrent maximum-sized uploads/workers can create substantial heap and disk/ZIP I/O pressure in one process. Stream upload to temporary storage and use bounded/batched UID staging if peak concurrency matters. |
| Action-file UID resolution | One large multi-join update | `StoreUIDsAndResult` inserts all valid UIDs then resolves them in one CTE/lateral update. For each UID, the query reaches bundle selections, campaigns, profiles, attributions, and four platform-specific `EXISTS` delivery branches. | This is the dominant database cost for large files. It can hold a transaction for a long time and compete with campaign execution queries. Inspect `EXPLAIN (ANALYZE, BUFFERS)` on production-shaped data and ensure indexes support bundle membership, UID/profile lookup, attribution, processed-campaign, and sent/status joins. |
| Action metrics | Full historical recomputation after every change | `Complete` deletes all bundle campaign/tag metrics and rebuilds them from `bundleActionEligibleAudienceSQL` whenever an action file is processed or deleted. The temporary table avoids rerunning the four delivery probes for each metric insert, but it still scans all eligible delivered audiences across the bundle history. | Runtime rises with all historical selections and delivery records, not only the changed file. Repeated uploads/deletions for a large bundle repeatedly pay that cost. Consider incremental UID deltas or a materialized delivery/attribution fact table. |
| Action metrics | Bundle-row lock serializes expensive refresh | `Complete` takes `FOR UPDATE` on the bundle and retains it while rebuilding metrics. `ClaimPending` intentionally selects only one file per bundle for the same reason. | Correct but throughput is capped at one full parse/resolve/refresh pipeline per bundle. Long refreshes delay later files and any operation that needs the same bundle-row lock. Track queue age, lock wait time, and refresh duration. |
| Action scheduler | Poll loop waits for every claimed worker | `BundleActionFileScheduler.runOnce` starts up to `maxRuns` goroutines and `Wait`s for all of them before the next poll/claim. | Slow files prevent the scheduler from filling newly available capacity until the whole current group finishes. This is bounded and simple, but can underuse capacity when task durations vary widely. A bounded worker pool with continuous claiming would improve utilization. |

Suggested operational measurements: bundle-list campaign rows per request;
evaluation planning time, batches/run, provider wait time, and DB queries/batch;
action-file size/unique UID count, UID-resolution time, metric-refresh time,
bundle-lock wait time, and pending-file age. These measurements establish which
of the listed constraints is material before changing correctness-sensitive
locking or query behavior.
