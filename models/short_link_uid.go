package models

import (
	"fmt"
	"strings"
)

// ShortLinkUIDLength is the public short-link token length.  Existing legacy
// tokens remain valid, but every newly allocated token must use this width.
const ShortLinkUIDLength = 6

// ShortLinkUIDCapacity is the number of base-36 values that fit in a
// ShortLinkUIDLength token.
const ShortLinkUIDCapacity uint64 = 36 * 36 * 36 * 36 * 36 * 36

// FormatShortLinkUID converts a numeric allocator value to a fixed-width,
// lowercase base-36 token. Keeping the format here prevents the bot, admin,
// and campaign-test paths from drifting again.
func FormatShortLinkUID(value uint64) (string, error) {
	if value >= ShortLinkUIDCapacity {
		return "", fmt.Errorf("short-link UID sequence exhausted at %d", value)
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if value == 0 {
		return strings.Repeat("0", ShortLinkUIDLength), nil
	}
	buf := make([]byte, 0, ShortLinkUIDLength)
	for value > 0 {
		buf = append(buf, digits[value%36])
		value /= 36
	}
	for left, right := 0, len(buf)-1; left < right; left, right = left+1, right-1 {
		buf[left], buf[right] = buf[right], buf[left]
	}
	return strings.Repeat("0", ShortLinkUIDLength-len(buf)) + string(buf), nil
}
