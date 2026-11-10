package businessflow

import (
	"errors"
	"strings"
	"testing"
)

func TestCollectCampaignAudienceUIDsBoundedRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	_, _, err := collectCampaignAudienceUIDs(strings.NewReader(""+
		`{"uid":"audience-1","code":"code-1"}`+"\n"+
		`{"uid":"audience-2","code":"code-2"}`+"\n"+
		`{"uid":"audience-3","code":"code-3"}`+"\n"), 2)
	if !errors.Is(err, errCampaignAudienceUIDLimitExceeded) {
		t.Fatalf("error = %v, want audience UID limit exceeded", err)
	}
}

func TestCollectCampaignAudienceUIDsBoundedAllowsDuplicateUpdates(t *testing.T) {
	t.Parallel()

	uids, uidToCode, err := collectCampaignAudienceUIDs(strings.NewReader(""+
		`{"uid":"audience-1","code":"old-code"}`+"\n"+
		`{"uid":"audience-1","code":"new-code"}`+"\n"+
		`{"uid":"audience-2","code":"code-2"}`+"\n"), 2)
	if err != nil {
		t.Fatalf("collect audience UIDs: %v", err)
	}
	if len(uids) != 2 || uidToCode["audience-1"] != "new-code" || uidToCode["audience-2"] != "code-2" {
		t.Fatalf("UIDs = %v, mapping = %v", uids, uidToCode)
	}
}
