package businessflow

import (
	"encoding/json"
	"testing"
)

func TestSameExecutionReservationRequestSnapshotIgnoresJSONBFormatting(t *testing.T) {
	created := json.RawMessage(`{"tag_ids":[14696,15557,16270,16456,17000],"score_classes":["A"],"allowed_colors":["white","pink"],"platform":"sms"}`)
	// PostgreSQL JSONB returns a normalized textual representation, not the
	// compact bytes originally written by json.Marshal.
	readBack := json.RawMessage(`{ "platform": "sms", "allowed_colors": ["white", "pink"], "score_classes": ["A"], "tag_ids": [14696, 15557, 16270, 16456, 17000] }`)

	if !sameExecutionReservationRequestSnapshot(created, readBack) {
		t.Fatal("semantically identical JSONB snapshots must match")
	}
}

func TestSameExecutionReservationRequestSnapshotDetectsEligibilityChange(t *testing.T) {
	current := json.RawMessage(`{"tag_ids":[9],"score_classes":["A"],"allowed_colors":["black"],"platform":"sms"}`)
	changed := json.RawMessage(`{"tag_ids":[9],"score_classes":["B"],"allowed_colors":["black"],"platform":"sms"}`)

	if sameExecutionReservationRequestSnapshot(current, changed) {
		t.Fatal("a changed eligibility snapshot must not match")
	}
}
