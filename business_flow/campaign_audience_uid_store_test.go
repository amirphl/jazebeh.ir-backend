package businessflow

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
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

func TestVisitCampaignAudienceUIDsDoesNotBlockPushStatisticsAppend(t *testing.T) {
	campaignID := uint(time.Now().UnixNano())
	path := campaignAudienceUIDsFilePath(campaignID)
	t.Cleanup(func() {
		_ = os.Remove(path)
		campaignAudienceUIDLocks.Delete(campaignID)
	})
	if err := appendCampaignAudienceUIDs(campaignID, []dto.BotAudienceUIDItem{{UID: "first", Code: "first-code"}}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	visitDone := make(chan error, 1)
	go func() {
		visitDone <- visitCampaignAudienceUIDs(campaignID, func(campaignAudienceUIDRecord) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	appendDone := make(chan error, 1)
	go func() {
		appendDone <- appendCampaignAudienceUIDs(campaignID, []dto.BotAudienceUIDItem{{UID: "later", Code: "later-code"}})
	}()
	select {
	case err := <-appendDone:
		if err != nil {
			t.Fatalf("append while visiting snapshot: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("campaign push-statistics append remained blocked by report scan")
	}
	close(release)
	if err := <-visitDone; err != nil {
		t.Fatalf("visit snapshot: %v", err)
	}
}
