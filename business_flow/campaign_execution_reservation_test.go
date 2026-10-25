package businessflow

import (
	"errors"
	"testing"

	"github.com/amirphl/Yamata-no-Orochi/app/dto"
	"github.com/amirphl/Yamata-no-Orochi/models"
	"github.com/amirphl/Yamata-no-Orochi/repository"
)

func TestFirstMatchingSmartTargetingTagUsesPersistedTagOrder(t *testing.T) {
	if got := firstMatchingSmartTargetingTag([]int32{9, 2}, []int64{2, 9}); got != 2 {
		t.Fatalf("assigned tag = %d, want first selected tag 2", got)
	}
	if got := firstMatchingSmartTargetingTag([]int32{9}, []int64{2}); got != 0 {
		t.Fatalf("unselected tag assignment = %d, want 0", got)
	}
}

func TestShortLinkAllocationKeyIsStableAndBindsEveryRecipient(t *testing.T) {
	firstDestination := "https://example.test/a"
	secondDestination := "https://example.test/b"
	req := &dto.BotAllocateShortLinksRequest{
		CampaignID: 17,
		Items: []dto.PhoneWithAdLink{
			{Phone: "989121234567", AdLink: &firstDestination},
			{Phone: "989121234567", AdLink: &secondDestination},
		},
	}
	key := shortLinkAllocationKey(req, "https://jzbe.ir")
	if key != shortLinkAllocationKey(req, "https://jzbe.ir") {
		t.Fatal("identical allocation request produced different keys")
	}
	changed := *req
	changed.Items = append([]dto.PhoneWithAdLink(nil), req.Items...)
	changed.Items[1].Phone = "989121234568"
	if key == shortLinkAllocationKey(&changed, "https://jzbe.ir") {
		t.Fatal("allocation key does not bind recipient position and phone")
	}
}

func TestShortLinkAllocationCodesRequireCompleteOrderedRows(t *testing.T) {
	zero, one := 0, 1
	rows := []*models.ShortLink{
		{UID: "first", AllocationPosition: &zero},
		{UID: "second", AllocationPosition: &one},
	}
	codes, err := shortLinkAllocationCodes(rows, 2)
	if err != nil || len(codes) != 2 || codes[0] != "first" || codes[1] != "second" {
		t.Fatalf("allocation rows were not restored in request order: codes=%#v err=%v", codes, err)
	}
	rows[1].AllocationPosition = &zero
	if _, err := shortLinkAllocationCodes(rows, 2); err == nil {
		t.Fatal("duplicate allocation position was accepted")
	}
}

func TestExecutionReservationBusinessErrorsRemainDistinct(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{repository.ErrSmartTargetingExecutionReservationInsufficientCapacity, "SMART_TARGETING_EXECUTION_RESERVATION_INSUFFICIENT_CAPACITY"},
		{repository.ErrSmartTargetingExecutionReservationConflict, "SMART_TARGETING_EXECUTION_RESERVATION_CONFLICT"},
		{repository.ErrSmartTargetingExecutionReservationConcurrency, "SMART_TARGETING_EXECUTION_RESERVATION_CONCURRENT_CHANGE"},
		{repository.ErrSmartTargetingExecutionReservationStale, "SMART_TARGETING_EXECUTION_RESERVATION_STALE"},
		{repository.ErrSmartTargetingExecutionReservationCorrupt, "SMART_TARGETING_EXECUTION_RESERVATION_CORRUPT"},
		{repository.ErrSmartTargetingExecutionReservationMissing, "SMART_TARGETING_EXECUTION_RESERVATION_MISSING"},
		{repository.ErrSmartTargetingExecutionReservationUnavailable, "SMART_TARGETING_EXECUTION_RESERVATION_UNAVAILABLE"},
	}
	for _, test := range tests {
		mapped, ok := executionReservationBusinessError(test.err).(*BusinessError)
		if !ok {
			t.Fatalf("mapped error for %v is not a BusinessError", test.err)
		}
		if mapped.Code != test.code {
			t.Fatalf("error code for %v = %q, want %q", test.err, mapped.Code, test.code)
		}
		if !errors.Is(mapped, test.err) {
			t.Fatalf("mapped error %q did not preserve %v", mapped.Code, test.err)
		}
	}
}

func TestExecutionReservationFinalizationMetricReasonsRemainDistinct(t *testing.T) {
	tests := []struct {
		err    error
		reason string
	}{
		{repository.ErrSmartTargetingExecutionReservationInsufficientCapacity, "insufficient_capacity"},
		{repository.ErrSmartTargetingExecutionReservationConflict, "conflict"},
		{repository.ErrSmartTargetingExecutionReservationConcurrency, "concurrency"},
		{repository.ErrSmartTargetingExecutionReservationStale, "stale"},
		{repository.ErrSmartTargetingExecutionReservationCorrupt, "corrupt"},
		{repository.ErrSmartTargetingExecutionReservationMissing, "missing"},
	}
	for _, test := range tests {
		if got := smartTargetingExecutionReservationFailureReason(test.err); got != test.reason {
			t.Fatalf("reason for %v = %q, want %q", test.err, got, test.reason)
		}
	}
}
