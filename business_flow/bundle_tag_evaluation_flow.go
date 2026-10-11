package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

type BundleTagEvaluationFlow interface {
	RequestBundleTagEvaluation(ctx context.Context, req *dto.RequestBundleTagEvaluationRequest, metadata *ClientMetadata) (*dto.RequestBundleTagEvaluationResponse, error)
	GetBundleTagEvaluationStatus(ctx context.Context, req *dto.GetBundleTagEvaluationStatusRequest, metadata *ClientMetadata) (*dto.GetBundleTagEvaluationStatusResponse, error)
	ListBundleTagScores(ctx context.Context, req *dto.ListBundleTagScoresRequest, metadata *ClientMetadata) (*dto.ListBundleTagScoresResponse, error)
	ExecuteBundleTagEvaluationRun(ctx context.Context, runID int64) error
}

type openAIClientFactory func(cfg config.SmartTagOpenAIConfig) (services.SmartTagOpenAIClient, error)

type BundleTagEvaluationFlowImpl struct {
	bundleRepo         repository.BundleRepository
	customerRepo       repository.CustomerRepository
	tagRepo            repository.TagRepository
	runRepo            repository.BundleTagEvaluationRunRepository
	eventRepo          repository.BundleTagEvaluationEventRepository
	personaAttemptRepo repository.BundleTagPersonaAnalysisAttemptRepository
	batchRepo          repository.BundleTagEvaluationBatchRepository
	batchAttemptRepo   repository.BundleTagEvaluationBatchAttemptRepository
	scoreRepo          repository.BundleTagScoreRepository
	readRepo           repository.BundleTagEvaluationReadRepository
	db                 *gorm.DB
	cfg                config.SmartTagEvaluationConfig
	newClient          openAIClientFactory
	requestPacer       *openAIRequestPacer
}

type BundleTagEvaluationConflictError struct {
	Response *dto.RequestBundleTagEvaluationResponse
}

func (e *BundleTagEvaluationConflictError) Error() string {
	return "bundle tag evaluation already active"
}

type BundleTagEvaluationDailyLimitError struct {
	Limit          int       `json:"limit"`
	SubmittedCount int64     `json:"submitted_count"`
	ResetsAt       time.Time `json:"resets_at"`
}

func (e *BundleTagEvaluationDailyLimitError) Error() string {
	return "customer daily bundle tag evaluation limit reached"
}

type bundleTagScoreResult struct {
	TagID          uint     `json:"tag_id"`
	BundleFitScore *float64 `json:"bundle_fit_score"`
	FitLevel       string   `json:"fit_level"`
	RelationType   string   `json:"relation_type"`
	Reason         string   `json:"reason"`
}

type bundleTagScorePayload struct {
	TagID            uint     `json:"tag_id"`
	BundleFitScore   *float64 `json:"bundle_fit_score"`
	CampaignFitScore *float64 `json:"campaign_fit_score"`
	FitLevel         string   `json:"fit_level"`
	RelationType     string   `json:"relation_type"`
	Reason           string   `json:"reason"`
}

type bundleTagEvaluationConfigurationSnapshot struct {
	Version         int                      `json:"version"`
	PersonaAnalysis snapshotPromptConfig     `json:"persona_analysis"`
	TagScoring      snapshotPromptConfig     `json:"tag_scoring"`
	OpenAI          snapshotOpenAIConfig     `json:"openai"`
	Batching        snapshotBatchingConfig   `json:"batching"`
	Validation      snapshotValidationConfig `json:"validation"`
}

type snapshotPromptConfig struct {
	SystemPrompt *string `json:"system_prompt,omitempty"`
}

