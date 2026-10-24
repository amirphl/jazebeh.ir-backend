package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/utils"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSmartTargetingExecutionReservationUnavailable          = errors.New("smart targeting execution reservation is unavailable")
	ErrSmartTargetingExecutionReservationMissing              = errors.New("smart targeting execution reservation is missing")
	ErrSmartTargetingExecutionReservationConflict             = errors.New("smart targeting execution reservation conflicts with current bundle availability")
	ErrSmartTargetingExecutionReservationInsufficientCapacity = errors.New("smart targeting execution reservation has insufficient eligible capacity")
	ErrSmartTargetingExecutionReservationStale                = errors.New("smart targeting execution reservation is stale")
	ErrSmartTargetingExecutionReservationCorrupt              = errors.New("smart targeting execution reservation is corrupt")
	ErrSmartTargetingExecutionReservationConcurrency          = errors.New("smart targeting execution reservation changed concurrently")
)

// ExecutionReservationRequestSnapshot is persisted verbatim in the immutable
// header, so recovery never depends on mutable campaign JSON for eligibility.
type ExecutionReservationRequestSnapshot struct {
	TagIDs        []int64  `json:"tag_ids"`
	ScoreClasses  []string `json:"score_classes"`
	AllowedColors []string `json:"allowed_colors"`
	Platform      string   `json:"platform"`
}

type CampaignTargetingExecutionReservationSnapshot struct {
	Header  *models.CampaignTargetingExecutionReservationHeader
	Members []models.CampaignTargetingExecutionReservation
}

type CampaignTargetingExecutionReservationRepository interface {
	ReserveForCampaign(context.Context, *models.Campaign, *models.CampaignTargetingExecutionReservationHeader, []models.CampaignTargetingExecutionReservation) error
	ActiveReservedForCampaign(context.Context, uint, int64) (*CampaignTargetingExecutionReservationSnapshot, error)
	ValidateMaterializedForCampaign(context.Context, uint, uint, int64, []int64) error
	HasActiveRowsForCampaign(context.Context, uint) (bool, error)
	Materialize(context.Context, uint, int64) error
	ReleaseForCampaign(context.Context, uint) error
	InvalidateForCampaign(context.Context, uint) error
}

type CampaignTargetingExecutionReservationRepositoryImpl struct{ db *gorm.DB }

// executionReservationAvailabilityQuery validates the exact frozen members
// under the Bundle lock. The paired unnest deliberately retains each member's
// assigned tag: a profile that still has some selected tag but lost its own
// attribution must not be reserved for execution.
const executionReservationAvailabilityQuery = `
SELECT COUNT(*)
FROM unnest(?::bigint[], ?::bigint[]) AS requested(audience_id, assigned_tag_id)
LEFT JOIN audience_profiles AS audience ON audience.id = requested.audience_id
WHERE audience.id IS NULL
   OR audience.phone_number IS NULL OR BTRIM(audience.phone_number) = ''
   OR NOT (audience.tags && ?::integer[])
   OR NOT (audience.tags @> ARRAY[requested.assigned_tag_id]::integer[])
   OR (NOT ?::boolean AND (audience.color IS NULL OR audience.color <> ALL(?::text[])))
   OR EXISTS (SELECT 1 FROM bundle_audience_selection_members AS used WHERE used.bundle_id = ? AND used.audience_id = requested.audience_id)
   OR EXISTS (SELECT 1 FROM bundle_audience_exclusions AS excluded WHERE excluded.bundle_id = ? AND excluded.audience_id = requested.audience_id)
   OR EXISTS (SELECT 1 FROM campaign_targeting_test_sample_reservations AS test_reserved WHERE test_reserved.bundle_id = ? AND test_reserved.audience_id = requested.audience_id AND test_reserved.state = 'active')
   OR EXISTS (SELECT 1 FROM campaign_targeting_execution_reservations AS execution_reserved WHERE execution_reserved.bundle_id = ? AND execution_reserved.audience_id = requested.audience_id AND execution_reserved.state = 'active')`

