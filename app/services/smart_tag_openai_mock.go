package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/observability"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/google/uuid"
)

type mockSmartTagOpenAIClient struct{ model string }

func (c mockSmartTagOpenAIClient) CallResponsesAPI(ctx context.Context, payload map[string]any) (*SmartTagOpenAIResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Input []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err = json.Unmarshal(request, &decoded); err != nil {
		return nil, err
	}
	user := ""
	for _, message := range decoded.Input {
		if message.Role == "user" {
			for _, part := range message.Content {
				user += part.Text
			}
		}
	}
	output := "Simulated persona analysis for local testing."
	if _, tagsJSON, found := strings.Cut(user, "\n\nTags:\n"); found {
		var tags []models.BundleTagEvaluationTagSnapshot
		if err = json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, fmt.Errorf("mock OpenAI tags: %w", err)
		}
		scores := make([]map[string]any, 0, len(tags))
		for _, tag := range tags {
			scores = append(scores, map[string]any{"tag_id": tag.TagID, "bundle_fit_score": rand.IntN(101), "fit_level": "medium", "relation_type": "neutral", "reason": "Simulated result"})
		}
		encoded, encodeErr := json.Marshal(scores)
		if encodeErr != nil {
			return nil, encodeErr
		}
		output = string(encoded)
	}
	id := "mock-" + uuid.NewString()
	response, err := json.Marshal(map[string]any{"id": id, "model": c.model, "output_text": output})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	log.Printf("mock provider=openai action=responses")
	observability.RecordMockOperation("openai", "responses", "success", 1)
	return &SmartTagOpenAIResult{RequestPayload: request, RawResponse: string(response), HTTPStatusCode: 200, ProviderResponseID: &id, ModelName: c.model, RequestedAt: now, RespondedAt: now}, nil
}
