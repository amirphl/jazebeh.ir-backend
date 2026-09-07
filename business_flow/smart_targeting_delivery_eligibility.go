package businessflow

import (
	"context"
	"strings"

	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

// smartTargetingAllowedColorsForCampaign resolves the sender provider before
// deriving audience eligibility. Missing or retired line configuration keeps
// the historic PayamSMS-safe behavior; only a configured Candoo line removes
// the color restriction.
func smartTargetingAllowedColorsForCampaign(
	ctx context.Context,
	lineNumberRepo repository.LineNumberRepository,
	campaign *models.Campaign,
) ([]string, error) {
	if campaign == nil {
		return nil, nil
	}

	provider := models.SMSProviderPayamSMS
	if lineNumberRepo != nil && campaign.Spec.LineNumber != nil {
		lineNumber := strings.TrimSpace(*campaign.Spec.LineNumber)
		if lineNumber != "" {
			line, err := lineNumberRepo.ByValue(ctx, lineNumber)
			if err != nil {
				return nil, err
			}
			if line != nil && line.Provider == models.SMSProviderCandoo {
				provider = models.SMSProviderCandoo
			}
		}
	}

	return models.SmartTargetingAllowedColors(campaign.Spec.Platform, provider), nil
}
