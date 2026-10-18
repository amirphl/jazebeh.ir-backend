Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_flow.go](business_flow/smart_targeting_flow.go)

Found five logical issues; no code was changed.

1. **High — stale exact capacity can remain “current” after a selection becomes invalid.**  
   A new bundle evaluation can make persisted tags invalid under `CampaignSelectedTagRepository.Validate`, but `isCurrentSmartTargetingCapacity` only compares IDs, hashes, configuration, expiry, and allocation fingerprint—it never validates the selected tags. Thus an old calculation can still be reused by capacity and cost paths.  
   Evidence: [capacity validity check](business_flow/smart_targeting_capacity_flow.go:573), versus validation only when starting a new calculation [here](business_flow/smart_targeting_capacity_flow.go:305); reused by [capacity](business_flow/campaign_flow.go:1416) and [cost](business_flow/campaign_flow.go:1836).  
   Fix: include selection-source/evaluation generation in validity, or call `selectionRepo.Validate` in `isCurrentSmartTargetingCapacity`.

2. **High — selection responses are internally inconsistent under concurrent replacements.**  
   `ListTags` separately queries tag rows, selected IDs, and summary; `GetSelection` separately queries IDs and summary. A replacement committed between those statements can produce contradictory data (e.g. a row says tag A is selected, while `selected_tag_ids` contains B/C; or count/capacity does not match the returned IDs). The successful replacement response has the same issue because it calls `GetSelection` only after committing.  
   Evidence: [ListTags reads](business_flow/smart_targeting_flow.go:167), [GetSelection reads](business_flow/smart_targeting_flow.go:303), [post-commit reread](business_flow/smart_targeting_flow.go:371).  
   Fix: build each response in one repeatable-read/read-only transaction, or derive the summary from the exact selected rows already read.

3. **Medium — auto-select does not select from the snapshot it claims to use.**  
   Candidate IDs are read before the transaction and campaign lock; only `replace` later locks and revalidates them. Evaluation completion, tag activation changes, or CTR updates can change the source/order between those operations. It may save tags that are no longer the first `Count`, or fail after initially finding valid candidates.  
   Evidence: [candidate query](business_flow/smart_targeting_flow.go:413) occurs before [replacement transaction/lock](business_flow/smart_targeting_flow.go:353).  
   Fix: lock the campaign and select candidates inside the same transaction used for replacement.

4. **Medium — the API discards essential tag identity and explanation data.**  
   The repository explicitly fetches `tag_name`, persona, and evaluation `reason`, but the DTO and mapper comment them out. A tag with no display title is returned with only an opaque ID; users also cannot see the evaluation explanation promised by the DTO/endpoint comments.  
   Evidence: repository selects the fields [here](repository/campaign_selected_tag_repository.go:183), but the DTO omits them [here](app/dto/smart_targeting_dto.go:23) and the mapper drops them [here](business_flow/smart_targeting_flow.go:261).  
   Fix: restore `tag_name`, `tag_audience_persona`, and `reason` to the response contract and map them.

5. **Low — unbounded page numbers can overflow the offset calculation.**  
   The handlers permit any positive `int` page number, while the flow calculates `(page - 1) * pageSize` without overflow checking. A huge valid query value can wrap negative; the repository then skips `Offset` and can return the first page while echoing the huge page number.  
   Evidence: unbounded handler parsing [here](app/handlers/campaign_handler.go:287), unsafe multiplication [here](business_flow/smart_targeting_flow.go:166) and again [here](business_flow/smart_targeting_flow.go:231).  
   Fix: apply a maximum page or checked multiplication before querying.


I changed the codebase a bit. Once again review and find logical bugs/issues related to [smart\_targeting\_flow.go](business_flow/smart_targeting_flow.go)

The selected-tag validity check added to capacity currentness fixes the prior stale-tag reuse issue. Remaining findings:

1. **High — tag-list and selection responses can contradict themselves during a concurrent replacement.** `ListTags` reads table rows, selected IDs, and summary in separate statements; `GetSelection` separately reads IDs and summary. A replacement between reads can return mixed versions.  
   [smart_targeting_flow.go:167](business_flow/smart_targeting_flow.go:167), [smart_targeting_flow.go:303](business_flow/smart_targeting_flow.go:303)  
   Use one transaction/snapshot or derive the summary from the same selected rows.

