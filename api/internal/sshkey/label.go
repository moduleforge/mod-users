package sshkey

import (
	"fmt"
	"strings"
)

// maxLabelRunes is the maximum length, in runes, of a normalized label (D7).
const maxLabelRunes = 100

// maxLabelRawBytes bounds the raw, caller-supplied requested label before it
// is trimmed or rune-counted. Every UTF-8 rune consumes at most 4 bytes, so
// any input past this bound is unconditionally over maxLabelRunes once
// measured in runes; rejecting it here avoids doing O(n) trim/rune-count
// work on an arbitrarily large caller-supplied string before the real
// length check gets a chance to act, mirroring Parse's own early 16 KiB
// input-size guard.
const maxLabelRawBytes = 4 * maxLabelRunes

// NormalizeLabel resolves the stored label per design note D7. When
// requested is non-nil, its trimmed value is used; it must not exceed 100
// runes, or NormalizeLabel returns ErrLabelTooLong. When requested is nil,
// the label defaults to the trimmed comment, silently truncated to 100
// runes rather than rejected. An empty result is valid in both cases.
func NormalizeLabel(requested *string, comment string) (string, error) {
	if requested != nil {
		if len(*requested) > maxLabelRawBytes {
			return "", fmt.Errorf("sshkey: label exceeds %d bytes: %w", maxLabelRawBytes, ErrLabelTooLong)
		}
		trimmed := strings.TrimSpace(*requested)
		if n := len([]rune(trimmed)); n > maxLabelRunes {
			return "", fmt.Errorf("sshkey: label is %d runes, maximum is %d: %w", n, maxLabelRunes, ErrLabelTooLong)
		}
		return trimmed, nil
	}

	runes := []rune(strings.TrimSpace(comment))
	if len(runes) > maxLabelRunes {
		runes = runes[:maxLabelRunes]
	}
	return string(runes), nil
}
