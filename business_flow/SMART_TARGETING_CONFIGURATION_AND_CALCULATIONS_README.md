# Smart Targeting configuration and calculations

This guide explains the Smart Targeting control plane: tag configuration,
capacity calculations, Test sampling, Execution calculations, reservations,
and scheduler handoffs.

```text
Bundle and tag source
  -> ordered campaign tag selection
  -> exact capacity / Test sample / Execution candidate calculation
  -> finalization reservation
  -> scheduler materializes immutable send audience and tag attribution
```

## Core concepts and relationship

An ordered **tag selection** is editable campaign intent. A durable
**calculation** is an asynchronous result for one input snapshot. A
**reservation** is a concrete claim made at finalization. A materialized bundle
audience selection is the immutable list used to send. These must remain
separate: an editable campaign can change intent, whereas a finalized campaign
must never silently exchange recipients.

| Family | Question answered | Durable output |
| --- | --- | --- |
| Exact capacity | How many currently eligible distinct people exist? | Raw/eligible/usable counts, approved-work deduction, allocation fingerprint. |
| Test sampling | Which fixed-size audience sample is available for each tag? | Satisfied tags, concrete audience members, cost, Test selection. |
| Execution calculation | Which exact audience matches budget-derived execution count? | Ordered audience candidates with assigned tags. |

## Shared audience eligibility queries

[smart_targeting_audience_repository.go](../repository/smart_targeting_audience_repository.go)
implements the population shared by all calculations. Queries start at
`audience_profiles` and require selected tag overlap, nonblank phone number,
and allowed colors where sender/provider configuration requires them. They
exclude audiences already materialized in this bundle, held by active
Test/Execution reservations, present in committed Execution calculations, or
explicitly excluded from the bundle. A campaign's own reservation can be
exempted when refreshing that campaign.

Score classes use `normalized_score` percentiles over the eligible selected-tag
union: A is above p66, B is p33–p66, C is at/below p33. Profiles without a
score are `unscored`; restricted A/B/C queries exclude them. Selecting all
A/B/C skips percentile classification.

| Repository call | Query purpose |
| --- | --- |
| `CalculateCapacity` | CTE counts tagged raw population and candidate population; restricted classes calculate p33/p66 then count matching classes. |
| `CalculateScoreBounds` | Computes union-wide p33/p66 once for a Test sampling run. |
| `SelectForTag` / `SelectIDsForTag` | Finds candidates for one tag while honoring every shared exclusion and the in-run exclusion list. |
| `SelectCandidates` | Returns exact Execution candidates ordered `normalized_score DESC NULLS LAST, id ASC`. |

## 1. Tag discovery and selection

Source: [smart_targeting_flow.go](smart_targeting_flow.go), backed by
[campaign_selected_tag_repository.go](../repository/campaign_selected_tag_repository.go).

`ListTags` proves ownership of an existing campaign; `ListBundleTags` proves
ownership of a bundle before campaign creation. Both validate page/search/sort
input before querying.

| Repository call | Query and business meaning |
| --- | --- |
| `CampaignRepository.ByUUID` / `BundleRepository.ByID` | Loads the owner-scoped entity and bundle ID; no tag list is disclosed before ownership succeeds. |
| `BundleTagEvaluationReadRepository.CountCurrentScoresByBundleID` | Counts active current evaluation scores. Any score makes evaluated snapshots authoritative; no score falls back to the live active tag catalog. |
| `CampaignSelectedTagRepository.ListAvailable` | Performs a total count and paged coherent-source query. It joins Test/overall CTR, action-rate metrics, campaign Test performance, selection state, and attribution-based prior use. |
| `ListSelected` | Reads every persisted selection in `selection_order`, independent of display pages. |
| `Summary` | Counts selected tags and sums capacity snapshots; this is configured capacity, not exact availability. |

Default sorting communicates priority: evaluated non-Execution campaigns use
persona fit, Execution uses Test CTR then persona fit, and unevaluated bundles
use stable tag order. Persona-fit sort is refused when no evaluation exists.

Selection replacement is complete and atomic—not a patch. Empty, duplicate,
zero, and over-10,000 ID lists are rejected. Caller order is persisted because
it is later used to attribute multi-tag audiences.