func NewCampaignTargetingExecutionReservationRepository(db *gorm.DB) CampaignTargetingExecutionReservationRepository {
	return &CampaignTargetingExecutionReservationRepositoryImpl{db: db}
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) getDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(TxContextKey).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

func validExecutionReservationHeader(h *models.CampaignTargetingExecutionReservationHeader, c *models.Campaign, expected int64) bool {
	return h != nil && c != nil && c.BundleID != nil && h.CampaignID == c.ID && h.BundleID == *c.BundleID &&
		h.Phase == models.CampaignPhaseExecution && h.ReservationVersion == models.SmartTargetingExecutionReservationSchemaVersion &&
		h.RequestedAudienceCount == expected && h.CandidateGeneration == models.SmartTargetingCapacityAlgorithmVersion &&
		h.SelectionInputVersion == models.SmartTargetingExecutionReservationSelectionInputVersion &&
		h.AllocationFingerprintVersion == models.SmartTargetingExecutionReservationAllocationFingerprintVersion &&
		len(h.SelectionInputHash) == 64 && len(h.AllocationFingerprint) == 64 && len(h.RequestSnapshot) > 0
}

func validateExecutionReservationRows(c *models.Campaign, h *models.CampaignTargetingExecutionReservationHeader, rows []models.CampaignTargetingExecutionReservation) error {
	if c == nil || c.BundleID == nil || *c.BundleID == 0 || !validExecutionReservationHeader(h, c, int64(len(rows))) || len(rows) == 0 {
		return ErrSmartTargetingExecutionReservationUnavailable
	}
	seen := make(map[int64]struct{}, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.CampaignID != c.ID || row.BundleID != *c.BundleID || row.AudienceID <= 0 || row.AssignedTagID == 0 || row.SelectionOrder != int64(i) {
			return ErrSmartTargetingExecutionReservationUnavailable
		}
		if _, duplicate := seen[row.AudienceID]; duplicate {
			return ErrSmartTargetingExecutionReservationUnavailable
		}
		seen[row.AudienceID] = struct{}{}
	}
	return nil
}

func decodeExecutionReservationSnapshot(h *models.CampaignTargetingExecutionReservationHeader) (*ExecutionReservationRequestSnapshot, error) {
	if h == nil {
		return nil, ErrSmartTargetingExecutionReservationMissing
	}
	var snapshot ExecutionReservationRequestSnapshot
	if err := json.Unmarshal(h.RequestSnapshot, &snapshot); err != nil || len(snapshot.TagIDs) == 0 || len(snapshot.ScoreClasses) == 0 {
		return nil, ErrSmartTargetingExecutionReservationCorrupt
	}
	return &snapshot, nil
}

