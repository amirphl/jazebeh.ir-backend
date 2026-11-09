package repository

import (
	"strings"
	"testing"
)

func TestNonAutomatedClickTrafficSQLUsesAllBotSignals(t *testing.T) {
	condition := nonAutomatedClickTrafficSQL("click")

	for _, fragment := range []string{
		"click.is_test IS NOT TRUE",
		"COALESCE(click.ip, '') !~ '^(66\\.249\\.|74\\.125\\.)'",
		"COALESCE(click.user_agent, '') !~* '" + automatedClickUserAgentPattern + "'",
		"COALESCE(click.user_agent, '') ~* 'Chrome'",
		"COALESCE(click.user_agent, '') !~* '(Edg|OPR|Opera)'",
		"COALESCE(click.user_agent, '') ~* 'X11; Linux|Linux'",
		"COALESCE(click.user_agent, '') !~* 'Android|Windows NT|Mac OS X|Macintosh|iPhone|iPad|iPod'",
	} {
		if !strings.Contains(condition, fragment) {
			t.Fatalf("bot filter does not contain %q:\n%s", fragment, condition)
		}
	}
}

func TestTagPerformanceSQLUsesSharedBotFilter(t *testing.T) {
	for _, fragment := range []string{
		"COALESCE(click.ip, '') !~ '^(66\\.249\\.|74\\.125\\.)'",
		"COALESCE(click.user_agent, '') !~* '" + automatedClickUserAgentPattern + "'",
	} {
		if !strings.Contains(recomputeCampaignTagPerformanceSQL, fragment) {
			t.Fatalf("tag performance query does not contain %q", fragment)
		}
	}
}
