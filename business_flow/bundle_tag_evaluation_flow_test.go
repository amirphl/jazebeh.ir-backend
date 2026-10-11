package businessflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/services"
	"github.com/amirphl/Yamata-no-Orochi/config"
	"github.com/amirphl/Yamata-no-Orochi/models"
)

type smartTagOpenAIClientFunc func(context.Context, map[string]any) (*services.SmartTagOpenAIResult, error)

func (f smartTagOpenAIClientFunc) CallResponsesAPI(ctx context.Context, payload map[string]any) (*services.SmartTagOpenAIResult, error) {
	return f(ctx, payload)
}

func TestExecutionConfigurationUsesRunSnapshot(t *testing.T) {
	reasoningEffort := "medium"
	temperature := 0.25
	proxy := "http://user:password@proxy.example.test:8080"
	original := config.SmartTagEvaluationConfig{
		PersonaAnalysis: config.SmartTagPromptConfig{SystemPrompt: "persona-v1"},
		TagScoring:      config.SmartTagPromptConfig{SystemPrompt: "scoring-v1"},
		OpenAI: config.SmartTagOpenAIConfig{
			BaseURL:         "https://v1.example.test",
			Model:           "model-v1",
			ReasoningEffort: &reasoningEffort,
			MaxOutputTokens: 1234,
			Temperature:     &temperature,
			Timeout:         45 * time.Second,
			MaxRetries:      2,
			HTTPProxy:       &proxy,
		},
		Batching: config.SmartTagBatchingConfig{TagBatchSize: 10, MaxParallelBatches: 3},
		Validation: config.SmartTagValidationConfig{
			RequireExactTagCount: true,
			RequireExactTagIDs:   true,
			MaxMissingTagCount:   1,
		},
	}
	queuedFlow := &BundleTagEvaluationFlowImpl{cfg: original}
	run := &models.BundleTagEvaluationRun{
		PersonaAnalysisPromptSnapshot: original.PersonaAnalysis.SystemPrompt,
		ConfigurationSnapshot:         queuedFlow.mustMarshalJSON(queuedFlow.configurationSnapshot()),
	}

	current := original
	current.PersonaAnalysis.SystemPrompt = "persona-v2"
	current.TagScoring.SystemPrompt = "scoring-v2"
	current.OpenAI.Model = "model-v2"
	currentProxy := "http://new-proxy.example.test:8080"
	current.OpenAI.HTTPProxy = &currentProxy
	current.Validation.RequireExactTagIDs = false

	executionFlow := &BundleTagEvaluationFlowImpl{cfg: current}
	got, err := executionFlow.executionConfiguration(run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PersonaAnalysis.SystemPrompt != "persona-v1" || got.TagScoring.SystemPrompt != "scoring-v1" {
		t.Fatalf("expected snapshotted prompts, got persona=%q scoring=%q", got.PersonaAnalysis.SystemPrompt, got.TagScoring.SystemPrompt)
	}
	if got.OpenAI.Model != "model-v1" || !got.Validation.RequireExactTagIDs || got.Validation.MaxMissingTagCount != 1 {
		t.Fatalf("expected snapshotted execution settings, got model=%q exact_ids=%v max_missing=%d", got.OpenAI.Model, got.Validation.RequireExactTagIDs, got.Validation.MaxMissingTagCount)
	}
	if got.OpenAI.ReasoningEffort == nil || *got.OpenAI.ReasoningEffort != reasoningEffort ||
		got.OpenAI.Temperature == nil || *got.OpenAI.Temperature != temperature {
		t.Fatalf("expected snapshotted optional settings, got reasoning=%v temperature=%v", got.OpenAI.ReasoningEffort, got.OpenAI.Temperature)
	}
	if got.OpenAI.HTTPProxy == nil || *got.OpenAI.HTTPProxy != currentProxy {
		t.Fatalf("expected current operational proxy, got %v", got.OpenAI.HTTPProxy)
	}
	if got.Batching.MaxParallelBatches != 3 {
		t.Fatalf("expected snapshotted max parallel batches, got %d", got.Batching.MaxParallelBatches)
	}
}

func TestUTCDayBounds(t *testing.T) {
	input := time.Date(2026, time.September, 2, 23, 59, 59, 0, time.FixedZone("UTC+3:30", 3*60*60+30*60))
	start, end := utcDayBounds(input)
	wantStart := time.Date(2026, time.September, 2, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("utcDayBounds(%s) = (%s, %s), want (%s, %s)", input, start, end, wantStart, wantEnd)
	}
}

func TestRunBatchesConcurrentlyBoundsParallelism(t *testing.T) {
	batches := []*models.BundleTagEvaluationBatch{
		{BatchNumber: 1},
		{BatchNumber: 2},
		{BatchNumber: 3},
		{BatchNumber: 4},
	}
	started := make(chan struct{}, len(batches))
	release := make(chan struct{})
	done := make(chan error, 1)
	var active atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32

	go func() {
		done <- runBatchesConcurrently(context.Background(), batches, 2, func(ctx context.Context, _ *models.BundleTagEvaluationBatch) error {
			current := active.Add(1)
			defer active.Add(-1)
			calls.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		})
	}()

	<-started
	<-started
	select {
	case <-started:
		t.Fatal("more than two batches started before a worker became available")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("unexpected worker error: %v", err)
	}
	if got := maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
	if got := calls.Load(); got != int32(len(batches)) {
		t.Fatalf("processed batches = %d, want %d", got, len(batches))
	}
}

func TestRunBatchesConcurrentlyPreservesRootError(t *testing.T) {
	rootErr := errors.New("invalid OpenAI batch response")
	batches := []*models.BundleTagEvaluationBatch{{BatchNumber: 1}, {BatchNumber: 2}}
	err := runBatchesConcurrently(context.Background(), batches, 2, func(ctx context.Context, batch *models.BundleTagEvaluationBatch) error {
		if batch.BatchNumber == 2 {
			return rootErr
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, rootErr) {
		t.Fatalf("expected root batch error, got %v", err)
	}
	if !strings.Contains(err.Error(), "batch 2") {
		t.Fatalf("expected batch context in error, got %v", err)
	}
}

func TestOpenAIRequestPacerSpacesRequestsAndHonorsCancellation(t *testing.T) {
	pacer := newOpenAIRequestPacer(120)
	if pacer == nil || pacer.interval != 500*time.Millisecond {
		t.Fatalf("unexpected request interval: %+v", pacer)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatalf("first request should proceed immediately: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pacer.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued request should honor cancellation, got %v", err)
	}
}

func TestPacedOpenAIClientPausesAllRequestsAfterRateLimit(t *testing.T) {
	pacer := newOpenAIRequestPacer(60)
	client := &pacedOpenAIClient{
		pacer: pacer,
		delegate: smartTagOpenAIClientFunc(func(context.Context, map[string]any) (*services.SmartTagOpenAIResult, error) {
			return &services.SmartTagOpenAIResult{RetryAfter: 2 * time.Second}, &services.SmartTagOpenAIHTTPError{
				StatusCode: 429,
				Message:    "rate limited",
			}
		}),
	}

	before := time.Now()
	if _, err := client.CallResponsesAPI(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected rate-limit error")
	}
	pacer.mu.Lock()
	pauseUntil := pacer.pauseUntil
	pacer.mu.Unlock()
	if pauseUntil.Before(before.Add(1900 * time.Millisecond)) {
		t.Fatalf("global request pause is too short: %v", pauseUntil.Sub(before))
	}
}

func TestExecutionConfigurationSnapshotPreservesUnsetOptionalParameters(t *testing.T) {
	queuedFlow := &BundleTagEvaluationFlowImpl{cfg: config.SmartTagEvaluationConfig{
		PersonaAnalysis: config.SmartTagPromptConfig{SystemPrompt: "persona"},
		TagScoring:      config.SmartTagPromptConfig{SystemPrompt: "scoring"},
		OpenAI: config.SmartTagOpenAIConfig{
			Model:           "model-v1",
			MaxOutputTokens: 100,
			Timeout:         time.Second,
		},
	}}
	run := &models.BundleTagEvaluationRun{
		PersonaAnalysisPromptSnapshot: queuedFlow.cfg.PersonaAnalysis.SystemPrompt,
		ConfigurationSnapshot:         queuedFlow.mustMarshalJSON(queuedFlow.configurationSnapshot()),
	}

	reasoningEffort := "high"
	temperature := 0.5
	current := queuedFlow.cfg
	current.OpenAI.ReasoningEffort = &reasoningEffort
	current.OpenAI.Temperature = &temperature

	got, err := (&BundleTagEvaluationFlowImpl{cfg: current}).executionConfiguration(run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OpenAI.ReasoningEffort != nil || got.OpenAI.Temperature != nil {
		t.Fatalf("expected unset snapshotted options to remain nil, got reasoning=%v temperature=%v", got.OpenAI.ReasoningEffort, got.OpenAI.Temperature)
	}
}

func TestBuildOpenAIPayloadOptionalParameters(t *testing.T) {
	flow := &BundleTagEvaluationFlowImpl{}
	baseConfig := config.SmartTagEvaluationConfig{
		PersonaAnalysis: config.SmartTagPromptConfig{SystemPrompt: "persona"},
		TagScoring:      config.SmartTagPromptConfig{SystemPrompt: "scoring"},
		OpenAI: config.SmartTagOpenAIConfig{
			Model:           "test-model",
			MaxOutputTokens: 100,
		},
	}

	assertOptionalParameters := func(t *testing.T, payload map[string]any, wantPresent bool) {
		t.Helper()
		_, hasTemperature := payload["temperature"]
		_, hasReasoning := payload["reasoning"]
		if hasTemperature != wantPresent || hasReasoning != wantPresent {
			t.Fatalf("optional parameter presence: temperature=%v reasoning=%v, want %v; payload=%v", hasTemperature, hasReasoning, wantPresent, payload)
		}
	}

	t.Run("omits unset parameters", func(t *testing.T) {
		assertOptionalParameters(t, flow.buildPersonaAnalysisPayload(baseConfig, "target"), false)
		assertOptionalParameters(t, flow.buildTagScoringPayload(baseConfig, "analysis", nil), false)
	})

	t.Run("includes explicitly configured parameters", func(t *testing.T) {
		reasoningEffort := "low"
		temperature := 0.0
		configured := baseConfig
		configured.OpenAI.ReasoningEffort = &reasoningEffort
		configured.OpenAI.Temperature = &temperature

		personaPayload := flow.buildPersonaAnalysisPayload(configured, "target")
		scoringPayload := flow.buildTagScoringPayload(configured, "analysis", nil)
		assertOptionalParameters(t, personaPayload, true)
		assertOptionalParameters(t, scoringPayload, true)
		if got := personaPayload["temperature"]; got != 0.0 {
			t.Fatalf("expected explicit zero temperature, got %v", got)
		}
	})
}

func TestNormalizeOpenAIResultHandlesNilClientResult(t *testing.T) {
	flow := &BundleTagEvaluationFlowImpl{}
	result, err := flow.normalizeOpenAIResult(nil, nil, map[string]any{"model": "test-model"}, "test-model", time.Now().UTC())
	if err == nil {
		t.Fatal("expected nil client result to become an error")
	}
	if result == nil || len(result.RequestPayload) == 0 || result.ModelName != "test-model" {
		t.Fatalf("expected safe audit result, got %+v", result)
	}
}

func TestNonRetryableHTTPStatusSurvivesRestart(t *testing.T) {
	if err := nonRetryableHTTPStatusError(401, "invalid key"); err == nil {
		t.Fatal("expected a prior 401 attempt to remain terminal after restart")
	}
	if err := nonRetryableHTTPStatusError(429, "rate limited"); err != nil {
		t.Fatalf("expected a prior 429 attempt to remain retryable, got %v", err)
	}
}

func TestExtractOpenAIResponseText(t *testing.T) {
	t.Run("output_text", func(t *testing.T) {
		raw := `{"id":"resp_123","output_text":"hello world"}`
		got, err := extractOpenAIResponseText(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "hello world" {
			t.Fatalf("expected output_text, got %q", got)
		}
	})

	t.Run("nested output content", func(t *testing.T) {
		raw := `{"output":[{"content":[{"type":"output_text","text":"[1,2,3]"}]}]}`
		got, err := extractOpenAIResponseText(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "[1,2,3]" {
			t.Fatalf("expected nested text, got %q", got)
		}
	})

	t.Run("skips reasoning items", func(t *testing.T) {
		raw := `{"output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"not the final output"}]},{"type":"message","content":[{"type":"output_text","text":"final output"}]}]}`
		got, err := extractOpenAIResponseText(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "final output" {
			t.Fatalf("expected final message text, got %q", got)
		}
	})
}

func TestNormalizePersona(t *testing.T) {
	input := "  cafe\u0301\r\nshop  "
	got := normalizePersona(input)
	want := "caf\u00e9\nshop"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestParseAndValidateBatchResponse(t *testing.T) {
	flow := &BundleTagEvaluationFlowImpl{
		cfg: config.SmartTagEvaluationConfig{
			Validation: config.SmartTagValidationConfig{
				RequireExactTagCount: true,
				RequireExactTagIDs:   true,
				MaxMissingTagCount:   1,
			},
		},
	}

	tags := []models.BundleTagEvaluationTagSnapshot{
		{TagID: 11, TagName: "tag 11", TagDisplayTitle: "Tag 11", TagAudiencePersona: "persona 11", TagAudienceCount: 100},
		{TagID: 12, TagName: "tag 12", TagDisplayTitle: "Tag 12", TagAudiencePersona: "persona 12", TagAudienceCount: 200},
	}

	t.Run("accepts valid response", func(t *testing.T) {
		resultsJSON := []map[string]any{
			{
				"tag_id":           11,
				"bundle_fit_score": 91,
				"fit_level":        "very_strong",
				"relation_type":    "direct",
				"reason":           "foo",
			},
			{
				"tag_id":           12,
				"bundle_fit_score": 42,
				"fit_level":        "medium",
				"relation_type":    "indirect",
				"reason":           "bar",
			},
		}
		body, _ := json.Marshal(resultsJSON)
		raw := `{"output_text":` + string(mustJSON(t, string(body))) + `}`

		results, rawResults, err := flow.parseAndValidateBatchResponse(raw, tags, flow.cfg.Validation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 || len(rawResults) != 2 {
			t.Fatalf("expected two results, got results=%d raw=%d", len(results), len(rawResults))
		}
		if results[11].BundleFitScore == nil || *results[11].BundleFitScore != 91 {
			t.Fatalf("unexpected score for tag 11: %+v", results[11])
		}
	})

	t.Run("accepts Responses API scores envelope and campaign score alias", func(t *testing.T) {
		modelOutput := `{"scores":[{"tag_id":18,"campaign_fit_score":5,"fit_level":"unrelated","relation_type":"unrelated","reason":"reason"}]}`
		response := map[string]any{
			"id": "resp_123",
			"output": []any{
				map[string]any{
					"type":    "reasoning",
					"content": []any{},
				},
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": modelOutput,
						},
					},
				},
			},
		}
		raw := string(mustJSON(t, response))
		sampleTags := []models.BundleTagEvaluationTagSnapshot{{TagID: 18}}

		results, rawResults, err := flow.parseAndValidateBatchResponse(raw, sampleTags, flow.cfg.Validation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 || results[18].BundleFitScore == nil || *results[18].BundleFitScore != 5 {
			t.Fatalf("unexpected parsed results: %+v", results)
		}
		if !strings.Contains(string(rawResults[18]), `"campaign_fit_score":5`) {
			t.Fatalf("expected original score payload to be preserved, got %s", rawResults[18])
		}
	})

	t.Run("accepts one missing tag within tolerance", func(t *testing.T) {
		raw := `{"output_text":"[{\"tag_id\":11,\"bundle_fit_score\":91,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"}]"}`
		results, rawResults, err := flow.parseAndValidateBatchResponse(raw, tags, flow.cfg.Validation)
		if err != nil {
			t.Fatalf("unexpected validation error for tolerated missing tag: %v", err)
		}
		if len(results) != 1 || len(rawResults) != 1 {
			t.Fatalf("expected one result after ignoring missing tag, got results=%d raw=%d", len(results), len(rawResults))
		}
	})

	t.Run("zero tolerance preserves strict validation", func(t *testing.T) {
		validation := flow.cfg.Validation
		validation.MaxMissingTagCount = 0
		raw := `{"output_text":"[{\"tag_id\":11,\"bundle_fit_score\":91,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"}]"}`
		if _, _, err := flow.parseAndValidateBatchResponse(raw, tags, validation); err == nil {
			t.Fatalf("expected validation error with zero missing-tag tolerance")
		}
	})

	t.Run("rejects missing tags beyond tolerance", func(t *testing.T) {
		threeTags := append(append([]models.BundleTagEvaluationTagSnapshot{}, tags...), models.BundleTagEvaluationTagSnapshot{TagID: 13})
		raw := `{"output_text":"[{\"tag_id\":11,\"bundle_fit_score\":91,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"}]"}`
		if _, _, err := flow.parseAndValidateBatchResponse(raw, threeTags, flow.cfg.Validation); err == nil {
			t.Fatalf("expected validation error when missing tags exceed tolerance")
		}
	})

	t.Run("rejects out of range score", func(t *testing.T) {
		raw := `{"output_text":"[{\"tag_id\":11,\"bundle_fit_score\":101,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"},{\"tag_id\":12,\"bundle_fit_score\":42,\"fit_level\":\"medium\",\"relation_type\":\"indirect\",\"reason\":\"bar\"}]"}`
		if _, _, err := flow.parseAndValidateBatchResponse(raw, tags, flow.cfg.Validation); err == nil {
			t.Fatalf("expected validation error for out-of-range score")
		}
	})

	t.Run("rejects missing score", func(t *testing.T) {
		raw := `{"output_text":"[{\"tag_id\":11,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"},{\"tag_id\":12,\"bundle_fit_score\":42,\"fit_level\":\"medium\",\"relation_type\":\"indirect\",\"reason\":\"bar\"}]"}`
		if _, _, err := flow.parseAndValidateBatchResponse(raw, tags, flow.cfg.Validation); err == nil {
			t.Fatalf("expected validation error for missing score")
		}
	})

	t.Run("rejects null score", func(t *testing.T) {
		raw := `{"output_text":"[{\"tag_id\":11,\"bundle_fit_score\":null,\"fit_level\":\"very_strong\",\"relation_type\":\"direct\",\"reason\":\"foo\"},{\"tag_id\":12,\"bundle_fit_score\":42,\"fit_level\":\"medium\",\"relation_type\":\"indirect\",\"reason\":\"bar\"}]"}`
		if _, _, err := flow.parseAndValidateBatchResponse(raw, tags, flow.cfg.Validation); err == nil {
			t.Fatalf("expected validation error for null score")
		}
	})
}

func TestBuildBundleTagScoreRowsSkipsMissingTags(t *testing.T) {
	score := 91.0
	run := &models.BundleTagEvaluationRun{ID: 7, BundleID: 8}
	batch := &models.BundleTagEvaluationBatch{ID: 9}
	tags := []models.BundleTagEvaluationTagSnapshot{
		{TagID: 11, TagName: "tag 11"},
		{TagID: 14989, TagName: "ignored tag"},
	}
	results := map[uint]bundleTagScoreResult{
		11: {
			TagID:          11,
			BundleFitScore: &score,
			FitLevel:       "very_strong",
			RelationType:   "direct",
			Reason:         "reason",
		},
	}
	rawResults := map[uint]json.RawMessage{11: json.RawMessage(`{"tag_id":11}`)}

	rows, err := buildBundleTagScoreRows(run, batch, 10, tags, results, rawResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 || rows[0].TagID != 11 {
		t.Fatalf("expected only tag 11 to produce a score row, got %+v", rows)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return raw
}
