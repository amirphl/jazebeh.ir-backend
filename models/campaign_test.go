package models

import "testing"

func TestSmartTargetingAllowedColorsUsesSMSProvider(t *testing.T) {
	payam := SmartTargetingAllowedColors(CampaignPlatformSMS, SMSProviderPayamSMS)
	if len(payam) != 2 || payam[0] != "white" || payam[1] != "pink" {
		t.Fatalf("PayamSMS colors = %v, want [white pink]", payam)
	}

	if colors := SmartTargetingAllowedColors(CampaignPlatformSMS, SMSProviderCandoo); len(colors) != 0 {
		t.Fatalf("Candoo colors = %v, want no restriction", colors)
	}
	if colors := SmartTargetingAllowedColors(CampaignPlatformBale, SMSProviderPayamSMS); len(colors) != 0 {
		t.Fatalf("non-SMS colors = %v, want no restriction", colors)
	}
}