2. **Medium — auto-select is still TOCTOU.** It picks candidate IDs before acquiring the campaign lock used for replacement. A new evaluation/tag change can alter ordering or availability in between, so it may save a no-longer-top selection or fail unexpectedly.  
   [smart_targeting_flow.go:413](business_flow/smart_targeting_flow.go:413), [smart_targeting_flow.go:353](business_flow/smart_targeting_flow.go:353)  
   Fetch candidates inside the locked replacement transaction.

3. **Medium — dormant selections are presented as active after switching targeting methods.** Campaign updates intentionally retain Smart Targeting selections while standard/Excel is active, but `GetSelection` and `ListTags` do not require Smart Targeting mode and return those IDs/`selected=true` without marking them inactive.  
   [campaign_flow.go:468](business_flow/campaign_flow.go:468), [smart_targeting_flow.go:298](business_flow/smart_targeting_flow.go:298)  
   Reject these reads outside Smart Targeting mode, or return an explicit `active` flag.

4. **Medium — API contract drops tag identity and evaluation explanation.** The repository fetches tag name, persona, and reason, yet the DTO/mapper omit all three. A tag without `display_title` becomes an opaque ID, and the claimed evaluation explanation is unavailable.  
   [campaign_selected_tag_repository.go:187](repository/campaign_selected_tag_repository.go:187), [smart_targeting_dto.go:23](app/dto/smart_targeting_dto.go:23), [smart_targeting_flow.go:260](business_flow/smart_targeting_flow.go:260)

5. **Low — page arithmetic can overflow.** Huge positive `page` values are accepted, then `(page - 1) * pageSize` can wrap. The repository ignores negative offsets, potentially returning page one while reporting the huge requested page.  
   [campaign_handler.go:287](app/handlers/campaign_handler.go:287), [smart_targeting_flow.go:166](business_flow/smart_targeting_flow.go:166)

6. **Cross-flow high — current capacity remains valid after audience population/exclusion changes.** The new check validates selected tags, but not changes to audience tags, eligibility, scores, colors, or bundle exclusions used by the capacity query. A calculated capacity can therefore remain “current” until expiry despite an altered live population.  
   [smart_targeting_capacity_flow.go:573](business_flow/smart_targeting_capacity_flow.go:573)

`go test ./business_flow ./repository` passes.


--------------

Some issues fixed.
Once again Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_capacity\_flow.go](business_flow/smart_targeting_capacity_flow.go) .

------

I found three remaining logical issues:

1. **High — “current” capacity does not track audience-profile changes.**  
   A generation stays current if its campaign inputs and allocation fingerprint match, but that fingerprint excludes changes to audience `tags`, `phone_number`, `color`, and `normalized_score`. Those changes directly alter eligibility—and, for restricted grades, percentile boundaries. With a far-future schedule, validity can last until `schedule_at + 24h`, so an old count may be approved as exact and later fail live scheduler selection. See [currentness validation](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_capacity_flow.go:573) and [expiry calculation](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_capacity_flow.go:111).

2. **High — active Test-reservation releases are not serialized with capacity counting.**  
   The worker relies on a Bundle `FOR SHARE` lock while counting and fingerprinting ([worker](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_capacity_flow.go:759)). But `ReleaseForCampaign` only locks the campaign and updates reservation rows; it does not lock the Bundle ([repository](/home/amirphl/Downloads/Yamata-no-Orochi/repository/campaign_targeting_test_sample_selection_repository.go:259)). A cancellation/rejection can therefore commit between the capacity query and fingerprint query, producing a count based on the old reservation state but a fingerprint for the new state. That result is then incorrectly considered current.

3. **Medium — manual Bundle exclusion changes do not invalidate Test capacity.**  
   Test capacity explicitly excludes `bundle_audience_exclusions` ([audience query](/home/amirphl/Downloads/Yamata-no-Orochi/repository/smart_targeting_audience_repository.go:90)), but neither the input hash nor allocation fingerprint represents those rows ([fingerprint construction](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_capacity_flow.go:646)). Adding/removing an exclusion can therefore leave a Test capacity result marked current despite a changed eligible population.

Verification: `go test ./business_flow ./repository` passed; focused Smart Targeting scheduler tests passed. The full scheduler package is blocked by the sandbox preventing an `httptest` listener from binding.


------------

Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_capacity\_flow.go](business_flow/smart_targeting_capacity_flow.go) .

-------

Found four logical issues. No files were changed.