type snapshotOpenAIConfig struct {
	BaseURL         *string  `json:"base_url,omitempty"`
	Model           *string  `json:"model,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	Timeout         *string  `json:"timeout,omitempty"`
	MaxRetries      *int     `json:"max_retries,omitempty"`
	HTTPProxy       *string  `json:"http_proxy,omitempty"`
}

type snapshotBatchingConfig struct {
	TagBatchSize       *int `json:"tag_batch_size,omitempty"`
	MaxParallelBatches *int `json:"max_parallel_batches,omitempty"`
}

type openAIRequestPacer struct {
	mu            sync.Mutex
	interval      time.Duration
	nextRequestAt time.Time
	pauseUntil    time.Time
}

type pacedOpenAIClient struct {
	delegate services.SmartTagOpenAIClient
	pacer    *openAIRequestPacer
}

func newOpenAIRequestPacer(maxRequestsPerMinute int) *openAIRequestPacer {
	if maxRequestsPerMinute <= 0 {
		return nil
	}
	interval := time.Minute / time.Duration(maxRequestsPerMinute)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	return &openAIRequestPacer{interval: interval}
}

func (p *openAIRequestPacer) Wait(ctx context.Context) error {
	if p == nil || p.interval <= 0 {
		return nil
	}

	p.mu.Lock()
	now := time.Now()
	requestAt := now
	if p.nextRequestAt.After(requestAt) {
		requestAt = p.nextRequestAt
	}
	p.nextRequestAt = requestAt.Add(p.interval)
	p.mu.Unlock()

	for {
		delay := time.Until(requestAt)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		p.mu.Lock()
		now = time.Now()
		if !p.pauseUntil.After(now) {
			p.mu.Unlock()
			return nil
		}
		requestAt = p.pauseUntil
		if p.nextRequestAt.After(requestAt) {
			requestAt = p.nextRequestAt
		}
		p.nextRequestAt = requestAt.Add(p.interval)
		p.mu.Unlock()
	}
}

func (p *openAIRequestPacer) Pause(delay time.Duration) {
	if p == nil || delay <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	until := time.Now().Add(delay)
	if until.After(p.pauseUntil) {
		p.pauseUntil = until
	}
}

func (c *pacedOpenAIClient) CallResponsesAPI(ctx context.Context, payload map[string]any) (*services.SmartTagOpenAIResult, error) {
	if c == nil || c.delegate == nil {
		return nil, fmt.Errorf("OpenAI client is not configured")
	}
	if err := c.pacer.Wait(ctx); err != nil {
		return nil, err
	}
	result, err := c.delegate.CallResponsesAPI(ctx, payload)
	var httpErr *services.SmartTagOpenAIHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 429 {
		delay := time.Second
		if result != nil && result.RetryAfter > delay {
			delay = result.RetryAfter
		}
		c.pacer.Pause(delay)
	}
	return result, err
}

type snapshotValidationConfig struct {
	RequireExactTagCount *bool `json:"require_exact_tag_count,omitempty"`
	RequireExactTagIDs   *bool `json:"require_exact_tag_ids,omitempty"`
	MaxMissingTagCount   *int  `json:"max_missing_tag_count,omitempty"`
}

func NewBundleTagEvaluationFlow(
	bundleRepo repository.BundleRepository,
	customerRepo repository.CustomerRepository,
	tagRepo repository.TagRepository,
	runRepo repository.BundleTagEvaluationRunRepository,
	eventRepo repository.BundleTagEvaluationEventRepository,
	personaAttemptRepo repository.BundleTagPersonaAnalysisAttemptRepository,
	batchRepo repository.BundleTagEvaluationBatchRepository,
	batchAttemptRepo repository.BundleTagEvaluationBatchAttemptRepository,
	scoreRepo repository.BundleTagScoreRepository,
	readRepo repository.BundleTagEvaluationReadRepository,
	db *gorm.DB,
	cfg config.SmartTagEvaluationConfig,
) BundleTagEvaluationFlow {
	return &BundleTagEvaluationFlowImpl{
		bundleRepo:         bundleRepo,
		customerRepo:       customerRepo,
		tagRepo:            tagRepo,
		runRepo:            runRepo,
		eventRepo:          eventRepo,
		personaAttemptRepo: personaAttemptRepo,
		batchRepo:          batchRepo,
		batchAttemptRepo:   batchAttemptRepo,
		scoreRepo:          scoreRepo,
		readRepo:           readRepo,
		db:                 db,
		cfg:                cfg,
		newClient:          services.NewSmartTagOpenAIClient,
		requestPacer:       newOpenAIRequestPacer(cfg.OpenAI.MaxRequestsPerMinute),
	}
}

func (f *BundleTagEvaluationFlowImpl) RequestBundleTagEvaluation(ctx context.Context, req *dto.RequestBundleTagEvaluationRequest, metadata *ClientMetadata) (*dto.RequestBundleTagEvaluationResponse, error) {
	if req == nil {
		return nil, NewBusinessError("REQUEST_BUNDLE_TAG_EVALUATION_FAILED", "Failed to request bundle tag evaluation", ErrInvalidState)
	}
	if !f.cfg.Enabled || !f.cfg.Scheduler.Enabled {
		return nil, NewBusinessError("SMART_TAG_EVALUATION_DISABLED", "Smart tag evaluation is disabled", ErrInvalidState)
	}

	if _, err := getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("REQUEST_BUNDLE_TAG_EVALUATION_FAILED", "Failed to request bundle tag evaluation", err)
	}

	bundle, err := f.bundleRepo.ByID(ctx, req.BundleID)
	if err != nil {
		return nil, NewBusinessError("REQUEST_BUNDLE_TAG_EVALUATION_FAILED", "Failed to request bundle tag evaluation", err)
	}
	if bundle == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if bundle.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}

	currentStatus, err := f.readRepo.ByBundleID(ctx, bundle.ID)
	if err != nil {
		return nil, NewBusinessError("REQUEST_BUNDLE_TAG_EVALUATION_FAILED", "Failed to request bundle tag evaluation", err)
	}
	if currentStatus != nil && currentStatus.Status == models.BundleTagEvaluationStatusEvaluating && currentStatus.LatestRunID != nil {
		return nil, &BundleTagEvaluationConflictError{
			Response: &dto.RequestBundleTagEvaluationResponse{
				Message:         "Bundle tag evaluation is already active",
				EvaluationRunID: *currentStatus.LatestRunID,
				Status:          currentStatus.Status,
				CreatedAt:       timeOrZero(currentStatus.LatestRunCreatedAt),
			},
		}
	}

	now := time.Now().UTC()
	run := &models.BundleTagEvaluationRun{
		BundleID:                      bundle.ID,
		CustomerID:                    bundle.CustomerID,
		TargetPersonaSnapshot:         bundle.TargetAudiencePersona,
		PersonaAnalysisPromptSnapshot: f.cfg.PersonaAnalysis.SystemPrompt,
		ConfigurationSnapshot:         f.mustMarshalJSON(f.configurationSnapshot()),
		TagBatchSize:                  f.cfg.Batching.TagBatchSize,
		CreatedAt:                     now,
	}

	if err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		if err := lockCustomerEvaluationRequest(txCtx, f.db, bundle.CustomerID); err != nil {
			return err
		}
		if err := lockBundleEvaluationRequest(txCtx, f.db, bundle.ID); err != nil {
			return err
		}

		// Recheck after taking the bundle row lock. This is the authoritative
		// conflict check; the earlier read only avoids waiting in the common case.
		currentStatus, err := f.readRepo.ByBundleID(txCtx, bundle.ID)
		if err != nil {
			return err
		}
		if currentStatus != nil && currentStatus.Status == models.BundleTagEvaluationStatusEvaluating && currentStatus.LatestRunID != nil {
			return &BundleTagEvaluationConflictError{
				Response: &dto.RequestBundleTagEvaluationResponse{
					Message:         "Bundle tag evaluation is already active",
					EvaluationRunID: *currentStatus.LatestRunID,
					Status:          currentStatus.Status,
					CreatedAt:       timeOrZero(currentStatus.LatestRunCreatedAt),
				},
			}
		}

		dayStart, nextDayStart := utcDayBounds(now)
		submittedCount, err := f.runRepo.CountByCustomerIDCreatedBetween(txCtx, bundle.CustomerID, dayStart, nextDayStart)
		if err != nil {
			return err
		}
		if submittedCount >= int64(f.cfg.DailyLimitPerCustomer) {
			return &BundleTagEvaluationDailyLimitError{
				Limit:          f.cfg.DailyLimitPerCustomer,
				SubmittedCount: submittedCount,
				ResetsAt:       nextDayStart,
			}
		}

		if err := f.runRepo.Save(txCtx, run); err != nil {
			return err
		}
		err = f.eventRepo.Save(txCtx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			EventType:       models.BundleTagEvaluationEventCreated,
			Payload:         f.mustMarshalJSON(map[string]any{"message": "evaluation queued"}),
			CreatedAt:       now,
		})
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		if conflictErr, ok := err.(*BundleTagEvaluationConflictError); ok {
			return nil, conflictErr
		}
		if dailyLimitErr, ok := err.(*BundleTagEvaluationDailyLimitError); ok {
			return nil, dailyLimitErr
		}
		return nil, NewBusinessError("REQUEST_BUNDLE_TAG_EVALUATION_FAILED", "Failed to request bundle tag evaluation", err)
	}

	_ = metadata

	return &dto.RequestBundleTagEvaluationResponse{
		Message:         "Bundle tag evaluation requested successfully",
		EvaluationRunID: run.ID,
		Status:          models.BundleTagEvaluationStatusEvaluating,
		CreatedAt:       run.CreatedAt,
	}, nil
}

func (f *BundleTagEvaluationFlowImpl) GetBundleTagEvaluationStatus(ctx context.Context, req *dto.GetBundleTagEvaluationStatusRequest, metadata *ClientMetadata) (*dto.GetBundleTagEvaluationStatusResponse, error) {
	if req == nil {
		return nil, NewBusinessError("GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED", "Failed to get bundle tag evaluation status", ErrInvalidState)
	}
	if _, err := getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED", "Failed to get bundle tag evaluation status", err)
	}

	bundle, err := f.bundleRepo.ByID(ctx, req.BundleID)
	if err != nil {
		return nil, NewBusinessError("GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED", "Failed to get bundle tag evaluation status", err)
	}
	if bundle == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if bundle.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}

	row, err := f.readRepo.ByBundleID(ctx, bundle.ID)
	if err != nil {
		return nil, NewBusinessError("GET_BUNDLE_TAG_EVALUATION_STATUS_FAILED", "Failed to get bundle tag evaluation status", err)
	}
	if row == nil {
		row = &models.CurrentBundleTagEvaluationStatus{
			BundleID:   bundle.ID,
			CustomerID: bundle.CustomerID,
			Status:     models.BundleTagEvaluationStatusNotEvaluated,
		}
	}

	_ = metadata

	return &dto.GetBundleTagEvaluationStatusResponse{
		Message: "Bundle tag evaluation status retrieved successfully",
		Item: &dto.BundleTagEvaluationStatusItem{
			BundleID:              row.BundleID,
			Status:                row.Status,
			LatestRunID:           row.LatestRunID,
			LatestSuccessfulRunID: row.LatestSuccessfulRunID,
			LatestRunCreatedAt:    row.LatestRunCreatedAt,
			LatestCompletedAt:     row.LatestCompletedAt,
			LatestErrorMessage:    row.LatestErrorMessage,
			LatestErrorAt:         row.LatestErrorAt,
		},
	}, nil
}

func (f *BundleTagEvaluationFlowImpl) ListBundleTagScores(ctx context.Context, req *dto.ListBundleTagScoresRequest, metadata *ClientMetadata) (*dto.ListBundleTagScoresResponse, error) {
	if req == nil {
		return nil, NewBusinessError("LIST_BUNDLE_TAG_SCORES_FAILED", "Failed to list bundle tag scores", ErrInvalidState)
	}
	if _, err := getCustomer(ctx, f.customerRepo, req.CustomerID); err != nil {
		return nil, NewBusinessError("LIST_BUNDLE_TAG_SCORES_FAILED", "Failed to list bundle tag scores", err)
	}

	bundle, err := f.bundleRepo.ByID(ctx, req.BundleID)
	if err != nil {
		return nil, NewBusinessError("LIST_BUNDLE_TAG_SCORES_FAILED", "Failed to list bundle tag scores", err)
	}
	if bundle == nil {
		return nil, NewBusinessError("BUNDLE_NOT_FOUND", "Bundle not found", ErrBundleNotFound)
	}
	if bundle.CustomerID != req.CustomerID {
		return nil, NewBusinessError("BUNDLE_ACCESS_DENIED", "Bundle access denied", ErrBundleAccessDenied)
	}

	page := max(1, req.Page)
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	total, err := f.readRepo.CountCurrentScoresByBundleID(ctx, bundle.ID)
	if err != nil {
		return nil, NewBusinessError("LIST_BUNDLE_TAG_SCORES_FAILED", "Failed to list bundle tag scores", err)
	}
	rows, err := f.readRepo.ListCurrentScoresByBundleID(ctx, bundle.ID, limit, offset)
	if err != nil {
		return nil, NewBusinessError("LIST_BUNDLE_TAG_SCORES_FAILED", "Failed to list bundle tag scores", err)
	}

	items := make([]dto.BundleTagScoreItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		items = append(items, dto.BundleTagScoreItem{
			EvaluationRunID:          row.EvaluationRunID,
			TagID:                    row.TagID,
			TagNameSnapshot:          row.TagNameSnapshot,
			TagDisplayTitleSnapshot:  row.TagDisplayTitleSnapshot,
			TagPersonaSnapshot:       row.TagPersonaSnapshot,
			TagAudienceCountSnapshot: row.TagAudienceCountSnapshot,
			BundleFitScore:           row.BundleFitScore,
			FitLevel:                 row.FitLevel,
			RelationType:             row.RelationType,
			Reason:                   row.Reason,
		})
	}

	_ = metadata

	return &dto.ListBundleTagScoresResponse{
		Message: "Bundle tag scores retrieved successfully",
		Items:   items,
		Pagination: dto.PaginationInfo{
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: int((total + int64(limit) - 1) / int64(limit)),
		},
	}, nil
}

func (f *BundleTagEvaluationFlowImpl) ExecuteBundleTagEvaluationRun(ctx context.Context, runID int64) error {
	run, err := f.runRepo.ByID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("bundle tag evaluation run %d not found", runID)
	}

	releaseLock, err := acquireBundleEvaluationLock(ctx, f.db, run.BundleID)
	if err != nil {
		return err
	}
	defer releaseLock()

	status, err := f.readRepo.ByRunID(ctx, run.ID)
	if err != nil {
		return err
	}
	if status != nil && status.RunStatus == "evaluated" {
		return nil
	}

	executionCfg, err := f.executionConfiguration(run)
	if err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}

	latestEvent, err := f.eventRepo.LatestByRunID(ctx, run.ID)
	if err != nil {
		return err
	}
	if latestEvent == nil || latestEvent.EventType == models.BundleTagEvaluationEventCreated {
		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			EventType:       models.BundleTagEvaluationEventStarted,
			Payload:         f.mustMarshalJSON(map[string]any{"message": "evaluation started"}),
			CreatedAt:       time.Now().UTC(),
		}); err != nil {
			return err
		}
	}

	client, err := f.newClient(executionCfg.OpenAI)
	if err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}
	client = &pacedOpenAIClient{delegate: client, pacer: f.requestPacer}

	personaAnalysisText, err := f.ensurePersonaAnalysis(ctx, run, executionCfg, client)
	if err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}

	batches, err := f.ensureBatches(ctx, run)
	if err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}

	if err := f.processBatchesConcurrently(ctx, run, batches, personaAnalysisText, executionCfg, client); err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}

	totalExpected := 0
	for _, batch := range batches {
		if batch != nil {
			totalExpected += batch.TagCount
		}
	}
	totalActual, err := f.scoreRepo.CountByRunID(ctx, run.ID)
	if err != nil {
		return f.failRunUnlessCanceled(ctx, run.ID, err)
	}
	maxMissingTotal := executionCfg.Validation.MaxMissingTagCount * len(batches)
	missingTotal := int64(totalExpected) - totalActual
	if missingTotal < 0 || missingTotal > int64(maxMissingTotal) {
		return f.failRunUnlessCanceled(ctx, run.ID, fmt.Errorf("score count mismatch: expected %d got %d (maximum missing %d)", totalExpected, totalActual, maxMissingTotal))
	}

	completedExists, err := f.eventRepo.ExistsByRunIDAndType(ctx, run.ID, models.BundleTagEvaluationEventCompleted)
	if err != nil {
		return err
	}
	if !completedExists {
		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			EventType:       models.BundleTagEvaluationEventCompleted,
			Payload:         f.mustMarshalJSON(map[string]any{"message": "evaluation completed", "score_count": totalActual, "missing_tag_count": missingTotal}),
			CreatedAt:       time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

type batchProcessingError struct {
	batchNumber int
	err         error
}

func (f *BundleTagEvaluationFlowImpl) processBatchesConcurrently(
	ctx context.Context,
	run *models.BundleTagEvaluationRun,
	batches []*models.BundleTagEvaluationBatch,
	personaAnalysisText string,
	executionCfg config.SmartTagEvaluationConfig,
	client services.SmartTagOpenAIClient,
) error {
	return runBatchesConcurrently(ctx, batches, executionCfg.Batching.MaxParallelBatches, func(workerCtx context.Context, batch *models.BundleTagEvaluationBatch) error {
		return f.processBatch(workerCtx, run, batch, personaAnalysisText, executionCfg, client)
	})
}

func runBatchesConcurrently(
	ctx context.Context,
	batches []*models.BundleTagEvaluationBatch,
	maxParallel int,
	process func(context.Context, *models.BundleTagEvaluationBatch) error,
) error {
	if process == nil {
		return fmt.Errorf("batch processor is not configured")
	}
	pending := make([]*models.BundleTagEvaluationBatch, 0, len(batches))
	for _, batch := range batches {
		if batch != nil {
			pending = append(pending, batch)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	workerCount := max(1, maxParallel)
	if workerCount > len(pending) {
		workerCount = len(pending)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan *models.BundleTagEvaluationBatch, len(pending))
	results := make(chan batchProcessingError, workerCount)
	for _, batch := range pending {
		jobs <- batch
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for batch := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				if err := process(workerCtx, batch); err != nil {
					results <- batchProcessingError{batchNumber: batch.BatchNumber, err: err}
					cancel()
					return
				}
			}
		}()
	}
	workers.Wait()
	close(results)

	var selected *batchProcessingError
	for result := range results {
		if result.err == nil {
			continue
		}
		if selected == nil || (isContextError(selected.err) && !isContextError(result.err)) ||
			(isContextError(selected.err) == isContextError(result.err) && result.batchNumber < selected.batchNumber) {
			copy := result
			selected = &copy
		}
	}
	if selected != nil {
		return fmt.Errorf("batch %d: %w", selected.batchNumber, selected.err)
	}
	return ctx.Err()
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (f *BundleTagEvaluationFlowImpl) ensurePersonaAnalysis(ctx context.Context, run *models.BundleTagEvaluationRun, executionCfg config.SmartTagEvaluationConfig, client services.SmartTagOpenAIClient) (string, error) {
	latest, err := f.personaAttemptRepo.LatestByRunID(ctx, run.ID)
	if err != nil {
		return "", err
	}
	if latest != nil && isSuccessfulAttemptStatus(latest.HTTPStatusCode) {
		if extracted := strings.TrimSpace(derefString(latest.ExtractedResponseText)); extracted != "" {
			return extracted, nil
		}
		if raw := strings.TrimSpace(derefString(latest.RawResponse)); raw != "" {
			extracted, parseErr := extractOpenAIResponseText(raw)
			if parseErr == nil && strings.TrimSpace(extracted) != "" {
				return extracted, nil
			}
		}
	}
	if previousErr := nonRetryableAttemptError(latest); previousErr != nil {
		return "", previousErr
	}

	startedExists, err := f.eventRepo.ExistsByRunIDAndType(ctx, run.ID, models.BundleTagEvaluationEventPersonaAnalysisStarted)
	if err != nil {
		return "", err
	}
	if !startedExists {
		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			EventType:       models.BundleTagEvaluationEventPersonaAnalysisStarted,
			Payload:         f.mustMarshalJSON(map[string]any{"message": "persona analysis started"}),
			CreatedAt:       time.Now().UTC(),
		}); err != nil {
			return "", err
		}
	}

	baseAttempt := 1
	if latest != nil {
		baseAttempt = latest.AttemptNumber + 1
	}
	maxAttempts := executionCfg.OpenAI.MaxRetries + 1
	for attemptNumber := baseAttempt; attemptNumber <= maxAttempts; attemptNumber++ {
		payload := f.buildPersonaAnalysisPayload(executionCfg, run.TargetPersonaSnapshot)
		result, callErr := client.CallResponsesAPI(ctx, payload)
		now := time.Now().UTC()
		result, callErr = f.normalizeOpenAIResult(result, callErr, payload, executionCfg.OpenAI.Model, now)

		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if callErr != nil {
			message := callErr.Error()
			if saveErr := f.personaAttemptRepo.Save(ctx, &models.BundleTagPersonaAnalysisAttempt{
				EvaluationRunID:    run.ID,
				AttemptNumber:      attemptNumber,
				RequestPayload:     result.RequestPayload,
				RawResponse:        stringPtr(result.RawResponse),
				HTTPStatusCode:     intPtr(result.HTTPStatusCode),
				ProviderResponseID: result.ProviderResponseID,
				ModelName:          firstNonEmpty(result.ModelName, executionCfg.OpenAI.Model),
				UsageMetadata:      result.UsageMetadata,
				ErrorMessage:       &message,
				RequestedAt:        result.RequestedAt,
				RespondedAt:        &result.RespondedAt,
				CreatedAt:          now,
			}); saveErr != nil {
				return "", saveErr
			}
			if !shouldRetryOpenAIError(callErr) {
				return "", callErr
			}
			if err := waitBeforeOpenAIRetry(ctx, result, attemptNumber, maxAttempts); err != nil {
				return "", err
			}
			continue
		}

		extracted, parseErr := extractOpenAIResponseText(result.RawResponse)
		var extractedPtr *string
		var errMessage *string
		if parseErr == nil {
			extracted = strings.TrimSpace(extracted)
			if extracted != "" {
				extractedPtr = &extracted
			}
		} else {
			msg := parseErr.Error()
			errMessage = &msg
		}

		if err := f.personaAttemptRepo.Save(ctx, &models.BundleTagPersonaAnalysisAttempt{
			EvaluationRunID:       run.ID,
			AttemptNumber:         attemptNumber,
			RequestPayload:        result.RequestPayload,
			RawResponse:           stringPtr(result.RawResponse),
			ExtractedResponseText: extractedPtr,
			HTTPStatusCode:        intPtr(result.HTTPStatusCode),
			ProviderResponseID:    result.ProviderResponseID,
			ModelName:             firstNonEmpty(result.ModelName, executionCfg.OpenAI.Model),
			UsageMetadata:         result.UsageMetadata,
			ErrorMessage:          errMessage,
			RequestedAt:           result.RequestedAt,
			RespondedAt:           &result.RespondedAt,
			CreatedAt:             now,
		}); err != nil {
			return "", err
		}

		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			EventType:       models.BundleTagEvaluationEventPersonaResponseReceived,
			Payload:         f.mustMarshalJSON(map[string]any{"attempt_number": attemptNumber, "http_status_code": result.HTTPStatusCode}),
			CreatedAt:       now,
		}); err != nil {
			return "", err
		}

		if extractedPtr != nil {
			if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
				EvaluationRunID: run.ID,
				EventType:       models.BundleTagEvaluationEventPersonaAnalysisCompleted,
				Payload:         f.mustMarshalJSON(map[string]any{"attempt_number": attemptNumber}),
				CreatedAt:       time.Now().UTC(),
			}); err != nil {
				return "", err
			}
			return *extractedPtr, nil
		}
		if err := waitBeforeOpenAIRetry(ctx, result, attemptNumber, maxAttempts); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("persona analysis failed after retries")
}

func (f *BundleTagEvaluationFlowImpl) ensureBatches(ctx context.Context, run *models.BundleTagEvaluationRun) ([]*models.BundleTagEvaluationBatch, error) {
	existing, err := f.batchRepo.ListByRunID(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return existing, nil
	}

	snapshots := make([]models.BundleTagEvaluationTagSnapshot, 0)
	var afterID *uint
	for {
		rows, err := f.tagRepo.ListActiveAfterID(ctx, afterID, max(1, run.TagBatchSize))
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, tag := range rows {
			if tag == nil {
				continue
			}
			if tag.IsActive == nil || !*tag.IsActive {
				return nil, fmt.Errorf("active tag query returned inactive tag %d", tag.ID)
			}
			if strings.TrimSpace(tag.Name) == "" ||
				strings.TrimSpace(derefString(tag.DisplayTitle)) == "" ||
				strings.TrimSpace(derefString(tag.AudiencePersona)) == "" ||
				tag.AudienceCount == nil {
				return nil, fmt.Errorf("tag %d is missing required evaluation metadata", tag.ID)
			}
			snapshots = append(snapshots, models.BundleTagEvaluationTagSnapshot{
				TagID:              tag.ID,
				TagName:            tag.Name,
				TagDisplayTitle:    strings.TrimSpace(derefString(tag.DisplayTitle)),
				TagAudiencePersona: strings.TrimSpace(derefString(tag.AudiencePersona)),
				TagAudienceCount:   *tag.AudienceCount,
			})
		}
		lastID := rows[len(rows)-1].ID
		afterID = &lastID
	}

	batches := make([]*models.BundleTagEvaluationBatch, 0)
	batchSize := max(1, run.TagBatchSize)
	for idx, batchNumber := 0, 1; idx < len(snapshots); idx, batchNumber = idx+batchSize, batchNumber+1 {
		end := idx + batchSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		chunk := snapshots[idx:end]
		rawChunk, err := json.Marshal(chunk)
		if err != nil {
			return nil, err
		}
		batches = append(batches, &models.BundleTagEvaluationBatch{
			EvaluationRunID: run.ID,
			BatchNumber:     batchNumber,
			TagCount:        len(chunk),
			FirstTagID:      chunk[0].TagID,
			LastTagID:       chunk[len(chunk)-1].TagID,
			TagsSnapshot:    rawChunk,
			CreatedAt:       time.Now().UTC(),
		})
	}

	if len(batches) == 0 {
		return []*models.BundleTagEvaluationBatch{}, nil
	}

	if err := repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		for _, batch := range batches {
			if err := f.batchRepo.Save(txCtx, batch); err != nil {
				return err
			}
			if err := f.eventRepo.Save(txCtx, &models.BundleTagEvaluationEvent{
				EvaluationRunID: run.ID,
				BatchID:         &batch.ID,
				EventType:       models.BundleTagEvaluationEventBatchCreated,
				Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "tag_count": batch.TagCount}),
				CreatedAt:       time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return f.batchRepo.ListByRunID(ctx, run.ID)
}

func (f *BundleTagEvaluationFlowImpl) processBatch(ctx context.Context, run *models.BundleTagEvaluationRun, batch *models.BundleTagEvaluationBatch, personaAnalysisText string, executionCfg config.SmartTagEvaluationConfig, client services.SmartTagOpenAIClient) error {
	completed, err := f.eventRepo.ExistsByBatchIDAndType(ctx, batch.ID, models.BundleTagEvaluationEventBatchCompleted)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}

	existingCount, err := f.scoreRepo.CountByRunIDAndBatchID(ctx, run.ID, batch.ID)
	if err != nil {
		return err
	}
	if existingCount == int64(batch.TagCount) {
		return f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			BatchID:         &batch.ID,
			EventType:       models.BundleTagEvaluationEventBatchCompleted,
			Payload:         f.mustMarshalJSON(map[string]any{"message": "batch completed from existing scores", "batch_number": batch.BatchNumber}),
			CreatedAt:       time.Now().UTC(),
		})
	}

	var tagSnapshots []models.BundleTagEvaluationTagSnapshot
	if err := json.Unmarshal(batch.TagsSnapshot, &tagSnapshots); err != nil {
		return err
	}

	startedExists, err := f.eventRepo.ExistsByBatchIDAndType(ctx, batch.ID, models.BundleTagEvaluationEventBatchStarted)
	if err != nil {
		return err
	}
	if !startedExists {
		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			BatchID:         &batch.ID,
			EventType:       models.BundleTagEvaluationEventBatchStarted,
			Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber}),
			CreatedAt:       time.Now().UTC(),
		}); err != nil {
			return err
		}
	}

	latestAttempt, err := f.batchAttemptRepo.LatestByBatchID(ctx, batch.ID)
	if err != nil {
		return err
	}
	if latestAttempt != nil && isSuccessfulAttemptStatus(latestAttempt.HTTPStatusCode) && strings.TrimSpace(derefString(latestAttempt.RawResponse)) != "" {
		results, rawResults, parseErr := f.parseAndValidateBatchResponse(derefString(latestAttempt.RawResponse), tagSnapshots, executionCfg.Validation)
		if parseErr == nil {
			if err := f.insertBatchScores(ctx, run, batch, latestAttempt.ID, tagSnapshots, results, rawResults); err != nil {
				return err
			}
			return f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
				EvaluationRunID: run.ID,
				BatchID:         &batch.ID,
				EventType:       models.BundleTagEvaluationEventBatchCompleted,
				Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "attempt_number": latestAttempt.AttemptNumber}),
				CreatedAt:       time.Now().UTC(),
			})
		}
	}
	if previousErr := nonRetryableBatchAttemptError(latestAttempt); previousErr != nil {
		return previousErr
	}

	baseAttempt := 1
	if latestAttempt != nil {
		baseAttempt = latestAttempt.AttemptNumber + 1
	}
	maxAttempts := executionCfg.OpenAI.MaxRetries + 1
	for attemptNumber := baseAttempt; attemptNumber <= maxAttempts; attemptNumber++ {
		if err := f.ensureBatchTagsRemainActive(ctx, tagSnapshots); err != nil {
			return err
		}
		payload := f.buildTagScoringPayload(executionCfg, personaAnalysisText, tagSnapshots)
		result, callErr := client.CallResponsesAPI(ctx, payload)
		now := time.Now().UTC()
		result, callErr = f.normalizeOpenAIResult(result, callErr, payload, executionCfg.OpenAI.Model, now)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var results map[uint]bundleTagScoreResult
		var rawResults map[uint]json.RawMessage
		var parseErr error
		if callErr == nil {
			results, rawResults, parseErr = f.parseAndValidateBatchResponse(result.RawResponse, tagSnapshots, executionCfg.Validation)
		}

		var errMessage *string
		if callErr != nil {
			msg := callErr.Error()
			errMessage = &msg
		} else if parseErr != nil {
			msg := parseErr.Error()
			errMessage = &msg
		}
		batchAttempt := &models.BundleTagEvaluationBatchAttempt{
			BatchID:            batch.ID,
			AttemptNumber:      attemptNumber,
			RequestPayload:     result.RequestPayload,
			RawResponse:        stringPtr(result.RawResponse),
			HTTPStatusCode:     intPtr(result.HTTPStatusCode),
			ProviderResponseID: result.ProviderResponseID,
			ModelName:          firstNonEmpty(result.ModelName, executionCfg.OpenAI.Model),
			UsageMetadata:      result.UsageMetadata,
			ErrorMessage:       errMessage,
			RequestedAt:        result.RequestedAt,
			RespondedAt:        &result.RespondedAt,
			CreatedAt:          now,
		}
		if saveErr := f.batchAttemptRepo.Save(ctx, batchAttempt); saveErr != nil {
			return saveErr
		}
		if callErr != nil {
			_ = f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
				EvaluationRunID: run.ID,
				BatchID:         &batch.ID,
				EventType:       models.BundleTagEvaluationEventBatchFailed,
				Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "attempt_number": attemptNumber, "message": callErr.Error()}),
				CreatedAt:       time.Now().UTC(),
			})
			if !shouldRetryOpenAIError(callErr) {
				return callErr
			}
			if err := waitBeforeOpenAIRetry(ctx, result, attemptNumber, maxAttempts); err != nil {
				return err
			}
			continue
		}

		if err := f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			BatchID:         &batch.ID,
			EventType:       models.BundleTagEvaluationEventBatchResponseReceived,
			Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "attempt_number": attemptNumber, "http_status_code": result.HTTPStatusCode}),
			CreatedAt:       time.Now().UTC(),
		}); err != nil {
			return err
		}

		if parseErr != nil {
			_ = f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
				EvaluationRunID: run.ID,
				BatchID:         &batch.ID,
				EventType:       models.BundleTagEvaluationEventBatchFailed,
				Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "attempt_number": attemptNumber, "message": parseErr.Error()}),
				CreatedAt:       time.Now().UTC(),
			})
			if err := waitBeforeOpenAIRetry(ctx, result, attemptNumber, maxAttempts); err != nil {
				return err
			}
			continue
		}

		if err := f.insertBatchScores(ctx, run, batch, batchAttempt.ID, tagSnapshots, results, rawResults); err != nil {
			return err
		}
		return f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
			EvaluationRunID: run.ID,
			BatchID:         &batch.ID,
			EventType:       models.BundleTagEvaluationEventBatchCompleted,
			Payload:         f.mustMarshalJSON(map[string]any{"batch_number": batch.BatchNumber, "attempt_number": attemptNumber}),
			CreatedAt:       time.Now().UTC(),
		})
	}
	return fmt.Errorf("batch %d failed after retries", batch.BatchNumber)
}

func (f *BundleTagEvaluationFlowImpl) ensureBatchTagsRemainActive(ctx context.Context, snapshots []models.BundleTagEvaluationTagSnapshot) error {
	if len(snapshots) == 0 {
		return fmt.Errorf("batch contains no tags")
	}
	ids := make([]uint, 0, len(snapshots))
	expected := make(map[uint]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.TagID == 0 {
			return fmt.Errorf("batch contains an invalid tag ID")
		}
		if _, duplicate := expected[snapshot.TagID]; duplicate {
			return fmt.Errorf("batch contains duplicate tag %d", snapshot.TagID)
		}
		expected[snapshot.TagID] = struct{}{}
		ids = append(ids, snapshot.TagID)
	}

	activeTags, err := f.tagRepo.ListByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("failed to revalidate active batch tags: %w", err)
	}
	for _, tag := range activeTags {
		if tag != nil {
			delete(expected, tag.ID)
		}
	}
	if len(expected) == 0 {
		return nil
	}

	inactiveIDs := make([]uint, 0, len(expected))
	for _, id := range ids {
		if _, inactive := expected[id]; inactive {
			inactiveIDs = append(inactiveIDs, id)
		}
	}
	return fmt.Errorf("tag catalog changed during evaluation; inactive or missing tags would be sent to OpenAI: %v", inactiveIDs)
}

func waitBeforeOpenAIRetry(ctx context.Context, result *services.SmartTagOpenAIResult, attemptNumber, maxAttempts int) error {
	if attemptNumber >= maxAttempts {
		return nil
	}

	delay := 500 * time.Millisecond
	for retry := 1; retry < attemptNumber && delay < 30*time.Second; retry++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	if result != nil && result.RetryAfter > delay {
		delay = result.RetryAfter
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (f *BundleTagEvaluationFlowImpl) insertBatchScores(ctx context.Context, run *models.BundleTagEvaluationRun, batch *models.BundleTagEvaluationBatch, batchAttemptID int64, tags []models.BundleTagEvaluationTagSnapshot, results map[uint]bundleTagScoreResult, rawResults map[uint]json.RawMessage) error {
	scoreRows, err := buildBundleTagScoreRows(run, batch, batchAttemptID, tags, results, rawResults)
	if err != nil {
		return err
	}

	return repository.WithTransaction(ctx, f.db, func(txCtx context.Context) error {
		return f.scoreRepo.SaveBatch(txCtx, scoreRows)
	})
}

func buildBundleTagScoreRows(run *models.BundleTagEvaluationRun, batch *models.BundleTagEvaluationBatch, batchAttemptID int64, tags []models.BundleTagEvaluationTagSnapshot, results map[uint]bundleTagScoreResult, rawResults map[uint]json.RawMessage) ([]*models.BundleTagScore, error) {
	scoreRows := make([]*models.BundleTagScore, 0, len(tags))
	for _, tag := range tags {
		result, ok := results[tag.TagID]
		if !ok {
			continue
		}
		rawResult, ok := rawResults[tag.TagID]
		if !ok {
			return nil, fmt.Errorf("missing raw result for tag %d", tag.TagID)
		}
		tagName := tag.TagName
		tagDisplay := tag.TagDisplayTitle
		tagPersona := tag.TagAudiencePersona
		tagAudienceCount := tag.TagAudienceCount
		scoreRows = append(scoreRows, &models.BundleTagScore{
			EvaluationRunID:          run.ID,
			BatchID:                  batch.ID,
			BatchAttemptID:           batchAttemptID,
			BundleID:                 run.BundleID,
			TagID:                    tag.TagID,
			TagNameSnapshot:          &tagName,
			TagDisplayTitleSnapshot:  &tagDisplay,
			TagPersonaSnapshot:       &tagPersona,
			TagAudienceCountSnapshot: &tagAudienceCount,
			BundleFitScore:           *result.BundleFitScore,
			FitLevel:                 strings.TrimSpace(result.FitLevel),
			RelationType:             strings.TrimSpace(result.RelationType),
			Reason:                   strings.TrimSpace(result.Reason),
			RawResult:                rawResult,
			CreatedAt:                time.Now().UTC(),
		})
	}

	return scoreRows, nil
}

func (f *BundleTagEvaluationFlowImpl) parseAndValidateBatchResponse(raw string, tags []models.BundleTagEvaluationTagSnapshot, validation config.SmartTagValidationConfig) (map[uint]bundleTagScoreResult, map[uint]json.RawMessage, error) {
	if validation.MaxMissingTagCount < 0 {
		return nil, nil, fmt.Errorf("maximum missing tag count must not be negative")
	}

	text, err := extractOpenAIResponseText(raw)
	if err != nil {
		return nil, nil, err
	}
	jsonBody := cleanupJSONText(text)
	if jsonBody == "" {
		return nil, nil, fmt.Errorf("empty batch response body")
	}

	items, err := decodeBundleTagScoreItems(jsonBody)
	if err != nil {
		return nil, nil, err
	}
	if validation.RequireExactTagCount {
		missingItemCount := len(tags) - len(items)
		if missingItemCount < 0 || missingItemCount > validation.MaxMissingTagCount {
			return nil, nil, fmt.Errorf("unexpected result count: expected %d got %d (maximum missing %d)", len(tags), len(items), validation.MaxMissingTagCount)
		}
	}

	expectedTags := make(map[uint]struct{}, len(tags))
	for _, tag := range tags {
		expectedTags[tag.TagID] = struct{}{}
	}

	results := make(map[uint]bundleTagScoreResult, len(items))
	rawResults := make(map[uint]json.RawMessage, len(items))
	for _, itemJSON := range items {
		payload := bundleTagScorePayload{}
		if err := json.Unmarshal(itemJSON, &payload); err != nil {
			return nil, nil, err
		}
		if payload.BundleFitScore != nil && payload.CampaignFitScore != nil && *payload.BundleFitScore != *payload.CampaignFitScore {
			return nil, nil, fmt.Errorf("tag %d has conflicting bundle_fit_score and campaign_fit_score", payload.TagID)
		}
		fitScore := payload.BundleFitScore
		if fitScore == nil {
			fitScore = payload.CampaignFitScore
		}
		row := bundleTagScoreResult{
			TagID:          payload.TagID,
			BundleFitScore: fitScore,
			FitLevel:       payload.FitLevel,
			RelationType:   payload.RelationType,
			Reason:         payload.Reason,
		}
		if row.TagID == 0 {
			return nil, nil, fmt.Errorf("missing tag_id")
		}
		if _, ok := expectedTags[row.TagID]; !ok && validation.RequireExactTagIDs {
			return nil, nil, fmt.Errorf("unexpected tag_id %d", row.TagID)
		}
		if _, exists := results[row.TagID]; exists {
			return nil, nil, fmt.Errorf("duplicate tag_id %d", row.TagID)
		}
		if row.BundleFitScore == nil {
			return nil, nil, fmt.Errorf("tag %d is missing bundle_fit_score", row.TagID)
		}
		if *row.BundleFitScore < 0 || *row.BundleFitScore > 100 {
			return nil, nil, fmt.Errorf("tag %d score out of range", row.TagID)
		}
		if strings.TrimSpace(row.FitLevel) == "" || strings.TrimSpace(row.RelationType) == "" || strings.TrimSpace(row.Reason) == "" {
			return nil, nil, fmt.Errorf("tag %d has missing required fields", row.TagID)
		}
		results[row.TagID] = row
		rawResults[row.TagID] = append(json.RawMessage(nil), itemJSON...)
	}

	missingTagIDs := make([]uint, 0, validation.MaxMissingTagCount+1)
	for _, tag := range tags {
		if _, ok := results[tag.TagID]; !ok {
			missingTagIDs = append(missingTagIDs, tag.TagID)
		}
	}
	if len(missingTagIDs) > validation.MaxMissingTagCount {
		return nil, nil, fmt.Errorf("missing results for %d tags %v (maximum missing %d)", len(missingTagIDs), missingTagIDs, validation.MaxMissingTagCount)
	}

	return results, rawResults, nil
}

func decodeBundleTagScoreItems(jsonBody string) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(jsonBody)
	var items []json.RawMessage
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, err
		}
		return items, nil
	}

	if strings.HasPrefix(trimmed, "{") {
		var envelope struct {
			Scores json.RawMessage `json:"scores"`
		}
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return nil, err
		}
		if len(envelope.Scores) == 0 || string(envelope.Scores) == "null" {
			return nil, fmt.Errorf("batch response object is missing scores")
		}
		if err := json.Unmarshal(envelope.Scores, &items); err != nil {
			return nil, fmt.Errorf("invalid batch response scores: %w", err)
		}
		return items, nil
	}

	return nil, fmt.Errorf("batch response must be a JSON array or an object containing scores")
}

func (f *BundleTagEvaluationFlowImpl) buildPersonaAnalysisPayload(executionCfg config.SmartTagEvaluationConfig, targetPersona string) map[string]any {
	payload := map[string]any{
		"model": executionCfg.OpenAI.Model,
		"input": []map[string]any{
			buildResponsesAPIMessage("system", executionCfg.PersonaAnalysis.SystemPrompt),
			buildResponsesAPIMessage("user", targetPersona),
		},
		"max_output_tokens": executionCfg.OpenAI.MaxOutputTokens,
	}
	if executionCfg.OpenAI.Temperature != nil {
		payload["temperature"] = *executionCfg.OpenAI.Temperature
	}
	if reasoning := buildReasoningPayload(executionCfg.OpenAI.ReasoningEffort); reasoning != nil {
		payload["reasoning"] = reasoning
	}
	return payload
}

func (f *BundleTagEvaluationFlowImpl) buildTagScoringPayload(executionCfg config.SmartTagEvaluationConfig, personaAnalysisText string, tags []models.BundleTagEvaluationTagSnapshot) map[string]any {
	tagJSON, _ := json.Marshal(tags)
	userPrompt := fmt.Sprintf("Bundle persona analysis:\n%s\n\nTags:\n%s", personaAnalysisText, string(tagJSON))
	payload := map[string]any{
		"model": executionCfg.OpenAI.Model,
		"input": []map[string]any{
			buildResponsesAPIMessage("system", executionCfg.TagScoring.SystemPrompt),
			buildResponsesAPIMessage("user", userPrompt),
		},
		"max_output_tokens": executionCfg.OpenAI.MaxOutputTokens,
	}
	if executionCfg.OpenAI.Temperature != nil {
		payload["temperature"] = *executionCfg.OpenAI.Temperature
	}
	if reasoning := buildReasoningPayload(executionCfg.OpenAI.ReasoningEffort); reasoning != nil {
		payload["reasoning"] = reasoning
	}
	return payload
}

func (f *BundleTagEvaluationFlowImpl) failRun(ctx context.Context, runID int64, cause error) error {
	message := cause.Error()
	_ = f.eventRepo.Save(ctx, &models.BundleTagEvaluationEvent{
		EvaluationRunID: runID,
		EventType:       models.BundleTagEvaluationEventFailed,
		Payload:         f.mustMarshalJSON(map[string]any{"message": message, "error_message": message}),
		CreatedAt:       time.Now().UTC(),
	})
	return cause
}

func (f *BundleTagEvaluationFlowImpl) failRunUnlessCanceled(ctx context.Context, runID int64, cause error) error {
	if ctx.Err() != nil {
		// Shutdown and execution timeouts are resumable interruptions, not terminal
		// evaluation failures. Leave the latest event nonterminal for the scheduler.
		return cause
	}
	return f.failRun(ctx, runID, cause)
}

func (f *BundleTagEvaluationFlowImpl) configurationSnapshot() bundleTagEvaluationConfigurationSnapshot {
	timeout := f.cfg.OpenAI.Timeout.String()
	var proxy *string
	if f.cfg.OpenAI.HTTPProxy != nil {
		redactedProxy := redactProxy(*f.cfg.OpenAI.HTTPProxy)
		proxy = &redactedProxy
	}
	return bundleTagEvaluationConfigurationSnapshot{
		Version:         5,
		PersonaAnalysis: snapshotPromptConfig{SystemPrompt: &f.cfg.PersonaAnalysis.SystemPrompt},
		TagScoring:      snapshotPromptConfig{SystemPrompt: &f.cfg.TagScoring.SystemPrompt},
		OpenAI: snapshotOpenAIConfig{
			BaseURL:         &f.cfg.OpenAI.BaseURL,
			Model:           &f.cfg.OpenAI.Model,
			ReasoningEffort: f.cfg.OpenAI.ReasoningEffort,
			MaxOutputTokens: &f.cfg.OpenAI.MaxOutputTokens,
			Temperature:     f.cfg.OpenAI.Temperature,
			Timeout:         &timeout,
			MaxRetries:      &f.cfg.OpenAI.MaxRetries,
			HTTPProxy:       proxy,
		},
		Batching: snapshotBatchingConfig{
			TagBatchSize:       &f.cfg.Batching.TagBatchSize,
			MaxParallelBatches: &f.cfg.Batching.MaxParallelBatches,
		},
		Validation: snapshotValidationConfig{
			RequireExactTagCount: &f.cfg.Validation.RequireExactTagCount,
			RequireExactTagIDs:   &f.cfg.Validation.RequireExactTagIDs,
			MaxMissingTagCount:   &f.cfg.Validation.MaxMissingTagCount,
		},
	}
}

func (f *BundleTagEvaluationFlowImpl) executionConfiguration(run *models.BundleTagEvaluationRun) (config.SmartTagEvaluationConfig, error) {
	executionCfg := f.cfg
	if run == nil {
		return executionCfg, fmt.Errorf("bundle tag evaluation run is nil")
	}

	// This dedicated column predates snapshot version 2 and keeps queued version-1
	// runs deterministic for persona analysis during a rolling deployment.
	executionCfg.PersonaAnalysis.SystemPrompt = run.PersonaAnalysisPromptSnapshot

	if len(run.ConfigurationSnapshot) == 0 {
		return executionCfg, nil
	}

	var snapshot bundleTagEvaluationConfigurationSnapshot
	if err := json.Unmarshal(run.ConfigurationSnapshot, &snapshot); err != nil {
		return executionCfg, fmt.Errorf("invalid evaluation configuration snapshot: %w", err)
	}

	if snapshot.PersonaAnalysis.SystemPrompt != nil {
		executionCfg.PersonaAnalysis.SystemPrompt = *snapshot.PersonaAnalysis.SystemPrompt
	}
	if snapshot.TagScoring.SystemPrompt != nil {
		executionCfg.TagScoring.SystemPrompt = *snapshot.TagScoring.SystemPrompt
	}
	if snapshot.OpenAI.BaseURL != nil {
		executionCfg.OpenAI.BaseURL = *snapshot.OpenAI.BaseURL
	}
	if snapshot.OpenAI.Model != nil {
		executionCfg.OpenAI.Model = *snapshot.OpenAI.Model
	}
	if snapshot.Version >= 3 || snapshot.OpenAI.ReasoningEffort != nil {
		executionCfg.OpenAI.ReasoningEffort = snapshot.OpenAI.ReasoningEffort
	}
	if snapshot.OpenAI.MaxOutputTokens != nil {
		executionCfg.OpenAI.MaxOutputTokens = *snapshot.OpenAI.MaxOutputTokens
	}
	if snapshot.Version >= 3 || snapshot.OpenAI.Temperature != nil {
		executionCfg.OpenAI.Temperature = snapshot.OpenAI.Temperature
	}
	if snapshot.OpenAI.Timeout != nil {
		timeout, err := time.ParseDuration(*snapshot.OpenAI.Timeout)
		if err != nil || timeout <= 0 {
			return executionCfg, fmt.Errorf("invalid evaluation timeout snapshot %q", *snapshot.OpenAI.Timeout)
		}
		executionCfg.OpenAI.Timeout = timeout
	}
	if snapshot.OpenAI.MaxRetries != nil {
		executionCfg.OpenAI.MaxRetries = *snapshot.OpenAI.MaxRetries
	}
	if snapshot.Batching.TagBatchSize != nil {
		executionCfg.Batching.TagBatchSize = *snapshot.Batching.TagBatchSize
	}
	if snapshot.Batching.MaxParallelBatches != nil {
		executionCfg.Batching.MaxParallelBatches = *snapshot.Batching.MaxParallelBatches
	}
	if snapshot.Validation.RequireExactTagCount != nil {
		executionCfg.Validation.RequireExactTagCount = *snapshot.Validation.RequireExactTagCount
	}
	if snapshot.Validation.RequireExactTagIDs != nil {
		executionCfg.Validation.RequireExactTagIDs = *snapshot.Validation.RequireExactTagIDs
	}
	if snapshot.Validation.MaxMissingTagCount != nil {
		executionCfg.Validation.MaxMissingTagCount = *snapshot.Validation.MaxMissingTagCount
	}

	// API credentials and proxy routing deliberately stay operational config;
	// snapshots contain only a redacted proxy and cannot be used for transport.
	if strings.TrimSpace(executionCfg.OpenAI.Model) == "" {
		return executionCfg, fmt.Errorf("evaluation model snapshot is empty")
	}
	if executionCfg.OpenAI.MaxOutputTokens <= 0 {
		return executionCfg, fmt.Errorf("evaluation max_output_tokens snapshot must be positive")
	}
	if executionCfg.OpenAI.MaxRetries < 0 {
		return executionCfg, fmt.Errorf("evaluation max_retries snapshot must not be negative")
	}
	if executionCfg.Validation.MaxMissingTagCount < 0 {
		return executionCfg, fmt.Errorf("evaluation max_missing_tag_count snapshot must not be negative")
	}
	if executionCfg.Batching.MaxParallelBatches <= 0 {
		// Snapshots written before parallel batch execution did not contain this
		// setting. Preserve resumability while keeping concurrency bounded.
		executionCfg.Batching.MaxParallelBatches = 1
	}
	return executionCfg, nil
}

func (f *BundleTagEvaluationFlowImpl) normalizeOpenAIResult(result *services.SmartTagOpenAIResult, callErr error, payload map[string]any, model string, now time.Time) (*services.SmartTagOpenAIResult, error) {
	if result == nil {
		result = &services.SmartTagOpenAIResult{}
		if callErr == nil {
			callErr = fmt.Errorf("OpenAI client returned a nil result")
		}
	}
	if len(result.RequestPayload) == 0 {
		result.RequestPayload = f.mustMarshalJSON(payload)
	}
	if strings.TrimSpace(result.ModelName) == "" {
		result.ModelName = model
	}
	if result.RequestedAt.IsZero() {
		result.RequestedAt = now
	}
	if result.RespondedAt.IsZero() {
		result.RespondedAt = now
	}
	return result, callErr
}

func buildResponsesAPIMessage(role string, text string) map[string]any {
	return map[string]any{
		"role": role,
		"content": []map[string]any{
			{
				"type": "input_text",
				"text": text,
			},
		},
	}
}

func buildReasoningPayload(effort *string) any {
	if effort == nil || strings.TrimSpace(*effort) == "" {
		return nil
	}
	return map[string]any{"effort": *effort}
}

func extractOpenAIResponseText(raw string) (string, error) {
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return "", err
	}

	if outputText, ok := body["output_text"].(string); ok && strings.TrimSpace(outputText) != "" {
		return outputText, nil
	}

	output, ok := body["output"].([]any)
	if !ok {
		return "", fmt.Errorf("response output not found")
	}
	for _, item := range output {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if itemType, _ := itemMap["type"].(string); itemType != "" && itemType != "message" {
			continue
		}
		content, ok := itemMap["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range content {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if partType, _ := partMap["type"].(string); partType != "" && partType != "output_text" {
				continue
			}
			if text, ok := partMap["text"].(string); ok && strings.TrimSpace(text) != "" {
				return text, nil
			}
			if textMap, ok := partMap["text"].(map[string]any); ok {
				if value, ok := textMap["value"].(string); ok && strings.TrimSpace(value) != "" {
					return value, nil
				}
			}
		}
	}
	return "", fmt.Errorf("response text not found")
}

func cleanupJSONText(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```JSON")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
	}
	return strings.TrimSpace(trimmed)
}

