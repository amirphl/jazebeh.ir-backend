package models

import "testing"

func TestValidateNewShortLinkRequiresAllocatorContract(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	position := 0
	valid := &ShortLink{UID: "100000", AllocationKey: &key, AllocationPosition: &position}
	if err := validateNewShortLink(valid); err != nil {
		t.Fatalf("valid allocator row rejected: %v", err)
	}

	for name, row := range map[string]*ShortLink{
		"legacy length":      {UID: "zzzzz", AllocationKey: &key, AllocationPosition: &position},
		"uppercase":          {UID: "10000A", AllocationKey: &key, AllocationPosition: &position},
		"invalid character":  {UID: "10000-", AllocationKey: &key, AllocationPosition: &position},
		"no allocation key":  {UID: "100000", AllocationPosition: &position},
		"bad allocation key": {UID: "100000", AllocationKey: func() *string { value := "short"; return &value }(), AllocationPosition: &position},
		"no position":        {UID: "100000", AllocationKey: &key},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateNewShortLink(row); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
