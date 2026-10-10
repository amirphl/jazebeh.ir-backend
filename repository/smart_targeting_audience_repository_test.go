package repository

import (
	"reflect"
	"strings"
	"testing"
)

func TestSmartTargetingPopulationUsesOptionalParameterizedColorFilter(t *testing.T) {
	query := strings.ToLower(smartTargetingPopulationCTE)
	for _, forbidden := range []string{"platform", "'white'", "'pink'", "candidate_stack", "insert into"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("capacity population unexpectedly contains %q:\n%s", forbidden, smartTargetingPopulationCTE)
		}
	}
	for _, required := range []string{"bundle_audience_selection_members", "used.bundle_id", "used.audience_id", "campaign_targeting_test_sample_reservations", "reserved.state = 'active'", "bundle_audience_exclusions", "bundle_exclusion.bundle_id", "bundle_exclusion.audience_id", "percentile_disc", "ap.color = any(?::text[])"} {
		if !strings.Contains(query, required) {
			t.Fatalf("capacity population is missing %q:\n%s", required, smartTargetingPopulationCTE)
		}
	}
	for _, forbidden := range []string{"ap.uid", "ap.phone_number,", "ap.tags,"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("capacity population materializes unused column %q:\n%s", forbidden, smartTargetingPopulationCTE)
		}
	}
	selectionQuery := strings.ToLower(smartTargetingSelectionPopulationCTE)
	for _, required := range []string{"ap.uid", "ap.phone_number", "ap.tags", "ap.normalized_score"} {
		if !strings.Contains(selectionQuery, required) {
			t.Fatalf("execution selection population is missing %q:\n%s", required, smartTargetingSelectionPopulationCTE)
		}
	}
}

func TestSmartTargetingPopulationArgsControlOptionalEligibilityFilters(t *testing.T) {
	args := smartTargetingPopulationArgs(SmartTargetingAudienceQuery{TagIDs: []int64{7}, BundleID: 3})
	if disabled, ok := args[1].(bool); !ok || !disabled {
		t.Fatalf("empty allowed colors produced disabled=%#v, want true", args[1])
	}
	if disabled, ok := args[7].(bool); !ok || !disabled {
		t.Fatalf("default Bundle exclusions produced disabled=%#v, want true", args[7])
	}

	args = smartTargetingPopulationArgs(SmartTargetingAudienceQuery{
		TagIDs: []int64{7}, BundleID: 3, AllowedColors: []string{"white", "pink"}, ApplyBundleAudienceExclusions: true,
	})
	if disabled, ok := args[1].(bool); !ok || disabled {
		t.Fatalf("SMS allowed colors produced disabled=%#v, want false", args[1])
	}
	if disabled, ok := args[7].(bool); !ok || disabled {
		t.Fatalf("enabled Bundle exclusions produced disabled=%#v, want false", args[7])
	}
	if len(args) != 9 || args[3] != uint(3) || args[4] != uint(3) || args[5] != uint(0) || args[6] != uint(0) || args[7] != false || args[8] != uint(3) {
		t.Fatalf("population arguments = %#v, want colors and Bundle eligibility inputs", args)
	}
}

func TestSmartTargetingAllClassesRequiresCanonicalDistinctSet(t *testing.T) {
	if !smartTargetingAllClasses([]string{"A", "B", "C"}) {
		t.Fatal("canonical A/B/C selection must use the all-classes query")
	}
	for _, classes := range [][]string{{"A", "A", "A"}, {"C", "B", "A"}, {"A", "B"}} {
		if smartTargetingAllClasses(classes) {
			t.Fatalf("invalid or non-canonical classes %v used the all-classes query", classes)
		}
	}
}

func TestSmartTargetingClassifiedPopulationCalculatesBothBoundsWithOneAggregate(t *testing.T) {
	query := strings.ToLower(smartTargetingClassifiedPopulationCTE)
	if got := strings.Count(query, "percentile_disc"); got != 1 {
		t.Fatalf("classified population percentile aggregate count = %d, want 1:\n%s", got, query)
	}
	for _, required := range []string{"array[0.33, 0.66]", "percentile_values[1] as p33", "percentile_values[2] as p66"} {
		if !strings.Contains(query, required) {
			t.Fatalf("classified population is missing %q:\n%s", required, query)
		}
	}
}