func normalizePersona(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	return norm.NFC.String(trimmed)
}

func utcDayBounds(now time.Time) (time.Time, time.Time) {
	utcNow := now.UTC()
	dayStart := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	return dayStart, dayStart.AddDate(0, 0, 1)
}

func lockCustomerEvaluationRequest(ctx context.Context, db *gorm.DB, customerID uint) error {
	tx := db.WithContext(ctx)
	if contextTx, ok := ctx.Value(repository.TxContextKey).(*gorm.DB); ok && contextTx != nil {
		tx = contextTx
	}

	var lockedCustomerID uint
	if err := tx.Raw("SELECT id FROM customers WHERE id = ? FOR UPDATE", customerID).Scan(&lockedCustomerID).Error; err != nil {
		return err
	}
	if lockedCustomerID == 0 {
		return fmt.Errorf("customer %d disappeared while queuing evaluation", customerID)
	}
	return nil
}

func lockBundleEvaluationRequest(ctx context.Context, db *gorm.DB, bundleID uint) error {
	tx := db.WithContext(ctx)
	if contextTx, ok := ctx.Value(repository.TxContextKey).(*gorm.DB); ok && contextTx != nil {
		tx = contextTx
	}

	var lockedBundleID uint
	if err := tx.Raw("SELECT id FROM bundles WHERE id = ? FOR UPDATE", bundleID).Scan(&lockedBundleID).Error; err != nil {
		return err
	}
	if lockedBundleID == 0 {
		return fmt.Errorf("bundle %d disappeared while queuing evaluation", bundleID)
	}
	return nil
}

