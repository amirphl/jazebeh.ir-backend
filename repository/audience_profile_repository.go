package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// AudienceProfileRepositoryImpl implements AudienceProfileRepository
type AudienceProfileRepositoryImpl struct {
	*BaseRepository[models.AudienceProfile, models.AudienceProfileFilter]
}

func NewAudienceProfileRepository(db *gorm.DB) AudienceProfileRepository {
	return &AudienceProfileRepositoryImpl{BaseRepository: NewBaseRepository[models.AudienceProfile, models.AudienceProfileFilter](db)}
}

func (r *AudienceProfileRepositoryImpl) ByID(ctx context.Context, id uint) (*models.AudienceProfile, error) {
	db := r.getDB(ctx)
	var ap models.AudienceProfile
	if err := db.Last(&ap, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ap, nil
}

func (r *AudienceProfileRepositoryImpl) ByIDs(ctx context.Context, ids []int64) ([]*models.AudienceProfile, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var unordered []*models.AudienceProfile
	err := r.getDB(ctx).Model(&models.AudienceProfile{}).
		Select("id", "uid", "phone_number").
		Where("id = ANY(?::bigint[])", pq.Int64Array(ids)).
		Find(&unordered).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*models.AudienceProfile, len(unordered))
	for _, row := range unordered {
		if row != nil {
			byID[int64(row.ID)] = row
		}
	}
	rows := make([]*models.AudienceProfile, 0, len(ids))
	for _, id := range ids {
		if row := byID[id]; row != nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (r *AudienceProfileRepositoryImpl) ByUID(ctx context.Context, uid string) (*models.AudienceProfile, error) {
	rows, err := r.ByFilter(ctx, models.AudienceProfileFilter{UID: &uid}, "", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *AudienceProfileRepositoryImpl) ByUIDs(ctx context.Context, uids []string) ([]*models.AudienceProfile, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	db := r.getDB(ctx)
	rows := make([]*models.AudienceProfile, 0, len(uids))
	if err := audienceProfilesByUIDsQuery(db, uids).Find(&rows).Error; err != nil {
		return nil, err
	}

	return rows, nil
}

func audienceProfilesByUIDsQuery(db *gorm.DB, uids []string) *gorm.DB {
	return db.Model(&models.AudienceProfile{}).
		Select("id", "uid", "phone_number").
		Where("uid = ANY(?::varchar[])", pq.StringArray(uids))
}

// SelectCampaignCandidates returns only the columns the campaign schedulers use
// and applies exclusions in PostgreSQL before LIMIT. Keeping the excluded IDs in
// one typed array parameter avoids PostgreSQL's bind-parameter limit even when a
// selection contains tens of thousands of audience IDs.
func (r *AudienceProfileRepositoryImpl) SelectCampaignCandidates(
	ctx context.Context,
	filter models.AudienceProfileFilter,
	excludeIDs []int64,
	limit int,
) ([]*models.AudienceProfile, error) {
	if limit <= 0 {
		return nil, nil
	}

	db := r.getDB(ctx)
	query := r.campaignCandidatesQuery(db, filter, excludeIDs, limit)

	var rows []*models.AudienceProfile
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to select campaign audience candidates: %w", err)
	}
	return rows, nil
}

func (r *AudienceProfileRepositoryImpl) campaignCandidatesQuery(
	db *gorm.DB,
	filter models.AudienceProfileFilter,
	excludeIDs []int64,
	limit int,
) *gorm.DB {
	query := r.applyFilter(db.Model(&models.AudienceProfile{}), filter).
		Select("id", "uid", "phone_number").
		Where("phone_number IS NOT NULL AND BTRIM(phone_number) <> ''")
	if len(excludeIDs) > 0 {
		query = query.Where(
			`NOT EXISTS (
				SELECT 1
				FROM unnest(?::bigint[]) AS excluded(id)
				WHERE excluded.id = audience_profiles.id
			)`,
			pq.Int64Array(excludeIDs),
		)
	}
	if filter.ExcludeBundleID != nil {
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM bundle_audience_selection_members AS used
			WHERE used.bundle_id = ? AND used.audience_id = audience_profiles.id
		)`, *filter.ExcludeBundleID)
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM campaign_targeting_test_sample_reservations AS reserved
			WHERE reserved.bundle_id = ? AND reserved.audience_id = audience_profiles.id
			  AND reserved.state = 'active'
		)`, *filter.ExcludeBundleID)
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM campaign_targeting_execution_reservations AS reserved
			WHERE reserved.bundle_id = ? AND reserved.audience_id = audience_profiles.id
			  AND reserved.state = 'active'
		)`, *filter.ExcludeBundleID)
	}
	return query.Order("id DESC").Limit(limit)
}

func (r *AudienceProfileRepositoryImpl) applyFilter(db *gorm.DB, f models.AudienceProfileFilter) *gorm.DB {
	if f.ID != nil {
		db = db.Where("id = ?", *f.ID)
	}
	if len(f.IDs) > 0 {
		db = db.Where("id = ANY(?::bigint[])", pq.Int64Array(f.IDs))
	}
	if f.UID != nil {
		db = db.Where("uid = ?", *f.UID)
	}
	if f.PhoneNumber != nil {
		db = db.Where("phone_number = ?", *f.PhoneNumber)
	}
	if f.Tags != nil && len(*f.Tags) > 0 {
		db = db.Where("tags && ?", *f.Tags) // overlap operator for arrays
	}
	if f.Color != nil {
		db = db.Where("color = ?", *f.Color)
	}
	if f.CreatedAfter != nil {
		db = db.Where("created_at >= ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		db = db.Where("created_at < ?", *f.CreatedBefore)
	}
	if ns := f.NormalizedScore; ns != nil {
		switch {
		case ns.LTE != nil && ns.OrGT != nil:
			db = db.Where("normalized_score <= ? OR normalized_score > ?", *ns.LTE, *ns.OrGT)
		case ns.LTE != nil && ns.OrGTE != nil:
			db = db.Where("normalized_score <= ? OR normalized_score >= ?", *ns.LTE, *ns.OrGTE)
		case ns.GT != nil && ns.LTE != nil:
			db = db.Where("normalized_score > ? AND normalized_score <= ?", *ns.GT, *ns.LTE)
		case ns.GTE != nil && ns.LTE != nil:
			db = db.Where("normalized_score >= ? AND normalized_score <= ?", *ns.GTE, *ns.LTE)
		case ns.GT != nil:
			db = db.Where("normalized_score > ?", *ns.GT)
		case ns.GTE != nil:
			db = db.Where("normalized_score >= ?", *ns.GTE)
		case ns.LTE != nil:
			db = db.Where("normalized_score <= ?", *ns.LTE)
		}
	}
	return db
}

func (r *AudienceProfileRepositoryImpl) ByFilter(ctx context.Context, filter models.AudienceProfileFilter, orderBy string, limit, offset int) ([]*models.AudienceProfile, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.AudienceProfile{}), filter)

	if orderBy == "" {
		// Deterministic shuffle before limit/offset without loading all rows.
		query = query.Order("md5(COALESCE(uid, id::text))")
	} else {
		query = query.Order(orderBy)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	var rows []*models.AudienceProfile
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to find audience profiles by filter: %w", err)
	}
	return rows, nil
}

func (r *AudienceProfileRepositoryImpl) Count(ctx context.Context, filter models.AudienceProfileFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.AudienceProfile{}), filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *AudienceProfileRepositoryImpl) Exists(ctx context.Context, filter models.AudienceProfileFilter) (bool, error) {
	count, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
