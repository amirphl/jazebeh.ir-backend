package businessflow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
)

type campaignAudienceUIDRecord struct {
	UID  string `json:"uid"`
	Code string `json:"code"`
}

var campaignAudienceUIDLocks sync.Map

var errCampaignAudienceUIDLimitExceeded = errors.New("campaign audience UID limit exceeded")

func campaignAudienceUIDsDirPath() string {
	return filepath.Join("data", "campaign_audience_uids")
}

func campaignAudienceUIDsFilePath(campaignID uint) string {
	return filepath.Join(campaignAudienceUIDsDirPath(), fmt.Sprintf("%d.jsonl", campaignID))
}

func campaignAudienceUIDLock(campaignID uint) *sync.Mutex {
	lock, _ := campaignAudienceUIDLocks.LoadOrStore(campaignID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func appendCampaignAudienceUIDs(campaignID uint, items []dto.BotAudienceUIDItem) error {
	if campaignID == 0 {
		return fmt.Errorf("campaign id must be greater than 0")
	}
	if len(items) == 0 {
		return nil
	}

	lock := campaignAudienceUIDLock(campaignID)
	lock.Lock()
	defer lock.Unlock()

	path := campaignAudienceUIDsFilePath(campaignID)
	if err := removeCampaignAudienceUIDsFileIfExpired(path, time.Now()); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, item := range items {
		if err := enc.Encode(campaignAudienceUIDRecord{
			UID:  item.UID,
			Code: item.Code,
		}); err != nil {
			return err
		}
	}

	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func readCampaignAudienceUIDs(campaignID uint) ([]string, map[string]string, error) {
	return readCampaignAudienceUIDsBounded(campaignID, 0)
}

// readCampaignAudienceUIDsBounded reads at most maxUIDs distinct UIDs. A
// non-positive limit preserves the unbounded behavior used by legacy exports.
// The bound is enforced while scanning so an oversized file cannot make a
// request allocate its complete UID map before it is rejected.
func readCampaignAudienceUIDsBounded(campaignID uint, maxUIDs int) ([]string, map[string]string, error) {
	if campaignID == 0 {
		return nil, nil, fmt.Errorf("campaign id must be greater than 0")
	}

	lock := campaignAudienceUIDLock(campaignID)
	lock.Lock()
	defer lock.Unlock()

	path := campaignAudienceUIDsFilePath(campaignID)
	if err := removeCampaignAudienceUIDsFileIfExpired(path, time.Now()); err != nil {
		return nil, nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	return collectCampaignAudienceUIDs(f, maxUIDs)
}

func collectCampaignAudienceUIDs(reader io.Reader, maxUIDs int) ([]string, map[string]string, error) {
	uidToCode := make(map[string]string)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var record campaignAudienceUIDRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, nil, err
		}
		if record.UID == "" {
			continue
		}
		if _, exists := uidToCode[record.UID]; !exists && maxUIDs > 0 && len(uidToCode) >= maxUIDs {
			return nil, nil, errCampaignAudienceUIDLimitExceeded
		}
		uidToCode[record.UID] = record.Code
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	uids := make([]string, 0, len(uidToCode))
	for uid := range uidToCode {
		uids = append(uids, uid)
	}
	return uids, uidToCode, nil
}

func removeCampaignAudienceUIDsFileIfExpired(path string, now time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if now.Sub(info.ModTime()) <= audienceUIDsTTL {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