func TestSmartTargetingScoreBoundsQueryCalculatesEligibleUnionOnce(t *testing.T) {
	query := SmartTargetingAudienceQuery{
		BundleID: 3, ApplyBundleAudienceExclusions: true, TagIDs: []int64{9, 2}, ScoreClasses: []string{"A", "C"}, AllowedColors: []string{"white", "pink"},
	}
	sql, args := smartTargetingScoreBoundsQuery(query)
	lowerSQL := strings.ToLower(sql)
	for _, required := range []string{
		"percentile_disc(array[0.33, 0.66]", "ap.tags && ?::integer[]", "ap.color = any(?::text[])",
		"bundle_audience_selection_members", "used.bundle_id = ?", "ap.normalized_score is not null",
		"campaign_targeting_test_sample_reservations", "reserved.state = 'active'",
		"bundle_audience_exclusions", "bundle_exclusion.bundle_id = ?", "bundle_exclusion.audience_id = ap.id",
	} {
		if !strings.Contains(lowerSQL, required) {
			t.Fatalf("score-bound query is missing %q:\n%s", required, sql)
		}
	}
	if strings.Count(lowerSQL, "percentile_disc") != 1 {
		t.Fatalf("score-bound query recalculates percentile aggregates:\n%s", sql)
	}
	if len(args) != 7 || args[6] != uint(3) {
		t.Fatalf("score-bound arguments = %#v, want tag IDs, colors, and Bundle reservation inputs", args)
	}

	withoutColors, args := smartTargetingScoreBoundsQuery(SmartTargetingAudienceQuery{
		BundleID: 3, TagIDs: []int64{9}, ScoreClasses: []string{"A"},
	})
	if strings.Contains(strings.ToLower(withoutColors), "ap.color") || len(args) != 5 {
		t.Fatalf("unrestricted-color score-bound query = %q with %d args", withoutColors, len(args))
	}
	if strings.Contains(strings.ToLower(withoutColors), "bundle_audience_exclusions") {
		t.Fatalf("default score-bound query unexpectedly applies Bundle exclusions:\n%s", withoutColors)
	}
}

func TestSmartTargetingPerTagSelectionUsesNoOrdering(t *testing.T) {
	p33, p66 := 24.0, 29.6
	query := SmartTargetingAudienceQuery{
		BundleID: 3, ApplyBundleAudienceExclusions: true, TagIDs: []int64{9, 2}, ScoreClasses: []string{"A", "C"},
	}
	sql, args, err := smartTargetingPerTagSelectionQuery(query, &SmartTargetingScoreBounds{P33: &p33, P66: &p66}, 9, []int64{101, 102}, 600, true)
	if err != nil {
		t.Fatalf("build per-tag selection query: %v", err)
	}
	lowerSQL := strings.ToLower(sql)
	for _, required := range []string{
		"select ap.id", "from audience_profiles as ap", "ap.tags @> array[?]::integer[]", "bundle_audience_selection_members",
		"campaign_targeting_test_sample_reservations", "reserved.state = 'active'",
		"bundle_audience_exclusions", "bundle_exclusion.bundle_id = ?", "bundle_exclusion.audience_id = ap.id",
		"unnest(?::bigint[])", "ap.normalized_score <= ?::double precision or ap.normalized_score > ?::double precision", "limit ?",
	} {
		if !strings.Contains(lowerSQL, required) {
			t.Fatalf("per-tag selection query is missing %q:\n%s", required, sql)
		}
	}
	for _, forbidden := range []string{"order by", "random()", "hashint8extended", "= any(tags)", "candidate_population", "classified", "ap.uid"} {
		if strings.Contains(lowerSQL, forbidden) {
			t.Fatalf("per-tag ID query unexpectedly contains %q:\n%s", forbidden, sql)
		}
	}
	if len(args) != 10 || args[0] != int64(9) || args[1] != uint(3) || args[2] != uint(3) {
		t.Fatalf("per-tag arguments = %#v, want tag, Bundle allocations/reservations, exclusions, two bounds, and limit", args)
	}

	fullSQL, _, err := smartTargetingPerTagSelectionQuery(query, &SmartTargetingScoreBounds{P33: &p33, P66: &p66}, 9, nil, 600, false)
	if err != nil {
		t.Fatalf("build full per-tag selection query: %v", err)
	}
	for _, column := range []string{"ap.uid", "ap.phone_number", "ap.tags", "ap.normalized_score"} {
		if !strings.Contains(strings.ToLower(fullSQL), column) {
			t.Fatalf("final scheduler query is missing %q:\n%s", column, fullSQL)
		}
	}
	if strings.Contains(strings.ToLower(fullSQL), "order by") {
		t.Fatalf("final per-tag scheduler query unexpectedly imposes ordering:\n%s", fullSQL)
	}
	if _, _, err := smartTargetingPerTagSelectionQuery(query, &SmartTargetingScoreBounds{P33: &p33, P66: &p66}, 77, nil, 600, true); err == nil {
		t.Fatal("per-tag selection accepted a tag outside the selected set")
	}
	query.ApplyBundleAudienceExclusions = false
	withoutBundleExclusions, args, err := smartTargetingPerTagSelectionQuery(query, &SmartTargetingScoreBounds{P33: &p33, P66: &p66}, 9, nil, 600, true)
	if err != nil {
		t.Fatalf("build default per-tag selection query: %v", err)
	}
	if strings.Contains(strings.ToLower(withoutBundleExclusions), "bundle_audience_exclusions") || len(args) != 9 {
		t.Fatalf("default per-tag query unexpectedly applies Bundle exclusions:\n%s\nargs=%#v", withoutBundleExclusions, args)
	}
}