// ReserveForCampaign runs only in the short finalization transaction. Candidate
// discovery occurs before the Bundle lock; this method then atomically checks
// all direct mutable constraints/conflicts and writes header plus members.
func (r *CampaignTargetingExecutionReservationRepositoryImpl) ReserveForCampaign(ctx context.Context, c *models.Campaign, h *models.CampaignTargetingExecutionReservationHeader, rows []models.CampaignTargetingExecutionReservation) error {
	if err := validateExecutionReservationRows(c, h, rows); err != nil {
		return err
	}
	db := r.getDB(ctx)
	var existing models.CampaignTargetingExecutionReservationHeader
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("campaign_id = ?", c.ID).First(&existing).Error
	if err == nil {
		if existing.State != "active" {
			return ErrSmartTargetingExecutionReservationStale
		}
		if existing.BundleID != h.BundleID || existing.Phase != h.Phase || existing.ReservationVersion != h.ReservationVersion || existing.RequestedAudienceCount != h.RequestedAudienceCount || existing.SelectionInputVersion != h.SelectionInputVersion || existing.SelectionInputHash != h.SelectionInputHash || existing.AllocationFingerprintVersion != h.AllocationFingerprintVersion || existing.AllocationFingerprint != h.AllocationFingerprint {
			return ErrSmartTargetingExecutionReservationConcurrency
		}
		snapshot, readErr := r.activeSnapshot(ctx, &existing, int64(len(rows)))
		if readErr != nil {
			return readErr
		}
		for i := range rows {
			if snapshot.Members[i].AudienceID != rows[i].AudienceID || snapshot.Members[i].AssignedTagID != rows[i].AssignedTagID {
				return ErrSmartTargetingExecutionReservationConcurrency
			}
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	snapshot, err := decodeExecutionReservationSnapshot(h)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(rows))
	assignedTagIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.AudienceID)
		assignedTagIDs = append(assignedTagIDs, int64(row.AssignedTagID))
	}
	if err := lockAndValidateExecutionReservationScores(ctx, db, rows); err != nil {
		return err
	}
	var conflicts int64
	if err := db.Raw(executionReservationAvailabilityQuery,
		pq.Int64Array(ids), pq.Int64Array(assignedTagIDs), pq.Int64Array(snapshot.TagIDs), len(snapshot.AllowedColors) == 0, pq.StringArray(snapshot.AllowedColors),
		*c.BundleID, *c.BundleID, *c.BundleID, *c.BundleID).Scan(&conflicts).Error; err != nil {
		return err
	}
	if conflicts != 0 {
		return ErrSmartTargetingExecutionReservationConflict
	}
	h.State, h.CreatedAt = "active", utils.UTCNow()
	if err := db.Create(h).Error; err != nil {
		return executionReservationInsertError(err, true)
	}
	for i := range rows {
		rows[i].HeaderID, rows[i].State, rows[i].CreatedAt = h.ID, "active", h.CreatedAt
	}
	if err := db.CreateInBatches(&rows, 1000).Error; err != nil {
		return executionReservationInsertError(err, false)
	}
	return nil
}

// Lock the concrete profile rows before checking their remaining eligibility.
// Candidate ranking already applies percentile score classes outside the
// Bundle critical section; preserving the selected score snapshot here stops a
// profile score mutation from changing that eligibility between discovery and
// the atomic reservation write without re-running the full population scan.
// TODO(smart-targeting-score-bounds): Exact member-score equality is not
// sufficient for restricted A/B/C selections: changes to other eligible
// profiles can shift p33/p66 and reclassify an unchanged member. Persist and
// validate the original bounds, or re-evaluate every proposed member against
// current bounds while holding the Bundle lock.
func lockAndValidateExecutionReservationScores(ctx context.Context, db *gorm.DB, reservations []models.CampaignTargetingExecutionReservation) error {
	ids := make([]int64, 0, len(reservations))
	planned := make(map[int64]*float64, len(reservations))
	for _, reservation := range reservations {
		ids = append(ids, reservation.AudienceID)
		planned[reservation.AudienceID] = reservation.AudienceScore
	}
	type scoreRow struct {
		ID    int64    `gorm:"column:id"`
		Score *float64 `gorm:"column:normalized_score"`
	}
	var current []scoreRow
	if err := db.WithContext(ctx).Raw(`SELECT id, normalized_score FROM audience_profiles WHERE id = ANY(?::bigint[]) FOR UPDATE`, pq.Int64Array(ids)).Scan(&current).Error; err != nil {
		return err
	}
	if len(current) != len(reservations) {
		return ErrSmartTargetingExecutionReservationConflict
	}
	for _, row := range current {
		expected, exists := planned[row.ID]
		if !exists || !sameExecutionReservationScore(expected, row.Score) {
			return ErrSmartTargetingExecutionReservationConflict
		}
		delete(planned, row.ID)
	}
	if len(planned) != 0 {
		return ErrSmartTargetingExecutionReservationConflict
	}
	return nil
}

func sameExecutionReservationScore(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Float64bits(*left) == math.Float64bits(*right)
}