| Repository call | Query and business meaning |
| --- | --- |
| Campaign `SELECT ... FOR UPDATE` | Serializes selection replacement with capacity-input creation, preventing mixed old/new selection snapshots. |
| `CampaignSelectedTagRepository.Replace` | Locks/rechecks campaign editability and bundle, validates IDs against coherent source, clears Test preview, deletes old rows, and inserts ordered snapshots. |
| `Validate` | Compares selection count with currently available selected-tag rows; detects tags no longer valid for the source. |
| `ListAvailableTagIDs` | AutoSelect reads IDs from the complete filtered/sorted source—not a visible UI page—then uses the same replacement transaction. |

Selection snapshots preserve title, capacity, persona fit, CTR, and ATR at
decision time. Changing selection clears Test preview state because a prior
sample represents different campaign intent.

## 2. Exact capacity generations

Source: [smart_targeting_capacity_flow.go](smart_targeting_capacity_flow.go),
backed by [campaign_targeting_capacity_repository.go](../repository/campaign_targeting_capacity_repository.go).

`Start` accepts an owned Smart Targeting campaign in an allowed state. It uses
requested score classes or campaign `AudienceGrades`; an explicit changed
request persists its classes back to the campaign so cost, approval, polling,
and workers all use the same configuration.

| Repository call | Query and business meaning |
| --- | --- |
| Campaign `FOR UPDATE` | Produces coherent campaign/selection input after rechecking status, platform, bundle, and Smart Targeting mode. |
| `ListSelected`, `Validate` | Reads selected tags, requires at least one, and confirms they remain available. Input hashing treats tags as a set for capacity reuse. |
| `LineNumberRepository.ByValue` | Reads sender configuration to derive allowed audience colors for applicable platforms. |
| `ActiveByCampaignID` | Finds active calculating work. An unexpired same-input request returns idempotently; changed input supersedes it. |
| `LatestCalculatedByInput` | Finds a prior completed generation. It is reused only if input, expiry, and allocation fingerprint still match. |
| `Save` | Inserts `calculating` work with tags/classes/platform/colors/phase, expiry, hashes, and empty fingerprint. |
| `LatestByInput`, `LatestByCampaignID`, `ByID` | Polling returns a current matching result or latest stale result, telling a client when recalculation is required. |

The input hash binds campaign, tags, classes, platform, allowed colors, and
phase. The allocation fingerprint represents shared bundle availability:
materialized/approved campaign consumption and active Test reservations. This
prevents a result from being called current merely because filters match.

### Capacity worker

The scheduler claims durable rows using `FOR UPDATE SKIP LOCKED`, sets
`started_at`, bounds parallelism, and supplies a job timeout. Completion/failure
requires the same `started_at`, fencing an old local goroutine after a stale
lease is reclaimed.

| Repository call | Query and business meaning |
| --- | --- |
| `ClaimPending` | CTE finds calculating rows with unset/stale lease in creation order, locks them, sets `started_at`, and returns claimed rows. |
| `ByID` | Worker verifies current status and its lease before work. |
| `LockBundleForShare` | Stabilizes prior-use/reservation state while counting; approval/materialization takes the stronger update lock. |
| `CalculateCapacity` | Runs the shared CTE, returning raw and eligible unique counts. |
| Allocation-state repository queries | Count approved shared-bundle demand and derive a deterministic fingerprint. |
| `Complete` | Lease-guarded update records raw, eligible, approved deduction, usable `max(eligible-deduction,0)`, fingerprint, and calculated status. |
| `Fail` | Lease-guarded failed transition with a stable polling message. |

Capacity is not an audience reservation. It is an expiring result that must be
fresh again at campaign finalization.

`CurrentSmartTargetingCapacity` is the shared gate used by cost/finalization
logic. It rebuilds the effective input from the current campaign, calls
`LatestCalculatedByInput`, and accepts only a calculated, unexpired,
fingerprint-current row. `EnsureCurrentSmartTargetingCapacity` starts a new
generation when that gate fails, returning a typed pending response rather than
pretending an estimated value is exact.

## 3. Test sampling and Test reservations

Source: [smart_targeting_sampling_flow.go](smart_targeting_sampling_flow.go),
backed by `campaign_targeting_test_sampling_repository.go` and
`campaign_targeting_test_sample_selection_repository.go`.