func TestSmartTargetingPerTagSelectionPreservesAllClassColorEligibility(t *testing.T) {
	query := SmartTargetingAudienceQuery{
		BundleID:                      3,
		ApplyBundleAudienceExclusions: true,
		TagIDs:                        []int64{9},
		ScoreClasses:                  []string{"A", "B", "C"},
		AllowedColors:                 []string{"white", "pink"},
	}
	sql, args, err := smartTargetingPerTagSelectionQuery(query, nil, 9, []int64{101}, 600, true)
	if err != nil {
		t.Fatalf("build all-class per-tag selection query: %v", err)
	}
	lowerSQL := strings.ToLower(sql)
	for _, required := range []string{
		"ap.tags @> array[?]::integer[]",
		"ap.color = any(?::text[])",
		"bundle_audience_selection_members",
		"campaign_targeting_test_sample_reservations",
		"bundle_audience_exclusions",
		"unnest(?::bigint[])",
		"limit ?",
	} {
		if !strings.Contains(lowerSQL, required) {
			t.Fatalf("all-class per-tag query is missing %q:\n%s", required, sql)
		}
	}
	for _, forbidden := range []string{"normalized_score", "order by", "random()"} {
		if strings.Contains(lowerSQL, forbidden) {
			t.Fatalf("all-class per-tag ID query unexpectedly contains %q:\n%s", forbidden, sql)
		}
	}
	if len(args) != 9 || args[0] != int64(9) || args[2] != uint(3) || args[3] != uint(3) || args[6] != uint(3) {
		t.Fatalf("all-class per-tag arguments = %#v", args)
	}
}

func TestSmartTargetingAudienceQueriesCanKeepOwnActiveTestReservation(t *testing.T) {
	query := SmartTargetingAudienceQuery{
		BundleID: 3, ExcludeActiveTestReservationCampaignID: 17,
		TagIDs: []int64{9}, ScoreClasses: []string{"A", "B", "C"},
	}
	args := smartTargetingPopulationArgs(query)
	if args[5] != uint(17) || args[6] != uint(17) {
		t.Fatalf("population reservation scope = %#v, want campaign 17 twice", args)
	}

	_, args = smartTargetingScoreBoundsQuery(query)
	if args[3] != uint(17) || args[4] != uint(17) {
		t.Fatalf("score-bound reservation scope = %#v, want campaign 17 twice", args)
	}

	_, args, err := smartTargetingPerTagSelectionQuery(query, nil, 9, nil, 1, true)
	if err != nil {
		t.Fatalf("build scoped per-tag query: %v", err)
	}
	if args[3] != uint(17) || args[4] != uint(17) {
		t.Fatalf("per-tag reservation scope = %#v, want campaign 17 twice", args)
	}
}

func TestSmartTargetingPerTagScorePredicatesPreserveClassBoundaries(t *testing.T) {
	p33, p66 := 24.0, 29.6
	bounds := &SmartTargetingScoreBounds{P33: &p33, P66: &p66}
	tests := []struct {
		classes  []string
		wantSQL  string
		wantArgs []any
	}{
		{[]string{"A"}, "normalized_score >", []any{p66}},
		{[]string{"B"}, "normalized_score >", []any{p33, p66}},
		{[]string{"C"}, "normalized_score <=", []any{p33}},
		{[]string{"A", "B"}, "normalized_score >", []any{p33}},
		{[]string{"A", "C"}, "normalized_score <=", []any{p33, p66}},
		{[]string{"B", "C"}, "normalized_score <=", []any{p66}},
		{[]string{"A", "B", "C"}, "", nil},
	}
	for _, tt := range tests {
		sql, args, err := smartTargetingPerTagScorePredicate(tt.classes, bounds)
		if err != nil {
			t.Fatalf("classes %v failed: %v", tt.classes, err)
		}
		if !strings.Contains(sql, tt.wantSQL) || !reflect.DeepEqual(args, tt.wantArgs) {
			t.Fatalf("classes %v predicate = %q %#v, want fragment %q args %#v", tt.classes, sql, args, tt.wantSQL, tt.wantArgs)
		}
	}

	emptyBounds := &SmartTargetingScoreBounds{}
	if sql, args, err := smartTargetingPerTagScorePredicate([]string{"A"}, emptyBounds); err != nil || !strings.Contains(sql, "AND FALSE") || len(args) != 0 {
		t.Fatalf("empty population predicate = %q %#v, %v; want AND FALSE", sql, args, err)
	}
	if _, _, err := smartTargetingPerTagScorePredicate([]string{"A"}, nil); err == nil {
		t.Fatal("restricted classes without score bounds must fail closed")
	}
}