- **High — a Test campaign can invalidate its own approval by recalculating capacity after finalization.** Capacity queries exclude *all* active Test reservations, including the requesting campaign’s reserved sample. A Test campaign is still allowed to start capacity calculations while `waiting-for-approval` or `approved`; its new result can therefore exclude its own required audience. Approval then rejects it because `requiredAudience > usable capacity`. See [allowed states](business_flow/smart_targeting_capacity_flow.go:95), [unscoped reservation exclusion](repository/smart_targeting_audience_repository.go:79), and [approval comparison](business_flow/campaign_admin_flow.go:564).

- **High — “current exact capacity” is not invalidated when its actual audience population changes.** `isCurrentSmartTargetingCapacity` validates only campaign inputs and the allocation fingerprint; it never versions or rechecks audience tags, phone validity, colors, scores, additions/deletions, or Test bundle-exclusion membership. Yet all of those directly alter the count query. The UI and approval flow can consequently treat up-to-24-hour-old counts as current. See [currentness checks](business_flow/smart_targeting_capacity_flow.go:573) versus [population predicates](repository/smart_targeting_audience_repository.go:61).

- **High — a calculated result remains valid after selected tags become unavailable.** Tag availability is checked when starting and executing a calculation, but not when reusing or validating one. If a tag is deactivated or removed from the bundle’s eligible tag source after the calculation, `CurrentSmartTargetingCapacity` can still authorize approval, and the scheduler does not repeat `selectionRepo.Validate` before selecting audiences. Compare [start-time validation](business_flow/smart_targeting_capacity_flow.go:305) with [reuse validation](business_flow/smart_targeting_capacity_flow.go:573) and [scheduler execution](app/scheduler/smart_targeting_capacity_candidates.go:111).

- **Medium — the “approved campaign deduction” is not exact and can falsely report zero/insufficient capacity.** For unmaterialized approved/running campaigns, the flow subtracts the entire `NumAudience` from every Smart Targeting population, despite not knowing whether those future recipients overlap this campaign’s tags, score classes, platform colors, or exclusions. Only materialized selections can be excluded precisely. This creates unnecessary approval/finalization failures for disjoint audiences. See [deduction logic](business_flow/smart_targeting_capacity_flow.go:643) and [allocation query](repository/campaign_execution_repository.go:137).

Targeted tests currently pass: `go test ./business_flow ./repository ./app/scheduler`.

------------

Once again Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_capacity\_flow.go](business_flow/smart_targeting_capacity_flow.go) .

-----

Fresh review found these concrete issues:

- **High — stale capacity survives audience-data changes.** Currentness checks only campaign inputs, expiry, and allocation fingerprint; they do not detect changed audience tags, phone values, scores, delivery colors, additions/deletions, or Test exclusion membership. Counts can be treated as exact long after their query population changed—especially because expiry may extend past the scheduled date. [Validation](business_flow/smart_targeting_capacity_flow.go:573) vs. [query inputs](repository/smart_targeting_audience_repository.go:61).

- **High — removed/deactivated selected tags can still be approved.** The flow validates tag availability only when creating a job. Reuse/currentness and `CurrentSmartTargetingCapacity` merely load selected IDs; they do not call `selectionRepo.Validate`. Admin approval therefore can accept a stale result, lock budget, then leave the campaign approved but unschedulable when the bot path finally validates those tags. [Start validation](business_flow/smart_targeting_capacity_flow.go:305), [missing current validation](business_flow/smart_targeting_capacity_flow.go:573), [approval](business_flow/campaign_admin_flow.go:539), [bot validation](business_flow/campaign_bot_flow.go:188).

- **High — recalculating a finalized Test campaign counts out its own reserved sample.** The audience CTE excludes every active Test reservation without excluding the current campaign. Since `Start` permits `waiting-for-approval` and `approved`, a recalculation after reservation creation returns a capacity that excludes its own required sample. [Allowed statuses](business_flow/smart_targeting_capacity_flow.go:95), [reservation anti-join](repository/smart_targeting_audience_repository.go:79).

- **Medium — bundle exclusions do not invalidate results.** Test capacity applies `bundle_audience_exclusions`, but the allocation fingerprint omits them, and the input hash includes only the boolean “apply exclusions,” not the actual exclusion set. Adding/removing an exclusion leaves an old capacity marked current; final reservation later fails its hard availability check. [Fingerprint construction](business_flow/smart_targeting_capacity_flow.go:615), [exclusion query](repository/smart_targeting_audience_repository.go:83), [reservation safety check](repository/campaign_targeting_test_sample_selection_repository.go:147).

