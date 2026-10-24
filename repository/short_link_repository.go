package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"gorm.io/gorm"
)

// ShortLinkRepositoryImpl implements ShortLinkRepository
type ShortLinkRepositoryImpl struct {
	*BaseRepository[models.ShortLink, models.ShortLinkFilter]
}

func NewShortLinkRepository(db *gorm.DB) ShortLinkRepository {
	return &ShortLinkRepositoryImpl{BaseRepository: NewBaseRepository[models.ShortLink, models.ShortLinkFilter](db)}
}

// ShortLinkWithClick is a projection row combining short link columns with click context fields
type ShortLinkWithClick struct {
	ID             uint      `gorm:"column:id"`
	UID            string    `gorm:"column:uid"`
	CampaignID     *uint     `gorm:"column:campaign_id"`
	ClientID       *uint     `gorm:"column:client_id"`
	ScenarioID     *uint     `gorm:"column:scenario_id"`
	ScenarioName   *string   `gorm:"column:scenario_name"`
	PhoneNumber    *string   `gorm:"column:phone_number"`
	LongLink       string    `gorm:"column:long_link"`
	ShortLink      string    `gorm:"column:short_link"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	ClickUserAgent *string   `gorm:"column:click_user_agent"`
	ClickIP        *string   `gorm:"column:click_ip"`
}

// Override SaveBatch with a fast path using PostgreSQL COPY for large batches
func (r *ShortLinkRepositoryImpl) SaveBatch(ctx context.Context, entities []*models.ShortLink) error {
	if len(entities) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, entity := range entities {
		if entity.CreatedAt.IsZero() {
			entity.CreatedAt = now
		}
		if entity.UpdatedAt.IsZero() {
			entity.UpdatedAt = now
		}
	}

	// COPY owns its database/sql transaction. If a caller has already opened a
	// GORM transaction, use its connection instead of creating a second,
	// independent transaction that could commit even if the caller rolls back.
	if _, inTransaction := ctx.Value(TxContextKey).(*gorm.DB); inTransaction {
		return r.BaseRepository.SaveBatch(ctx, entities)
	}

	// Try COPY via database/sql using lib/pq for maximum throughput.
	sqlDB, err := r.DB.DB()
	if err == nil && sqlDB != nil {
		if err := r.copyInShortLinks(ctx, sqlDB, entities); err == nil {
			return nil
		}
		// On COPY error, fall back to GORM batch insert
	}

	// Fallback: use GORM batching
	return r.BaseRepository.SaveBatch(ctx, entities)
}

func (r *ShortLinkRepositoryImpl) copyInShortLinks(ctx context.Context, sqlDB *sql.DB, entities []*models.ShortLink) (err error) {
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	stmt, err := tx.Prepare(pq.CopyIn(
		"short_links",
		"uid",
		"campaign_id",
		"client_id",
		"scenario_id",
		"scenario_name",
		"phone_number",
		"long_link",
		"short_link",
		"is_test",
		"allocation_key",
		"allocation_position",
		"created_at",
		"updated_at",
	))
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entities {
		var campaignID, clientID interface{}
		var scenarioID interface{}
		var scenarioName interface{}
		var phone interface{}
		var allocationKey interface{}
		var allocationPosition interface{}
		if e.CampaignID != nil {
			campaignID = *e.CampaignID
		}
		if e.ClientID != nil {
			clientID = *e.ClientID
		}
		if e.ScenarioID != nil {
			scenarioID = *e.ScenarioID
		}
		if e.ScenarioName != nil && strings.TrimSpace(*e.ScenarioName) != "" {
			scenarioName = *e.ScenarioName
		}
		if e.PhoneNumber != nil && strings.TrimSpace(*e.PhoneNumber) != "" {
			phone = *e.PhoneNumber
		}
		if e.AllocationKey != nil && strings.TrimSpace(*e.AllocationKey) != "" {
			allocationKey = *e.AllocationKey
		}
		if e.AllocationPosition != nil {
			allocationPosition = *e.AllocationPosition
		}

		_, err = stmt.Exec(
			e.UID,
			campaignID,
			clientID,
			scenarioID,
			scenarioName,
			phone,
			e.LongLink,
			e.ShortLink,
			e.IsTest,
			allocationKey,
			allocationPosition,
			e.CreatedAt,
			e.UpdatedAt,
		)
		if err != nil {
			return err
		}
	}

	// flush COPY
	_, err = stmt.Exec()
	if err != nil {
		return err
	}
	return nil
}

func (r *ShortLinkRepositoryImpl) ByID(ctx context.Context, id uint) (*models.ShortLink, error) {
	db := r.getDB(ctx)
	var row models.ShortLink
	if err := db.Last(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *ShortLinkRepositoryImpl) ByUID(ctx context.Context, uid string) (*models.ShortLink, error) {
	filter := models.ShortLinkFilter{UID: &uid}
	rows, err := r.ByFilter(ctx, filter, "id DESC", 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *ShortLinkRepositoryImpl) ByUIDs(ctx context.Context, uids []string) ([]*models.ShortLink, error) {
	if len(uids) == 0 {
		return []*models.ShortLink{}, nil
	}
	const chunkSize = 5000
	rows := make([]*models.ShortLink, 0, len(uids))
	db := r.getDB(ctx)
	for start := 0; start < len(uids); start += chunkSize {
		end := min(start+chunkSize, len(uids))
		var chunk []*models.ShortLink
		if err := db.Where("uid IN ?", uids[start:end]).Order("id ASC").Find(&chunk).Error; err != nil {
			return nil, err
		}
		rows = append(rows, chunk...)
	}
	return rows, nil
}

// ByAllocationKey returns one immutable bulk-allocation attempt in request
// order.  A retry republishes these rows rather than creating another 200K
// links after an ambiguous client/proxy timeout.
func (r *ShortLinkRepositoryImpl) ByAllocationKey(ctx context.Context, allocationKey string) ([]*models.ShortLink, error) {
	if strings.TrimSpace(allocationKey) == "" {
		return []*models.ShortLink{}, nil
	}
	var rows []*models.ShortLink
	if err := r.getDB(ctx).
		Where("allocation_key = ?", allocationKey).
		Order("allocation_position ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *ShortLinkRepositoryImpl) ListPendingExternalPublication(ctx context.Context, limit int) ([]*models.ShortLink, error) {
	if limit <= 0 {
		limit = 1000
	}
	var rows []*models.ShortLink
	err := r.getDB(ctx).
		Where("external_published_at IS NULL").
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *ShortLinkRepositoryImpl) MarkExternallyPublished(ctx context.Context, uids []string, publishedAt time.Time) error {
	if len(uids) == 0 {
		return nil
	}
	return r.getDB(ctx).
		Model(&models.ShortLink{}).
		Where("uid IN ?", uids).
		Update("external_published_at", publishedAt.UTC()).Error
}

func (r *ShortLinkRepositoryImpl) applyFilter(db *gorm.DB, f models.ShortLinkFilter) *gorm.DB {
	if f.ID != nil {
		db = db.Where("id = ?", *f.ID)
	}
	if f.UID != nil {
		db = db.Where("uid = ?", *f.UID)
	}
	if f.CampaignID != nil {
		db = db.Where("campaign_id = ?", *f.CampaignID)
	}
	if f.ClientID != nil {
		db = db.Where("client_id = ?", *f.ClientID)
	}
	if f.ScenarioID != nil {
		db = db.Where("scenario_id = ?", *f.ScenarioID)
	}
	if f.ScenarioNameLike != nil {
		db = db.Where("scenario_name LIKE ?", *f.ScenarioNameLike)
	}
	if f.PhoneNumber != nil {
		db = db.Where("phone_number = ?", *f.PhoneNumber)
	}
	if f.CreatedAfter != nil {
		db = db.Where("created_at >= ?", *f.CreatedAfter)
	}
	if f.CreatedBefore != nil {
		db = db.Where("created_at < ?", *f.CreatedBefore)
	}
	return db
}

func (r *ShortLinkRepositoryImpl) ByFilter(ctx context.Context, filter models.ShortLinkFilter, orderBy string, limit, offset int) ([]*models.ShortLink, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.ShortLink{}), filter)
	if orderBy != "" {
		query = query.Order(orderBy)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	var rows []*models.ShortLink
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *ShortLinkRepositoryImpl) Count(ctx context.Context, filter models.ShortLinkFilter) (int64, error) {
	db := r.getDB(ctx)
	query := r.applyFilter(db.Model(&models.ShortLink{}), filter)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *ShortLinkRepositoryImpl) Exists(ctx context.Context, filter models.ShortLinkFilter) (bool, error) {
	c, err := r.Count(ctx, filter)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func (r *ShortLinkRepositoryImpl) ListByScenarioWithClicks(ctx context.Context, scenarioID uint, orderBy string) ([]*models.ShortLink, error) {
	db := r.getDB(ctx)
	query := db.Table("short_link_clicks").
		Select(`DISTINCT ON (short_link_id)
			short_link_id AS id,
			uid,
			campaign_id,
			client_id,
			scenario_id,
			scenario_name,
			phone_number,
			long_link,
			short_link,
			short_link_created_at AS created_at,
			short_link_updated_at AS updated_at`).
		Where("scenario_id = ?", scenarioID).
		Order("short_link_id")
	query = applyOrder(query, orderBy, "id ASC")
	var rows []*models.ShortLink
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *ShortLinkRepositoryImpl) ListWithClicksDetailsByScenario(ctx context.Context, scenarioID uint, orderBy string) ([]*ShortLinkWithClick, error) {
	db := r.getDB(ctx)
	q := db.Table("short_link_clicks").
		Select(`short_link_id AS id,
			uid,
			campaign_id,
			client_id,
			scenario_id,
			scenario_name,
			phone_number,
			long_link,
			short_link,
			short_link_created_at AS created_at,
			short_link_updated_at AS updated_at,
			user_agent AS click_user_agent,
			ip AS click_ip`).
		Where("scenario_id = ?", scenarioID)
	q = applyOrder(q, orderBy, "short_link_id ASC, id ASC")
	var out []*ShortLinkWithClick
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ShortLinkRepositoryImpl) ListWithClicksDetailsByScenarioRange(ctx context.Context, scenarioFrom, scenarioTo uint, orderBy string) ([]*ShortLinkWithClick, error) {
	db := r.getDB(ctx)
	q := db.Table("short_link_clicks").
		Select(`short_link_id AS id,
			uid,
			campaign_id,
			client_id,
			scenario_id,
			scenario_name,
			phone_number,
			long_link,
			short_link,
			short_link_created_at AS created_at,
			short_link_updated_at AS updated_at,
			user_agent AS click_user_agent,
			ip AS click_ip`).
		Where("scenario_id >= ? AND scenario_id < ?", scenarioFrom, scenarioTo)
	q = applyOrder(q, orderBy, "short_link_id ASC, id ASC")
	var out []*ShortLinkWithClick
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ShortLinkRepositoryImpl) ListWithClicksDetailsByScenarioNameRegex(ctx context.Context, pattern string, orderBy string) ([]*ShortLinkWithClick, error) {
	db := r.getDB(ctx)
	q := db.Table("short_link_clicks").
		Select(`short_link_id AS id,
			uid,
			campaign_id,
			client_id,
			scenario_id,
			scenario_name,
			phone_number,
			long_link,
			short_link,
			short_link_created_at AS created_at,
			short_link_updated_at AS updated_at,
			user_agent AS click_user_agent,
			ip AS click_ip`).
		Where("scenario_name ~ ?", pattern)
	q = applyOrder(q, orderBy, "scenario_id ASC, short_link_id ASC, id ASC")
	var out []*ShortLinkWithClick
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ShortLinkRepositoryImpl) ListWithClicksDetailsByScenarioNameLike(ctx context.Context, pattern string, orderBy string) ([]*ShortLinkWithClick, error) {
	db := r.getDB(ctx)
	q := db.Table("short_link_clicks").
		Select(`short_link_id AS id,
			uid,
			campaign_id,
			client_id,
			scenario_id,
			scenario_name,
			phone_number,
			long_link,
			short_link,
			short_link_created_at AS created_at,
			short_link_updated_at AS updated_at,
			user_agent AS click_user_agent,
			ip AS click_ip`).
		Where("scenario_name LIKE ?", "%"+pattern+"%")
	q = applyOrder(q, orderBy, "scenario_id ASC, short_link_id ASC, id ASC")
	var out []*ShortLinkWithClick
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// applyOrder applies a caller-provided order or falls back to a safe default
func applyOrder(db *gorm.DB, orderBy, fallback string) *gorm.DB {
	if strings.TrimSpace(orderBy) != "" {
		return db.Order(orderBy)
	}
	return db.Order(fallback)
}

func (r *ShortLinkRepositoryImpl) GetLastScenarioID(ctx context.Context) (uint, error) {
	db := r.getDB(ctx)
	var max sql.NullInt64
	if err := db.Model(&models.ShortLink{}).Select("MAX(scenario_id)").Scan(&max).Error; err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return uint(max.Int64), nil
}

// GetMaxUIDSince returns the highest UID (by numeric base36 order) among
// short links created strictly after the provided timestamp. It orders by
// character length first, then lexicographically, which is correct for fixed-length
// base36 strings used as UIDs.
func (r *ShortLinkRepositoryImpl) GetMaxUIDSince(ctx context.Context, since time.Time) (string, error) {
	db := r.getDB(ctx)
	var uids []string
	q := db.Model(&models.ShortLink{}).
		Where("created_at > ?", since).
		Order("id DESC").
		Limit(1)
	if err := q.Pluck("uid", &uids).Error; err != nil {
		return "", err
	}
	if len(uids) == 0 {
		return "", nil
	}
	return uids[0], nil
}
