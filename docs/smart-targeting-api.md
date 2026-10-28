# Smart Targeting API

All endpoints require customer JWT authentication and enforce campaign or
Bundle ownership. Routes use the `/api/v1` prefix and return the common
`APIResponse` envelope.

## Endpoint summary

| Method and path | Purpose |
|---|---|
| `GET /bundles/{id}/smart-targeting/tags` | Browse a Bundle’s selectable tags before a campaign exists. |
| `GET /campaigns/{uuid}/smart-targeting/tags` | Browse tags plus the campaign’s complete selection state. |
| `GET /campaigns/{uuid}/smart-targeting/selection` | Read the ordered persisted selection. |
| `PUT /campaigns/{uuid}/smart-targeting/selection` | Atomically replace the complete ordered selection. |
| `POST /campaigns/{uuid}/smart-targeting/selection/auto` | Select from the complete filtered/sorted result. |
| `POST /campaigns/{uuid}/smart-targeting/capacity-calculations` | Start or reuse an asynchronous exact-capacity generation. |
| `GET /campaigns/{uuid}/smart-targeting/capacity-calculations` | Read the current/latest generation state. |
| `GET /campaigns/{uuid}/smart-targeting/capacity-calculations/{calculation_id}` | Read one historical generation. |
| `POST /campaigns/{uuid}/smart-targeting/test-sampling-preview` | Start a new immutable asynchronous Test-phase sampling generation. |
| `GET /campaigns/{uuid}/smart-targeting/test-sampling-preview` | Read the current/latest sampling job state. |
| `GET /campaigns/{uuid}/smart-targeting/test-sampling-preview/{calculation_id}` | Read one historical sampling job. |

The path column above omits the shared `/api/v1` prefix for readability.

## Tag browsing and selection

Both tag-list endpoints accept:

- `search`: case-insensitive tag name/display-title search, maximum 200
  characters;
- `sort_by`: `tag_capacity`, `bundle_persona_fit_score`,
  `test_phase_avg_ctr`, or `overall_avg_ctr`;
- `sort_direction`: `asc` or `desc`;
- `page`: positive page number, default 1;
- `page_size`: 1–100, default 20; `limit` is a compatibility alias and is
  ignored when `page_size` is supplied.

`bundle_persona_fit_score` requires a completed Bundle evaluation.
`test_phase_avg_ctr` reads the last materialized Bundle/tag Test result and is
null until at least one attributed delivery exists. Sorting by that field uses
the materialized value, with unavailable rows last.

The effective tag source is resolved per Bundle:

- if the Bundle has a current completed smart-tag evaluation, the complete
  score snapshot supplies tag metadata, capacity, fit score, and explanation
  fields;
- otherwise, active live `tags` rows are used and evaluation fields are null.

Sources are never partially merged. An unevaluated Bundle defaults to tag ID
ascending. An evaluated Bundle defaults to fit score descending with tag ID as
the stable tie-breaker.

The campaign-scoped list returns `selected_tag_ids` for the entire selection,
not just the current page, plus `selected_tag_count` and
`selected_raw_capacity`. The ID array retains the customer’s selection order
independently of the table’s current search, sort, and pagination. The Bundle
endpoint has no campaign selection and returns an empty selection summary.

Each tag row also exposes materialized aggregate fields:

- `total_test_selected_count`, `total_test_sent_count`,
  `total_test_delivered_count`, and `total_test_click_count`;
- `test_phase_avg_ctr`, calculated as total clicks divided by total deliveries.

The campaign-scoped endpoint additionally exposes `selected_count`,
`sent_count`, `delivered_count`, `click_count`, and `test_campaign_ctr`. These
fields are null until that Campaign's report is prepared. CTR is null—not
zero—when its delivery denominator is zero.

Replace a selection with:

```json
{
  "tag_ids": [42, 7, 19]
}
```

The array must contain 1–10,000 unique, non-zero IDs available from the
Bundle’s effective source. Order is significant. Replacement is permitted only
while the campaign is editable, uses `smart_targeting`, and belongs to the
authenticated customer. Validation and replacement occur atomically.

Automatic selection accepts:

```json
{
  "count": 100,
  "search": "optional filter",
  "sort_by": "tag_capacity",
  "sort_direction": "desc"
}
```