- **Medium — approved-campaign deduction is intentionally approximate, but presented as exact.** It subtracts each other unmaterialized campaign’s whole `NumAudience` regardless of overlap with this campaign’s tags, grades, platform colors, or exclusions. Disjoint campaigns can therefore receive zero/insufficient capacity even when all of their actual audiences remain available. [Deduction](business_flow/smart_targeting_capacity_flow.go:643), [source rows](repository/campaign_execution_repository.go:137).

Targeted tests pass: `go test ./business_flow ./repository ./app/scheduler`.

---------

What do you mean by "Test exclusion membership"

Assume audiences and tags merely change/evolve over the time. Is the following still a critical issue?
**High — stale capacity survives audience-data changes.** Currentness checks only campaign inputs, expiry, and allocation fingerprint; they do not detect changed audience tags, phone values, scores, delivery colors, additions/deletions, or Test exclusion membership. Counts can be treated as exact long after their query population changed—especially because expiry may extend past the scheduled date. [Validation (line 573)](business_flow/smart_targeting_capacity_flow.go:573) vs. [query inputs (line 61)](repository/smart_targeting_audience_repository.go:61).

Explain the following issue more clearly:
**High — removed/deactivated selected tags can still be approved.** The flow validates tag availability only when creating a job. Reuse/currentness and `CurrentSmartTargetingCapacity` merely load selected IDs; they do not call `selectionRepo.Validate`. Admin approval therefore can accept a stale result, lock budget, then leave the campaign approved but unschedulable when the bot path finally validates those tags. [Start validation (line 305)](business_flow/smart_targeting_capacity_flow.go:305), [missing current validation (line 573)](business_flow/smart_targeting_capacity_flow.go:573), [approval (line 539)](business_flow/campaign_admin_flow.go:539), [bot validation (line 188)](business_flow/campaign_bot_flow.go:188).

---

Yes—but I would qualify the first finding.

If audience data is expected to evolve, a capacity result is naturally a **time-bounded snapshot**, not a permanent guarantee. The code has a TTL for that reason. So the lack of invalidation for every audience edit is not automatically a critical bug.

It becomes serious when the system labels the result as “current exact capacity” and uses it to approve/lock money for campaigns scheduled much later. Here, expiry is extended to `schedule_at + 24h`, so a calculation made today for a campaign next month may remain “current” for a month even if the candidate population changes substantially. At execution, the scheduler reselects candidates and fails safely if it cannot find enough—but the campaign may already be approved with budget locked.

So I’d restate it as:

> **Medium/High policy mismatch:** capacity is treated operationally as current until a potentially distant schedule date, although it is only exact at calculation time. This produces approved campaigns that can later fail at dispatch when ordinary audience evolution reduces eligibility.

If that long-lived snapshot behavior is intentional and acceptable, it is not a bug; it is a documented business tradeoff. If approval requires a reliable capacity guarantee, then it is a high-severity issue.

For the selected-tag issue, the failure path is more deterministic:

1. A campaign selects tag `42`; it is currently valid for its bundle.
2. Capacity starts and calls `selectionRepo.Validate`, so the calculation is valid at that point.
3. Later, tag `42` is deactivated, or ceases to be available from the bundle’s current tag source. The row in `campaign_selected_tags` remains, however.
4. The capacity-currentness code only loads selected IDs and hashes them. It does **not** call `Validate`, so it still sees `[42]`, gets the same hash, and accepts the old capacity as current.
5. Admin approval uses that result, approves the campaign, and locks its budget.
6. At execution, `BotCampaignFlow.selectedTags` *does* call `Validate`; it fails, so the campaign cannot be dispatched. Since `ListReadyCampaigns` returns on that error, it can also prevent the ready-campaign response from being produced for that request.

In short: the capacity/approval path says “tag 42 is fine,” while the execution path says “tag 42 is no longer allowed.” That inconsistency leaves an approved, funded campaign stuck.

This issue remains high even with normally evolving audiences, because it concerns an invalid targeting configuration, not merely a changing count.

-----

Fix following two issues:



