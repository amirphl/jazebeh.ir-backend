package scheduler

import (
	"context"
	"io"
	"log"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
)

func TestNonSMSSelectorsPushLimitAndExclusionsIntoRepository(t *testing.T) {
	t.Parallel()

	type selector func(context.Context, uint, pq.Int32Array, int64, map[int64]struct{}, *uint, *models.NormalizedScoreConstraint) ([]string, []int64, []string, error)
	logger := log.New(io.Discard, "", 0)

	for _, tt := range []struct {
		name  string
		build func(*stubSMSAudienceProfileRepo) selector
	}{
		{
			name: "bale",
			build: func(repo *stubSMSAudienceProfileRepo) selector {
				return (&BaleCampaignScheduler{audRepo: repo, logger: logger}).selectBaleTagAudiences
			},
		},
		{
			name: "rubika",
			build: func(repo *stubSMSAudienceProfileRepo) selector {
				return (&RubikaCampaignScheduler{audRepo: repo, logger: logger}).selectRubikaTagAudiences
			},
		},
		{
			name: "splus",
			build: func(repo *stubSMSAudienceProfileRepo) selector {
				return (&SplusCampaignScheduler{audRepo: repo, logger: logger}).selectSplusTagAudiences
			},
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			excluded := make(map[int64]struct{}, 50_000)
			for id := int64(1); id <= 50_000; id++ {
				excluded[id] = struct{}{}
			}
			phone := "09120000001"
			calls := 0
			repo := &stubSMSAudienceProfileRepo{
				selectCandidatesFn: func(_ context.Context, filter models.AudienceProfileFilter, excludeIDs []int64, limit int) ([]*models.AudienceProfile, error) {
					calls++
					if filter.Color != nil {
						t.Fatalf("non-SMS selector unexpectedly filters color %q", *filter.Color)
					}
					if limit != 25_000 {
						t.Fatalf("candidate limit = %d, want 25000", limit)
					}
					if len(excludeIDs) != len(excluded) {
						t.Fatalf("exclusion count = %d, want %d", len(excludeIDs), len(excluded))
					}
					return []*models.AudienceProfile{{ID: 60_001, UID: "uid-1", PhoneNumber: &phone}}, nil
				},
			}

			phones, ids, uids, err := tt.build(repo)(context.Background(), 99, pq.Int32Array{10, 20}, 25_000, excluded, nil, nil)
			if err != nil {
				t.Fatalf("select audiences: %v", err)
			}
			if calls != 1 {
				t.Fatalf("candidate query calls = %d, want 1", calls)
			}
			if len(phones) != 1 || phones[0] != phone || len(ids) != 1 || ids[0] != 60_001 || len(uids) != 1 || uids[0] != "uid-1" {
				t.Fatalf("unexpected selection: phones=%v ids=%v uids=%v", phones, ids, uids)
			}
		})
	}
}