func executionReservationInsertError(err error, header bool) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && string(pgErr.Code) == "23505" {
		if header {
			return fmt.Errorf("create execution reservation header: %w", ErrSmartTargetingExecutionReservationConcurrency)
		}
		return fmt.Errorf("create execution reservation members: %w", ErrSmartTargetingExecutionReservationConflict)
	}
	if header {
		return fmt.Errorf("create execution reservation header: %w", err)
	}
	return fmt.Errorf("create execution reservation members: %w", err)
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) activeSnapshot(ctx context.Context, h *models.CampaignTargetingExecutionReservationHeader, expected int64) (*CampaignTargetingExecutionReservationSnapshot, error) {
	if h == nil || h.State != "active" || h.Phase != models.CampaignPhaseExecution || h.ReservationVersion != models.SmartTargetingExecutionReservationSchemaVersion || h.RequestedAudienceCount != expected || h.SelectionInputVersion != models.SmartTargetingExecutionReservationSelectionInputVersion || h.AllocationFingerprintVersion != models.SmartTargetingExecutionReservationAllocationFingerprintVersion {
		return nil, ErrSmartTargetingExecutionReservationCorrupt
	}
	if _, err := decodeExecutionReservationSnapshot(h); err != nil {
		return nil, err
	}
	var rows []models.CampaignTargetingExecutionReservation
	if err := r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("campaign_id = ? AND header_id = ? AND state = 'active'", h.CampaignID, h.ID).Order("selection_order ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if int64(len(rows)) != expected {
		return nil, ErrSmartTargetingExecutionReservationCorrupt
	}
	seen := make(map[int64]struct{}, len(rows))
	for i, row := range rows {
		if row.BundleID != h.BundleID || row.AudienceID <= 0 || row.AssignedTagID == 0 || row.SelectionOrder != int64(i) || row.State != "active" {
			return nil, ErrSmartTargetingExecutionReservationCorrupt
		}
		if _, duplicate := seen[row.AudienceID]; duplicate {
			return nil, ErrSmartTargetingExecutionReservationCorrupt
		}
		seen[row.AudienceID] = struct{}{}
	}
	return &CampaignTargetingExecutionReservationSnapshot{Header: h, Members: rows}, nil
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) ActiveReservedForCampaign(ctx context.Context, campaignID uint, expected int64) (*CampaignTargetingExecutionReservationSnapshot, error) {
	if campaignID == 0 || expected <= 0 {
		return nil, ErrSmartTargetingExecutionReservationUnavailable
	}
	if _, err := transactionForLock(ctx); err != nil {
		return nil, err
	}
	var h models.CampaignTargetingExecutionReservationHeader
	if err := r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("campaign_id = ?", campaignID).First(&h).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSmartTargetingExecutionReservationMissing
		}
		return nil, err
	}
	if h.State != "active" {
		return nil, ErrSmartTargetingExecutionReservationStale
	}
	return r.activeSnapshot(ctx, &h, expected)
}