1. **High — removed/deactivated selected tags can still be approved.** The flow validates tag availability only when creating a job. Reuse/currentness and `CurrentSmartTargetingCapacity` merely load selected IDs; they do not call `selectionRepo.Validate`. Admin approval therefore can accept a stale result, lock budget, then leave the campaign approved but unschedulable when the bot path finally validates those tags. [Start validation (line 305)](business_flow/smart_targeting_capacity_flow.go:305), [missing current validation (line 573)](business_flow/smart_targeting_capacity_flow.go:573), [approval (line 539)](business_flow/campaign_admin_flow.go:539), [bot validation (line 188)](business_flow/campaign_bot_flow.go:188).
2. **High — recalculating a finalized Test campaign counts out its own reserved sample.** The audience CTE excludes every active Test reservation without excluding the current campaign. Since `Start` permits `waiting-for-approval` and `approved`, a recalculation after reservation creation returns a capacity that excludes its own required sample. [Allowed statuses (line 95)](business_flow/smart_targeting_capacity_flow.go:95), [reservation anti-join (line 79)](repository/smart_targeting_audience_repository.go:79).

----

Implemented both fixes.
- Capacity currentness now revalidates the selected tags. If a tag was deactivated or removed from the bundle’s available source, the calculation becomes stale and cannot be reused for approval. [smart_targeting_capacity_flow.go (line 590)](business_flow/smart_targeting_capacity_flow.go:590)
- Audience queries can now retain the current campaign’s own active Test reservation while excluding every other campaign’s reservation. Capacity snapshots pass their campaign ID into that scope. [smart_targeting_audience_repository.go (line 16)](repository/smart_targeting_audience_repository.go:16), [smart_targeting_capacity_flow.go (line 710)](business_flow/smart_targeting_capacity_flow.go:710)
- Added focused regression coverage for both behaviors.
Verified with:
GOCACHE=/tmp/yamata-smart-targeting-go-build go test ./business_flow ./repository ./app/scheduler

---

Explain the following issues too:

1. **Medium — bundle exclusions do not invalidate results.** Test capacity applies `bundle_audience_exclusions`, but the allocation fingerprint omits them, and the input hash includes only the boolean “apply exclusions,” not the actual exclusion set. Adding/removing an exclusion leaves an old capacity marked current; final reservation later fails its hard availability check. [Fingerprint construction (line 615)](business_flow/smart_targeting_capacity_flow.go:615), [exclusion query (line 83)](repository/smart_targeting_audience_repository.go:83), [reservation safety check (line 147)](repository/campaign_targeting_test_sample_selection_repository.go:147).
2. **Medium — approved-campaign deduction is intentionally approximate, but presented as exact.** It subtracts each other unmaterialized campaign’s whole `NumAudience` regardless of overlap with this campaign’s tags, grades, platform colors, or exclusions. Disjoint campaigns can therefore receive zero/insufficient capacity even when all of their actual audiences remain available. [Deduction (line 643)](business_flow/smart_targeting_capacity_flow.go:643), [source rows (line 137)](repository/campaign_execution_repository.go:137).

---

1. Bundle exclusions not invalidating results

For a Smart Targeting Test campaign, the capacity query excludes audience IDs in `bundle_audience_exclusions`.

Example:

- Capacity calculation says 1,000 audiences are available.
- Later, 200 matching audiences are added to the bundle exclusion list.
- The true available count is now 800.

The stored calculation can still be considered current because:

- Its input hash records only that exclusions are enabled for Test campaigns (`true`), not which audiences are excluded.
- Its allocation fingerprint tracks other campaign allocations and Test reservations, not `bundle_audience_exclusions`.

That means the UI or approval path may rely on the old result. Later, when the Test sample is reserved, `ReserveForCampaign` rechecks the selected audience IDs against the exclusion table and rejects any excluded IDs. The campaign is safe from using excluded audiences, but it reaches the failure late—after showing/using a seemingly current capacity or preview.

This is medium severity because it does not usually permit an invalid delivery; it produces stale capacity and a late “refresh preview/capacity” failure.

2. Approved-campaign deduction is approximate

Before an approved campaign has actually selected concrete audience IDs, the system knows only its requested `NumAudience`, not who its recipients will be.

So, for another campaign’s capacity, it conservatively subtracts that full number:

```text
candidate capacity:                    1,000
another approved campaign's audience:   -300
reported usable capacity:                700
```

But that other campaign may target an entirely different population.

Example:

- Campaign A: tag `sports`, SMS, grade A; requests 300.
- Campaign B: tag `fashion`, Bale, grade C; has 1,000 eligible people.
- A and B have no overlapping audiences.

While A is approved but unmaterialized, B’s calculation still deducts A’s full 300 and reports only 700, even though B actually still has all 1,000 eligible recipients.

