package sshkey

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLabel(t *testing.T) {
	explicit := "my laptop key"
	explicitPadded := "  my laptop key  "
	empty := ""
	over100 := strings.Repeat("x", 101)
	exactly100 := strings.Repeat("x", 100)

	surrounded := "  my key\n"
	nul, newline, tab := "a\x00b", "a\nb", "a\tb"
	del, rlo, lri := "a\u007fb", "a\u202eb", "a\u2066b"
	lrm, rlm, alm := "a\u200eb", "a\u200fb", "a\u061cb"
	zwj := "a\u200db"

	tests := []struct {
		name      string
		requested *string
		comment   string
		want      string
		wantErr   error
	}{
		{name: "explicit label is trimmed and used as-is", requested: &explicitPadded, comment: "ignored comment", want: explicit},
		{name: "explicit label at exactly 100 runes is accepted", requested: &exactly100, comment: "", want: exactly100},
		{name: "explicit label over 100 runes is rejected", requested: &over100, comment: "", wantErr: ErrLabelTooLong},
		{name: "explicit empty label is valid", requested: &empty, comment: "fallback comment", want: ""},
		{name: "nil request defaults to trimmed comment", requested: nil, comment: "  comment as label  ", want: "comment as label"},
		{name: "nil request with empty comment yields empty label", requested: nil, comment: "", want: ""},
		{name: "explicit label with NUL is rejected", requested: &nul, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with embedded newline is rejected", requested: &newline, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with embedded tab is rejected", requested: &tab, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with DEL is rejected", requested: &del, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+202E is rejected", requested: &rlo, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+2066 is rejected", requested: &lri, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+200E (LRM) is rejected", requested: &lrm, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+200F (RLM) is rejected", requested: &rlm, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+061C (ALM) is rejected", requested: &alm, wantErr: ErrLabelInvalidChars},
		{name: "explicit label with U+200D (ZWJ) is still accepted", requested: &zwj, want: zwj},
		{name: "explicit label with only surrounding whitespace is valid", requested: &surrounded, want: "my key"},
		{name: "comment control and bidi characters are stripped", requested: nil, comment: "a\u202eb\x00c", want: "abc"},
		{name: "comment empty after stripping yields empty label", requested: nil, comment: "\x00\u202e", want: ""},
		{name: "comment with U+200E, U+200F, U+061C is stripped", requested: nil, comment: "a\u200eb\u200fc\u061cd", want: "abcd"},
		{name: "nil request truncates an over-length comment to 100 runes without error", requested: nil, comment: over100, want: exactly100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeLabel(tc.requested, tc.comment)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("NormalizeLabel() error = %v, want errors.Is match for %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeLabel() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizeLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeLabel_RejectsOversizedRawInputBeforeRuneCounting(t *testing.T) {
	// A requested label far larger than any possible 100-rune result must be
	// rejected on its raw byte length, without the function doing a full
	// trim/rune-count pass over it first (see maxLabelRawBytes).
	huge := strings.Repeat("x", 10_000)
	_, err := NormalizeLabel(&huge, "")
	if !errors.Is(err, ErrLabelTooLong) {
		t.Fatalf("NormalizeLabel() error = %v, want errors.Is match for ErrLabelTooLong", err)
	}
}

func TestNormalizeLabel_TruncatesByRuneNotByte(t *testing.T) {
	// A multi-byte rune label at the 100-rune boundary must be truncated by
	// rune count, not raw byte count, or this would corrupt UTF-8 or cut the
	// label short.
	comment := strings.Repeat("é", 105) // each 'é' is 2 bytes in UTF-8
	got, err := NormalizeLabel(nil, comment)
	if err != nil {
		t.Fatalf("NormalizeLabel() unexpected error: %v", err)
	}
	if runeCount := len([]rune(got)); runeCount != 100 {
		t.Errorf("NormalizeLabel() truncated to %d runes, want 100", runeCount)
	}
}

func TestIsDisallowedLabelRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r    rune
		want bool
	}{
		{'a', false},
		{' ', false},
		{'é', false},
		{0x00, true},
		{'\n', true},
		{'\t', true},
		{0x7f, true},
		{0x85, true},
		{0x2029, false},
		{0x202A, true},
		{0x202E, true},
		{0x202F, false},
		{0x2065, false},
		{0x2066, true},
		{0x2069, true},
		{0x206A, false},
		{0x061C, true},
		{0x200E, true},
		{0x200F, true},
		{0x200D, false},
		{0x2010, false},
	}
	for _, tc := range tests {
		if got := isDisallowedLabelRune(tc.r); got != tc.want {
			t.Errorf("isDisallowedLabelRune(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

func TestParse_CommentCanCarryControlCharacters(t *testing.T) {
	t.Parallel()

	// Documents that NUL and bidi characters survive Parse into Comment (so
	// the default-label strip path is reachable); a newline instead splits
	// the input into a second line and is refused.
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBl0Ub0EDwl4y6RGrKUYy3xzQeF8bYh9vQ6kzT8b3F9k"
	for _, comment := range []string{"a\x00b", "a\u202eb"} {
		p, err := Parse(key + " " + comment)
		if err != nil {
			t.Fatalf("Parse() unexpected error for comment %q: %v", comment, err)
		}
		if p.Comment != comment {
			t.Errorf("Parse().Comment = %q, want %q", p.Comment, comment)
		}
	}
	if _, err := Parse(key + " a\nb"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Parse() with newline in comment: error = %v, want ErrInvalid", err)
	}
}