The flow requires an editable Test-phase Smart Targeting campaign with positive
sample size per tag. It hashes ordered tags, score classes, allowed colors, and
sample size. It validates price before queueing so an invalid sender setup does
not become an inevitably failing worker job.

| Repository call | Query and business meaning |
| --- | --- |
| Campaign `FOR UPDATE`, `ListSelected`, `Validate` | Captures one coherent, valid selection/input snapshot. |
| `ActiveByCampaignID`, `LatestCalculatedByInput` | Reuses unchanged active or current completed preview; otherwise supersedes obsolete work. |
| Campaign update | Clears old preview fields, increments generation, and removes active preview selection pointer before changed work is saved. |
| `CampaignTargetingTestSamplingRepository.Save` | Inserts durable calculating work with ordered tags, hash, sample size, classes, and generation. |
| `ClaimPending` | CTE/lease claim used by the Test worker scheduler. |
| `CalculateScoreBounds` | Obtains p33/p66 once for the selected-tag union. |
| `SelectForTag` | Reads a fixed candidate set per selected tag. In-run exclusion prevents one person entering multiple tag samples; a tag is satisfied only if it produces the full count. |
| `LockBundleForShare` + allocation fingerprint | Rechecks shared availability before preview publication. |
| `CampaignTargetingTestSampleSelectionRepository.Save` | Persists exact ordered candidate members as immutable preview evidence. |
| Campaign update + `Complete` | Publishes satisfied tags, preview hash/time, effective count and active selection ID, then lease-guardedly writes results/cost/fingerprint/calculated status. |
| `Fail` | Lease-guarded worker failure transition. |

At finalization, `ReserveForCampaign` reloads the preview, locks audience rows
in stable ID order, rechecks hard availability, and inserts active Test
reservations. It never substitutes a conflicting person; that requires a new
preview. The send scheduler calls `ActiveReservedForCampaign`, creates
permanent bundle selection/tag-attribution rows, then `Materialize` changes
every active reservation to materialized. Terminal flows call
`ReleaseForCampaign`, locking campaign and affected bundles before releasing
only active reservations.

## 4. Execution calculations and reservations

Source: [smart_targeting_execution_calculation_flow.go](smart_targeting_execution_calculation_flow.go), backed by `campaign_targeting_execution_calculation_repository.go` and `campaign_targeting_execution_reservation_repository.go`.

Execution first calculates campaign cost; budget and price determine requested
audience count. The asynchronous calculation selects exact candidates but does
not yet reserve recipients or freeze funds.

| Repository call | Query and business meaning |
| --- | --- |
| Campaign `FOR UPDATE` | Rejects a campaign changed while execution input is being derived. |
| `ListSelected`, `Validate` | Reads ordered tags and validates them. Request JSON records tags/classes/colors/platform; hash and JSON are later rechecked. |
| `ActiveByCampaignID`, `ReadyByInput` | Reuses same-input active/ready work only if structural validation and allocation fingerprint remain current; otherwise supersedes/recalculates. |
| `Save`, `ClaimPending` | Persists pending work then claims it with CTE `FOR UPDATE SKIP LOCKED` and a `started_at` lease. |
| `SelectCandidates` | Returns deterministic score-descending candidates from shared eligibility population. |
| `Complete` | In one transaction, lease-guardedly changes row to ready and inserts ordered members; rejects invalid order, zero IDs, duplicate audiences, wrong count, or invalid fingerprint. |
| `Finish` | Lease-guarded failure/stale transition when campaign input, fingerprint, or capacity changed. |
| `ReadyForUpdate`, `Members`, `MarkCommitted` | Finalization locks ready generation/members, validates freshness, then marks it committed only with its reservation. |
| `ReserveForCampaign` | Inserts reservation header and ordered active rows, validating snapshot, uniqueness, score data, and current state. |
| `ActiveReservedForCampaign`, `Materialize`, `ReleaseForCampaign`, `InvalidateForCampaign` | Load exact active reservation to send; materialize after bundle selection; release terminal work; invalidate editable obsolete state. |

At send time, a reservation-aware campaign is fail-closed: it can only
materialize its persisted reservation. The scheduler inserts immutable bundle
membership and tag attribution, then marks reservation rows materialized. Only
an explicit legacy compatibility version can use the old fresh-selection path.

## Scheduler relation