The deduction is deliberately conservative: it avoids approving two campaigns based on the same shared pool before either has concrete reservations. But “exact usable capacity” is therefore not literally exact in this state—it is a safe lower bound.

This is medium severity because it causes false insufficient-capacity responses or prevents approval unnecessarily; it does not create audience reuse.

---

I think "Bundle exclusions not invalidating results" is not an issue because that "200" audiences were part of another tag sampling for another campaign within the same bundle which its capacity was excluded already. Is this true?


----

Not necessarily. In this code, those are two separate mechanisms:

- `campaign_targeting_test_sample_reservations` tracks audiences reserved by another finalized Test campaign. Capacity already excludes these, and the allocation fingerprint tracks them.
- `bundle_audience_exclusions` is a separate, manually populated bundle-level denylist. It is also applied to Test capacity, preview, and reservation safety checks.

So if those 200 people are genuinely reserved by another campaign, they should be represented by active reservation rows; that part is already invalidated correctly.

The exclusion-invalidating finding matters only if `bundle_audience_exclusions` can change independently—for example, an operator/import adds opt-outs, or an external process writes the table. In the repository, I found reads of this table but no code that writes it, so its producer is outside the reviewed flow.

If your system guarantees that every exclusion-table change always has a matching active reservation change, then the exclusion fingerprint is redundant and this is not a practical issue. Otherwise, it remains a stale-result risk.

---

State the following issues somewhere in a file or in codebase so that later we solve them:
**High — stale capacity survives audience-data changes.** Currentness checks only campaign inputs, expiry, and allocation fingerprint; they do not detect changed audience tags, phone values, scores, delivery colors, additions/deletions, or Test exclusion membership. Counts can be treated as exact long after their query population changed—especially because expiry may extend past the scheduled date. [Validation (line 573)](business_flow/smart_targeting_capacity_flow.go:573) vs. [query inputs (line 61)](repository/smart_targeting_audience_repository.go:61).

**Medium — bundle exclusions do not invalidate results.** Test capacity applies `bundle_audience_exclusions`, but the allocation fingerprint omits them, and the input hash includes only the boolean “apply exclusions,” not the actual exclusion set. Adding/removing an exclusion leaves an old capacity marked current; final reservation later fails its hard availability check. [Fingerprint construction (line 615)](business_flow/smart_targeting_capacity_flow.go:615), [exclusion query (line 83)](repository/smart_targeting_audience_repository.go:83), [reservation safety check (line 147)](repository/campaign_targeting_test_sample_selection_repository.go:147).

**Medium — approved-campaign deduction is intentionally approximate, but presented as exact.** It subtracts each other unmaterialized campaign’s whole `NumAudience` regardless of overlap with this campaign’s tags, grades, platform colors, or exclusions. Disjoint campaigns can therefore receive zero/insufficient capacity even when all of their actual audiences remain available. [Deduction (line 643)](business_flow/smart_targeting_capacity_flow.go:643), [source rows (line 137)](repository/campaign_execution_repository.go:137).

---

Some issues fixed.
Once again Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_capacity\_flow.go](business_flow/smart_targeting_capacity_flow.go) .

----

Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_sampling\_flow.go](business_flow/smart_targeting_sampling_flow.go)


---

Review findings:

1. **High — repeated preview requests cancel identical in-flight jobs.**  
   [`StartSmartTargetingTestSampling`](business_flow/smart_targeting_sampling_flow.go:333) supersedes *any* active calculation before creating a new generation, even when the current input hash is identical. The unused `reusableActiveSmartTargetingTestSampling` helper suggests reuse was intended. Client retries/polling mistakes can continuously cancel work and prevent a preview from completing.

2. **High — “random per tag” sampling is neither random nor deterministic.**  
   The persisted execution attribution calls it `random_per_tag`, but [`SelectForTag`](repository/smart_targeting_audience_repository.go:252) produces a query with `LIMIT` and no `ORDER BY` ([query construction](repository/smart_targeting_audience_repository.go:332)). PostgreSQL may return storage/plan-dependent rows, creating bias and unstable samples across equivalent calculations. Use an explicit deterministic seeded order or a defined random sampling strategy.