It selects from the entire filtered order, not a visible page, and replaces the
complete selection. If fewer rows match than requested, all matching rows are
selected. `count` must be between 1 and 10,000.

`selected_raw_capacity` sums the capacity snapshot stored for each selected
tag. It does not deduplicate audiences shared across tags and is not the exact
usable capacity.

## Exact capacity calculations

Start a generation with an empty body to use the campaign’s persisted
`audience_grades`, or override the campaign’s score-class selection while it is
editable:

```json
{
  "score_classes": ["A", "B"]
}
```

Allowed classes are A, B, and C, case-insensitive on this endpoint. Duplicates
or unknown classes are rejected. An empty effective class selection means all
three classes.

The start endpoint responds with HTTP 202. Calculations are asynchronous and
must be executed by a process with
`SMART_TARGETING_CAPACITY_SCHEDULER_ENABLED=true`. Duplicate starts for the
same inputs reuse or report the active generation rather than creating
parallel work.

Polling responses expose:

- `status`: `not_calculated`, `calculating`, `calculated`, `failed`, `stale`, or
  `expired` as applicable;
- `is_current` and `recalculation_required`;
- selected classes/tag count and timestamps;
- `raw_audience_count`;
- `eligible_unique_audience_count_before_approved_campaign_deduction`;
- `approved_campaign_audience_deduction`;
- `usable_unique_audience_count`;
- sanitized error code/message for failed work.

Count fields are present only for a current calculated generation, so missing
is distinct from a real zero. A calculation is current only while its selected
tags, score classes, platform eligibility, Test Bundle-exclusion mode,
algorithm/input fingerprint, Bundle allocation fingerprint, and expiry still
match. SMS generations count only white/pink audiences. Smart Targeting Test
generations also exclude the Bundle audience-exclusion list. Generations
normally expire after 24 hours; a scheduled campaign extends expiry to at
least 24 hours after its scheduled time.

Changing selection or score classes makes previous results stale. Approved and
not-yet-materialized campaign allocations are deducted to close the
approval-to-scheduler reservation gap. Campaign cost/finalization, approval,
and Execution preparation require a current exact capacity and can return a
pending/recalculation conflict. Test sampling itself does not.

## Campaign fields

Campaign create/update accepts:

```json
{
  "audience_targeting_method": "smart_targeting",
  "bundle_id": 12,
  "phase": "test",
  "selected_tag_ids": [42, 7, 19],
  "sample_size_per_tag": 100,
  "audience_grades": ["A", "B"]
}
```

`audience_targeting_method` values are:

- `standard`: existing level/category/tag filters;
- `smart_targeting`: Bundle-scoped selected tag IDs and exact capacity;
- `excel`: uploaded target audience file.

For create, `selected_tag_ids` is persisted atomically with the campaign. On
update, omission preserves the selection while an explicitly supplied array
replaces it. Only Smart Targeting accepts selected tag IDs.

## Test-phase sampling

A Smart Targeting campaign with `phase: "test"` requires a positive
`sample_size_per_tag`; there is no default. `budget` and a caller-supplied
audience count do not override the derived Test count.

Sampling can be requested before any exact-capacity generation exists. The
POST endpoint responds with HTTP 202 after durably submitting a new generation.
A process with `SMART_TARGETING_TEST_SAMPLING_SCHEDULER_ENABLED=true` executes
sampling asynchronously. A later request supersedes an in-flight prior request
and becomes the only generation eligible to publish the active selection.

Poll the collection GET for the current/latest job or the ID-scoped GET for a
specific historical job. Status is `not_calculated`, `calculating`,
`calculated`, `failed`, or `stale`. Completed result fields are returned only
when `is_current` is true; stale output is deliberately hidden and
`recalculation_required` is true.

Clients must treat GET as read-only: it never queues sampling work. After a
selection or other sampling input changes, and whenever GET reports
`not_calculated`, `stale`, `failed`, or `recalculation_required: true`, submit
one POST to request/retry the generation, then poll only while the returned
job is `calculating`. Repeated POSTs for unchanged input reuse the active job
or a current completed result; clients should nevertheless avoid using polling
as the trigger for POST retries.

