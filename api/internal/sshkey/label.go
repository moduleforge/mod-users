package sshkey

import (
	"fmt"
	"strings"
	"unicode"
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

// isDisallowedLabelRune reports whether r may not appear in a stored label:
// any control character (unicode.IsControl, which covers NUL, C0, DEL, and
// C1), or a bidi formatting character (U+202A through U+202E embeddings and
// overrides, U+2066 through U+2069 isolates) that could spoof how the label
// displays.
func isDisallowedLabelRune(r rune) bool {
	return unicode.IsControl(r) ||
		(r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2066 && r <= 0x2069)
}

// NormalizeLabel resolves the stored label per design note D7. When
// requested is non-nil, its trimmed value is used; it must not exceed 100
// runes, or NormalizeLabel returns ErrLabelTooLong, and it must not contain
// a control character or bidi formatting character once surrounding
// whitespace is trimmed, or NormalizeLabel returns ErrLabelInvalidChars.
// When requested is nil, the label defaults to the comment with control and
// bidi formatting characters stripped, then trimmed and silently truncated
// to 100 runes rather than rejected. An empty result is valid in both cases.
func NormalizeLabel(requested *string, comment string) (string, error) {
	if requested != nil {
		if len(*requested) > maxLabelRawBytes {
			return "", fmt.Errorf("sshkey: label exceeds %d bytes: %w", maxLabelRawBytes, ErrLabelTooLong)
		}
		trimmed := strings.TrimSpace(*requested)
		if n := len([]rune(trimmed)); n > maxLabelRunes {
			return "", fmt.Errorf("sshkey: label is %d runes, maximum is %d: %w", n, maxLabelRunes, ErrLabelTooLong)
		}
		if strings.IndexFunc(trimmed, isDisallowedLabelRune) >= 0 {
			return "", fmt.Errorf("sshkey: label contains control or bidi formatting characters: %w", ErrLabelInvalidChars)
		}
		return trimmed, nil
	}

	stripped := strings.Map(func(r rune) rune {
		if isDisallowedLabelRune(r) {
			return -1
		}
		return r
	}, comment)
	runes := []rune(strings.TrimSpace(stripped))
	if len(runes) > maxLabelRunes {
		runes = runes[:maxLabelRunes]
	}
	return string(runes), nil
}