3. **High — a preview can remain current after its audience no longer satisfies targeting rules.**  
   Sampling filters tag membership, score class, color, phone, exclusions, and reservations ([selection query](repository/smart_targeting_audience_repository.go:335)). But freshness checks only validate campaign input and bundle allocation fingerprint ([`isCurrent...`](business_flow/smart_targeting_sampling_flow.go:477)); that fingerprint does not represent audience profile/tag/score/color changes. At reservation time, [`ReserveForCampaign`](repository/campaign_targeting_test_sample_selection_repository.go:150) checks only existence, phone, materialization, reservation, and exclusion—not tags, score class, or color. Thus a profile changed after preview can still be delivered despite no longer matching the Test campaign. Add a population revision/expiry or fully revalidate snapshot members before reserving.

4. **Low — tag replacement invalidates preview fields but leaves the active selection pointer.**  
   [`invalidateCampaignSmartTargetingTestPreview`](repository/campaign_selected_tag_repository.go:32) clears the preview hash/counts but not `active_smart_targeting_test_selection_id`, unlike [`clearCampaignSmartTargetingTestSamplingPreview`](business_flow/smart_targeting_sampling_flow.go:237). Guards currently prevent finalization of that stale selection, but the campaign retains an internally inconsistent reference and exposes it through normal campaign reads.

Focused `business_flow` and `repository` tests passed.

---

Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_sampling\_flow.go](business_flow/smart_targeting_sampling_flow.go)

---

Found three material issues; no code changed.

1. **High — a “current” Test sample can remain valid forever despite audience eligibility changing.** Unlike capacity, sampling has no expiry or audience-population version check. It persists exact IDs, and reservation only rechecks existence, usable phone, existing allocations/reservations, and bundle exclusions—not tag membership, score-grade eligibility, or SMS color. A later delivery can therefore include people who no longer match the campaign’s targeting criteria. [sampling currentness](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_sampling_flow.go:487), [reservation recheck](/home/amirphl/Downloads/Yamata-no-Orochi/repository/campaign_targeting_test_sample_selection_repository.go:147).

2. **Medium — greedy tag processing can falsely mark later tags unsatisfied.** The flow assigns candidates to tags sequentially and permanently excludes them before evaluating later tags. With sample size 1, tag A `{x,y}` and tag B `{x}`, selecting `x` for A makes B fail, although assigning `y` to A and `x` to B satisfies both. No reassignment/backtracking occurs. [sequential exclusion](/home/amirphl/Downloads/Yamata-no-Orochi/business_flow/smart_targeting_sampling_flow.go:187).

3. **Medium — sampling and persisted send order are nondeterministic.** `SelectForTag` uses `LIMIT` without `ORDER BY`; PostgreSQL may return different rows and orders as plans/storage change. Since those rows become the immutable selection and `selection_order` drives later processing, identical requests can produce different recipients and delivery order. The repository explicitly documents this as an unresolved TODO. [repository query](/home/amirphl/Downloads/Yamata-no-Orochi/repository/smart_targeting_audience_repository.go:260).

Verification: `business_flow` and `repository` tests passed. `app/scheduler` tests could not run in this sandbox because an `httptest` listener was blocked by permissions.

---

Once again:
Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_capacity\_flow.go](business_flow/smart_targeting_capacity_flow.go) .

---

I found three remaining issues. The prior two fixes are present.

- **High — a tag deactivated after approval can block the entire platform’s ready queue.** Tag availability is checked at approval, but tags can change before scheduled delivery. `ListReadyCampaigns` iterates all ready campaigns and returns an error on the first invalid Smart Targeting selection instead of skipping/quarantining that one campaign. The scheduler then abandons the whole platform poll. [Ready-campaign loop](business_flow/campaign_bot_flow.go:96), [tag validation](business_flow/campaign_bot_flow.go:188). Affected Test campaigns can also retain their active sample reservation until separately resolved.

- **Medium — active Test reservations can be deducted twice from another campaign’s capacity.** The audience query already removes active Test-reserved audience IDs. But the allocation query also includes the approved, unmaterialized Test campaign, and the capacity flow subtracts its full `NumAudience` again. When its reserved members overlap the candidate campaign’s population, the capacity can be understated by up to the Test sample size. [Audience reservation exclusion](repository/smart_targeting_audience_repository.go:83), [allocation source includes approved Smart campaigns](repository/campaign_execution_repository.go:137), [numeric deduction](business_flow/smart_targeting_capacity_flow.go:660), [combining count and deduction](business_flow/smart_targeting_capacity_flow.go:765). The allocation deduction should omit campaigns with active Test reservations; retain them only in the fingerprint.

