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
