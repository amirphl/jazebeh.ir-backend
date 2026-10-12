# Smart Targeting capacity: known limitations

This document records follow-up work for the exact Smart Targeting capacity
flow. The current implementation is safe at selection time, but the capacity
snapshot can be conservative or become stale before execution.

## Capacity snapshot does not track audience-data changes

**Priority: high — requires an explicit freshness policy.**

Currentness validates the campaign's targeting inputs, expiry, and Bundle
allocation fingerprint. It does not detect changes to the audience population
used by the count query: tag assignments, phone eligibility, normalized scores,
delivery colors, or audience creation/deletion.

The result is exact when it is calculated, but can remain operationally
"current" until its expiry. Expiry may be extended through `schedule_at + 24h`,
so a far-future campaign can use an old snapshot for approval. The scheduler
still selects from the live population and fails safely if it cannot select the
required number, but an approved and funded campaign may then fail late.

Relevant code:

- `business_flow/smart_targeting_capacity_flow.go`: `isCurrentSmartTargetingCapacity`
- `repository/smart_targeting_audience_repository.go`: base population and
  score-class eligibility queries

Follow-up decision: define whether capacity is a short-lived estimate, or an
approval guarantee. If it must be a guarantee, add a suitable population
version/fingerprint or require a fresh calculation immediately before approval
or dispatch.

## Bundle exclusions do not participate in capacity currentness

**Priority: medium — applicable only when exclusions can change independently
of active Test reservations.**

Test capacity applies `bundle_audience_exclusions`, but neither the input hash
nor allocation fingerprint contains the exclusion set. A changed exclusion row
therefore does not by itself make an existing capacity calculation stale. The
Test reservation path rechecks exclusions as a hard safety measure, so this is
a late-failure/stale-preview concern rather than an excluded-audience delivery
risk.

This limitation is redundant if every exclusion-table change is guaranteed to
have a matching active Test-reservation change. Confirm the producer and
lifecycle of `bundle_audience_exclusions` before prioritizing a fix.

Relevant code:

- `business_flow/smart_targeting_capacity_flow.go`: allocation fingerprint
  construction
- `repository/smart_targeting_audience_repository.go`: Bundle exclusion
  anti-join
- `repository/campaign_targeting_test_sample_selection_repository.go`:
  reservation availability check

## Approved-campaign deduction is a conservative lower bound

**Priority: medium — capacity is not literally exact while other campaigns are
approved but unmaterialized.**

For each other approved/running campaign without a materialized Bundle audience
selection, capacity subtracts its whole `num_audience`. At that point the system
does not know the concrete recipients, so it cannot calculate the true overlap
with this campaign's tags, grades, platform colors, or exclusions.

This prevents overcommitment but can reject a campaign with disjoint eligible
audiences. The reported usable capacity is consequently a safe lower bound,
not an exact count, until all competing allocations are materialized.

Relevant code:

- `business_flow/smart_targeting_capacity_flow.go`: Bundle allocation
  deduction
- `repository/campaign_execution_repository.go`: Bundle campaign allocation
  query

Follow-up decision: retain this conservative behavior and communicate it as an
estimate/lower bound, or introduce an earlier concrete reservation mechanism
that supports overlap-aware capacity.