- **Medium — an approved campaign can be falsely rejected at execution after an allowed capacity refresh.** Capacity refresh is permitted while a campaign is `approved`. The execution lookup selects the latest unexpired calculation based only on static inputs—not its allocation fingerprint—then rejects immediately if that old snapshot’s usable count is below the requested count, before running the authoritative live query. If availability subsequently improves (for example, another campaign is cancelled and releases its reservation), delivery remains blocked until someone recalculates. [Approved refresh permitted](business_flow/smart_targeting_capacity_flow.go:95), [execution lookup](repository/campaign_targeting_capacity_repository.go:73), [early rejection before live query](app/scheduler/smart_targeting_capacity_candidates.go:149).

I did not repeat the already documented audience-data freshness, bundle-exclusion, and approximate-deduction limitations.

Verification: `go test ./business_flow ./repository ./app/scheduler` passed.

---

Once again:
Review the following business flows and the repository functions they call then list logical issues/bugs.
[smart\_targeting\_flow.go](business_flow/smart_targeting_flow.go)

---

Current review: the offset-overflow and auto-select candidate-lock issues are fixed. Remaining issues:

1. **High — replace/auto-select can return another request’s selection.**  
   Both mutations commit, then call `GetSelection` outside the transaction. A second replacement can commit in that gap, so request A can report success while returning request B’s IDs/summary.  
   [replace response](business_flow/smart_targeting_flow.go:388), [auto-select response](business_flow/smart_targeting_flow.go:477)  
   Build the response from the rows/summary inside the committing transaction, or introduce a selection version and read that exact version.

2. **High — list responses are not a consistent selection snapshot.**  
   `ListTags` reads visible rows, selected IDs, and the summary as separate statements. Under normal PostgreSQL read-committed semantics, a concurrent atomic replacement can make `items[].selected`, `selected_tag_ids`, and `summary` refer to different selection versions. `GetSelection` has the same IDs-versus-summary race.  
   [ListTags](business_flow/smart_targeting_flow.go:180), [GetSelection](business_flow/smart_targeting_flow.go:320)  
   Use a read-only repeatable-read transaction or one repository query that returns selection rows plus the aggregate.

3. **Medium — evaluation availability and the actual tag source can disagree.**  
   The flow first counts current score rows to choose default sort/`evaluation_available`, then the repository independently resolves the source again. An evaluation can complete—or the last active scored tag can deactivate—between statements. The result can say `evaluation_available: false` while returning evaluated rows, or choose database order instead of the expected fit-score default. This also remains possible in auto-select because its transaction is read committed and does not lock evaluation/tag state.  
   [availability decision](business_flow/smart_targeting_flow.go:166), [independent source resolution](repository/campaign_selected_tag_repository.go:110)  
   Resolve source/version and list results in one snapshot query, or use repeatable-read isolation for the whole operation.

4. **Medium — nullable tag capacities are silently treated as zero in the selection summary.**  
   Live tags permit `audience_count = NULL`; replacement accepts it. `SUM` ignores nulls and `COALESCE(..., 0)` turns an entirely unknown capacity into `0`, or produces a partial sum without indicating that it is incomplete.  
   [nullable model field](models/tag.go:15), [replacement acceptance](repository/campaign_selected_tag_repository.go:325), [summary query](repository/campaign_selected_tag_repository.go:254)  
   Reject unknown capacities for selection, or expose the raw-capacity summary as nullable/with a completeness flag.

5. **Medium — tag identity and evaluation explanation are fetched then discarded.**  
   The repository selects `tag_name`, persona, and `reason`, but the DTO and mapper omit them. In the live fallback, a tag with no display title is returned only as an ID; callers also cannot see the evaluation explanation described by the API documentation.  
   [repository projection](repository/campaign_selected_tag_repository.go:187), [DTO](app/dto/smart_targeting_dto.go:22), [mapper](business_flow/smart_targeting_flow.go:276), [documented explanation fields](docs/smart-targeting-api.md:43)

`go test ./business_flow ./repository ./app/handlers` passes.

---

Candoo Black Send:
P2 — Execution does not validate the persisted color-eligibility snapshot. allowed_colors is stored, but CurrentForExecution only matches platform, tags, grades, and version. If an admin changes a line’s provider after capacity calculation, scheduler selection uses the new provider rule against a capacity count made with the old one—leading to a late execution failure or stale capacity use. [capacity lookup (line 73)](/home/amirphl/Downloads/Yamata-no-Orochi/repository/campaign_targeting_capacity_repository.go:73)