Capacity, Test-sampling, and Execution-calculation schedulers are independent,
bounded worker pools. They poll, claim durable rows, maintain local in-flight
IDs, run timeout-bound work, and remove the local marker on return. Process
restart is recoverable because stale `started_at` leases may be claimed again.
Lease-guarded database writes are still essential on one app and one database:
a timed-out or blocked goroutine can outlive its lease.

Platform send schedulers subsequently call
`selectAndReserveExactSmartTargetingCandidates` in
[smart_targeting_capacity_candidates.go](../app/scheduler/smart_targeting_capacity_candidates.go).
For finalized Test/Execution campaigns it materializes the persisted
sample/reservation; it normally does not select replacement audiences.

## Logical bugs and issues identified

This list assumes one application instance and one PostgreSQL instance, with
no replicas. It excludes replica-lag and multi-node problems.

| Severity | Issue | Evidence and consequence | Suggested correction |
| --- | --- | --- | --- |
| High | Test “sampling” is neither random nor reproducible | `SelectForTag` has no `ORDER BY`; PostgreSQL may return planner/storage-dependent rows. The repository itself records this TODO. Unchanged input can yield different, statistically unrepresentative samples. | Use deterministic seeded/hash sampling with an indexable strategy; store seed and algorithm version. |
| Medium | Capacity deduction is hard to reason about | Candidate population already excludes materialized members and active reservations, then approved demand is deducted. As states evolve, this can undercount or double-account if deduction is not exactly non-materialized demand. | Define/test the state invariant and calculate only demand not already excluded at audience level. |
| Medium | Test previews cannot reserve against future approved work | The Test sampling flow explicitly notes that approved campaigns have no concrete audience IDs until their schedulers run, so a preview cannot exclude those future allocations. It can therefore be valid at preview time but conflict at finalization/materialization. | Materialize/reserve approved demand earlier, or make the UI and approval path explicitly handle a preview becoming unavailable and requiring refresh. |
| Medium | AutoSelect and displayed list have different query shapes | `ListAvailableTagIDs` omits campaign-specific Test-performance join present in list rows. Current sorts may not expose it, but future campaign-specific order can diverge from UI. | Share a builder or limit/document AutoSelect sorts that cannot depend on campaign-only fields. |
| Medium | Legacy Execution fallback has weaker guarantees | Reservation-aware campaigns fail closed, but explicit legacy compatibility can select recipients during scheduling. It differs from modern pre-reservation semantics. | Retire fallback on a defined migration date; test no newly finalized campaign can enter it. |
| Low | A/B/C terminology hides unscored exclusion | Restricted classes exclude `normalized_score IS NULL`, while all A/B/C bypasses classification. User expectations may differ. | Document it in UI/API or make unscored an explicit product class. |
| Low | Queue/running vocabulary is inconsistent | Capacity/Test use `calculating` before and after claim; Execution uses `pending`. Clients infer running state from `started_at`. | Unify lifecycle DTO states or document `started_at` as the discriminator. |

## Bottlenecks and scaling limits

| Area | Bottleneck | Effect |
| --- | --- | --- |
| Capacity | Large anti-join population scans | Eligibility queries run multiple `NOT EXISTS` checks against historical selections, both reservation types, committed calculations, and exclusions; restricted classes also require percentile work. |
| Test sampling | One query per selected tag | Bounds are shared, but each tag runs candidate selection and the exclusion-ID array grows. Many tags/samples increase database time and parameter size. |
| Execution | Ordered exact selection | Candidate population must be filtered and sorted by score/ID before limiting, competing with capacity and send-time work. |
| Locks | Bundle share/update serialization | Deliberate locking preserves uniqueness, but capacity/Test workers can wait behind approval or materialization for the same bundle. |
| Tag listing | Broad derived joins plus count | Lists join score snapshots, CTR, ATR, campaign performance, selection state, and attribution use; large catalogs can be costly. |
| Worker pools | Poll delay and fixed parallelism | Work waits for poll and uses bounded pools. Raising concurrency alone can overload the single database because every job scans audiences. |

Monitor queue age, lease-reclaim count, calculation duration by phase/class/tag
count, bundle-lock wait time, selected-tag count, and `EXPLAIN (ANALYZE,
BUFFERS)` for production-shaped audience queries. Preserve the principal
invariant while optimizing: an audience must not be silently reused across
conflicting campaign work in the same bundle.