// ValidateMaterializedForCampaign protects scheduler retries. A previously
// created Bundle selection is trusted for a modern campaign only when its
// ordered members exactly equal the corresponding materialized execution
// reservation; otherwise a direct/partial write could bypass the frozen set.
func (r *CampaignTargetingExecutionReservationRepositoryImpl) ValidateMaterializedForCampaign(ctx context.Context, campaignID, bundleID uint, expected int64, audienceIDs []int64) error {
	if campaignID == 0 || bundleID == 0 || expected <= 0 || int64(len(audienceIDs)) != expected {
		return ErrSmartTargetingExecutionReservationCorrupt
	}
	if _, err := transactionForLock(ctx); err != nil {
		return err
	}
	var h models.CampaignTargetingExecutionReservationHeader
	if err := r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("campaign_id = ?", campaignID).First(&h).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSmartTargetingExecutionReservationMissing
		}
		return err
	}
	if h.State != "materialized" || h.BundleID != bundleID || h.Phase != models.CampaignPhaseExecution || h.ReservationVersion != models.SmartTargetingExecutionReservationSchemaVersion || h.RequestedAudienceCount != expected || h.SelectionInputVersion != models.SmartTargetingExecutionReservationSelectionInputVersion || h.AllocationFingerprintVersion != models.SmartTargetingExecutionReservationAllocationFingerprintVersion {
		return ErrSmartTargetingExecutionReservationCorrupt
	}
	if _, err := decodeExecutionReservationSnapshot(&h); err != nil {
		return err
	}
	var rows []models.CampaignTargetingExecutionReservation
	if err := r.getDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("campaign_id = ? AND header_id = ? AND state = 'materialized'", campaignID, h.ID).
		Order("selection_order ASC").Find(&rows).Error; err != nil {
		return err
	}
	if int64(len(rows)) != expected {
		return ErrSmartTargetingExecutionReservationCorrupt
	}
	seen := make(map[int64]struct{}, len(rows))
	for index, row := range rows {
		if row.BundleID != bundleID || row.AudienceID <= 0 || row.AssignedTagID == 0 || row.SelectionOrder != int64(index) || row.AudienceID != audienceIDs[index] {
			return ErrSmartTargetingExecutionReservationCorrupt
		}
		if _, duplicate := seen[row.AudienceID]; duplicate {
			return ErrSmartTargetingExecutionReservationCorrupt
		}
		seen[row.AudienceID] = struct{}{}
	}
	return nil
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) HasActiveRowsForCampaign(ctx context.Context, campaignID uint) (bool, error) {
	var count int64
	err := r.getDB(ctx).Model(&models.CampaignTargetingExecutionReservation{}).Where("campaign_id = ? AND state = 'active'", campaignID).Count(&count).Error
	return count > 0, err
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) Materialize(ctx context.Context, campaignID uint, expected int64) error {
	snapshot, err := r.ActiveReservedForCampaign(ctx, campaignID, expected)
	if err != nil {
		return err
	}
	now, db := utils.UTCNow(), r.getDB(ctx)
	if result := db.Model(&models.CampaignTargetingExecutionReservation{}).Where("campaign_id = ? AND header_id = ? AND state = 'active'", campaignID, snapshot.Header.ID).Updates(map[string]any{"state": "materialized", "materialized_at": now}); result.Error != nil {
		return result.Error
	} else if result.RowsAffected != expected {
		return ErrSmartTargetingExecutionReservationConcurrency
	}
	if result := db.Model(&models.CampaignTargetingExecutionReservationHeader{}).Where("id = ? AND state = 'active'", snapshot.Header.ID).Updates(map[string]any{"state": "materialized", "materialized_at": now}); result.Error != nil {
		return result.Error
	} else if result.RowsAffected != 1 {
		return ErrSmartTargetingExecutionReservationConcurrency
	}
	return nil
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) ReleaseForCampaign(ctx context.Context, campaignID uint) error {
	return r.transitionForCampaign(ctx, campaignID, "released")
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) InvalidateForCampaign(ctx context.Context, campaignID uint) error {
	return r.transitionForCampaign(ctx, campaignID, "stale")
}

func (r *CampaignTargetingExecutionReservationRepositoryImpl) transitionForCampaign(ctx context.Context, campaignID uint, headerState string) error {
	if campaignID == 0 {
		return nil
	}
	if err := LockCampaignForUpdate(ctx, campaignID); err != nil {
		return err
	}
	db := r.getDB(ctx)
	var bundleIDs []uint
	if err := db.Model(&models.CampaignTargetingExecutionReservationHeader{}).Select("DISTINCT bundle_id").Where("campaign_id = ? AND state = 'active'", campaignID).Order("bundle_id ASC").Scan(&bundleIDs).Error; err != nil {
		return err
	}
	for _, bundleID := range bundleIDs {
		if err := LockBundleForUpdate(ctx, bundleID); err != nil {
			return err
		}
	}
	now := utils.UTCNow()
	if err := db.Model(&models.CampaignTargetingExecutionReservation{}).Where("campaign_id = ? AND state = 'active'", campaignID).Updates(map[string]any{"state": "released", "released_at": now}).Error; err != nil {
		return err
	}
	return db.Model(&models.CampaignTargetingExecutionReservationHeader{}).Where("campaign_id = ? AND state = 'active'", campaignID).Updates(map[string]any{"state": headerState, "released_at": now}).Error
}