The worker processes selected tag IDs in persisted order:

1. choose exactly `sample_size_per_tag` currently eligible audiences for the
   tag; no audience ordering is promised;
2. if the full sample is unavailable, mark the tag unsatisfied and consume no
   partial sample;
3. exclude audiences assigned to each earlier satisfied tag, so first
   successful attribution wins;
4. calculate `effective_audience_count = satisfied_tag_count ×
   sample_size_per_tag` and the campaign cost.

A current calculated response contains the complete sampling order, ordered
satisfied and unsatisfied results, each tag’s display name and available count,
sample size, effective count, and cost.

Every sampling request is an immutable, linearly ordered generation. A
completed generation persists its exact ordered audience IDs and their selected
tag attribution in a private sampling snapshot, as well as the public
satisfied-tag summary, input fingerprint, preview timestamp, and derived
compatibility value `num_audience`. The API never exposes those audience IDs.
Only the newest requested generation may become the campaign's active sample;
an older worker cannot replace it after a newer request. The input fingerprint
covers the campaign, Bundle, ordered selection, sample size, score classes,
and delivery color eligibility. Each completed job also stores its own
Bundle-allocation fingerprint. Editing sampling inputs or changing Bundle
allocation state makes the preview stale; starting an exact-capacity generation
by itself does not.

Finalization requires a current preview with at least one satisfied tag and a
current exact-capacity generation. Under the Bundle lock it activates a
releasable reservation for the exact persisted snapshot. At scheduler time,
that reservation is materialized into the normal Bundle audience allocation
ledger and the scheduler sends those stored audiences in the saved order; it
does not sample again or substitute candidates. Rejecting, cancelling, or
expiring a campaign releases an unmaterialized reservation while retaining the
immutable sampling history. Missing/unsafe persisted profiles fail safely and
are never replaced with fresh candidates.

Preview excludes audiences already materialized by earlier Bundle campaigns,
active Test-sample reservations, and audiences in the Bundle exclusion list.
An active reservation prevents another campaign from consuming a finalized
sample before runtime, without making abandoned preview history permanent.

## Execution-phase ordering

For `phase: "execution"`, `sample_size_per_tag` has no effect. Requested
audience count must not exceed the current exact usable capacity. Eligible
audiences are discovered at campaign finalization by `normalized_score DESC`,
null scores last, with audience ID as the stable tie-breaker. The expensive
discovery scan happens before the Bundle critical section. The short final
transaction rechecks the allocation fingerprint, Bundle exclusions,
phone/tag/color eligibility, active reservations, and permanent allocations,
then persists an immutable header plus the exact ordered, attributed members
with the waiting-for-approval/funds transition.

At scheduler time that reservation is materialized into the normal Bundle
allocation ledger and used for best-effort sending; unreachable recipients are
skipped and are never replaced with newly selected audiences. Rejecting,
cancelling, or expiring an unmaterialized campaign releases it. The Bot API
explicitly sends compatibility version `0` only for campaigns approved before
the migration. Missing, unsupported, stale, partial, or corrupt modern
reservations fail closed and never use legacy scheduler-time selection.

For Test and Execution, preparation writes immutable
campaign/audience/assigned-tag rows to
`campaign_audience_tag_attributions`. A durable scheduler configured with
`TAG_TEST_PERFORMANCE_SCHEDULER_ENABLED=true` observes new clicks and delivery
updates, then recomputes each affected Test or Execution Campaign from its
complete history. The setting name is retained for backward compatibility.
Its polling interval is configured with
`TAG_TEST_PERFORMANCE_SCHEDULER_INTERVAL`. Multi-tag audiences contribute only
to their persisted `assigned_tag_id`; repeated clicks and repeated provider
status rows do not inflate audience-level counts. Test CTR remains scoped to
the Bundle's Test Campaigns. Overall CTR is global across both eligible phases;
both divide clicking audiences by delivered audiences and remain null when the
delivery denominator is zero.

The Execution tag table's default order is materialized Test CTR descending,
then Bundle persona-fit score descending for tags without Test CTR, then tag ID
as the stable database-order fallback. Selecting tags snapshots display title,
capacity, persona-fit score, Test CTR, and overall CTR for historical reports.
