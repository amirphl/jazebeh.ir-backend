package models

import "testing"

func TestSmartTargetingAllowedColorsUsesSMSProvider(t *testing.T) {
	payam := SmartTargetingAllowedColors(CampaignPlatformSMS, SMSProviderPayamSMS)
	if len(payam) != 2 || payam[0] != "white" || payam[1] != "pink" {
		t.Fatalf("PayamSMS colors = %v, want [white pink]", payam)
	}

	candoo := SmartTargetingAllowedColors(CampaignPlatformSMS, SMSProviderCandoo)
	if len(candoo) != 1 || candoo[0] != "black" {
		t.Fatalf("Candoo Smart Targeting colors = %v, want [black]", candoo)
	}
	if colors := SmartTargetingAllowedColors(CampaignPlatformBale, SMSProviderPayamSMS); len(colors) != 0 {
		t.Fatalf("non-SMS colors = %v, want no restriction", colors)
	}
}

func TestSMSDeliveryAllowedColorsKeepsStandardCandooUnrestricted(t *testing.T) {
	payam := SMSDeliveryAllowedColors(CampaignPlatformSMS, SMSProviderPayamSMS)
	if len(payam) != 2 || payam[0] != "white" || payam[1] != "pink" {
		t.Fatalf("PayamSMS standard delivery colors = %v, want [white pink]", payam)
	}
	if colors := SMSDeliveryAllowedColors(CampaignPlatformSMS, SMSProviderCandoo); len(colors) != 0 {
		t.Fatalf("Candoo standard delivery colors = %v, want no restriction", colors)
	}
}