func acquireBundleEvaluationLock(ctx context.Context, db *gorm.DB, bundleID uint) (func(), error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, err
	}

	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", int64(bundleID)).Scan(&locked); err != nil {
		conn.Close()
		return nil, err
	}
	if !locked {
		conn.Close()
		return nil, fmt.Errorf("bundle evaluation lock busy for bundle %d", bundleID)
	}

	released := false
	return func() {
		if released {
			return
		}
		released = true
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", int64(bundleID))
		_ = conn.Close()
	}, nil
}

func redactProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil {
		if parsed.User != nil {
			parsed.User = nil
		}
		return parsed.String()
	}
	return "[invalid proxy]"
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func intPtr(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shouldRetryOpenAIError(err error) bool {
	var httpErr *services.SmartTagOpenAIHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}
	return true
}

func isSuccessfulAttemptStatus(statusCode *int) bool {
	// Nil keeps older audit rows and tests backward compatible; the current
	// client always records a status for an HTTP response.
	return statusCode == nil || (*statusCode >= 200 && *statusCode < 300)
}

func nonRetryableAttemptError(attempt *models.BundleTagPersonaAnalysisAttempt) error {
	if attempt == nil || attempt.HTTPStatusCode == nil || isSuccessfulAttemptStatus(attempt.HTTPStatusCode) {
		return nil
	}
	return nonRetryableHTTPStatusError(*attempt.HTTPStatusCode, derefString(attempt.ErrorMessage))
}

func nonRetryableBatchAttemptError(attempt *models.BundleTagEvaluationBatchAttempt) error {
	if attempt == nil || attempt.HTTPStatusCode == nil || isSuccessfulAttemptStatus(attempt.HTTPStatusCode) {
		return nil
	}
	return nonRetryableHTTPStatusError(*attempt.HTTPStatusCode, derefString(attempt.ErrorMessage))
}

func nonRetryableHTTPStatusError(statusCode int, message string) error {
	httpErr := &services.SmartTagOpenAIHTTPError{
		StatusCode: statusCode,
		Message:    firstNonEmpty(message, "previous OpenAI request failed"),
	}
	if httpErr.Retryable() {
		return nil
	}
	return httpErr
}

func (f *BundleTagEvaluationFlowImpl) mustMarshalJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
