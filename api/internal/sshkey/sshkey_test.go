package sshkey

import (
	"crypto/elliptic"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestParse(t *testing.T) {
	ed25519Line := authorizedKeysLine(t, genEd25519(t), "user@host")
	ecdsa256Line := authorizedKeysLine(t, genECDSA(t, elliptic.P256()), "user@host")
	ecdsa384Line := authorizedKeysLine(t, genECDSA(t, elliptic.P384()), "user@host")
	ecdsa521Line := authorizedKeysLine(t, genECDSA(t, elliptic.P521()), "user@host")
	rsa2048Line := authorizedKeysLine(t, genRSA(t, 2048), "user@host")
	rsa1024Line := authorizedKeysLine(t, genRSA(t, 1024), "user@host")
	noCommentLine := authorizedKeysLine(t, genEd25519(t), "")

	longInput := strings.Repeat("a", 16*1024+1)

	tests := []struct {
		name        string
		line        string
		wantErr     error
		wantType    string
		wantComment string
	}{
		{name: "ed25519 accepted", line: ed25519Line, wantType: ssh.KeyAlgoED25519, wantComment: "user@host"},
		{name: "ecdsa p256 accepted", line: ecdsa256Line, wantType: ssh.KeyAlgoECDSA256, wantComment: "user@host"},
		{name: "ecdsa p384 accepted", line: ecdsa384Line, wantType: ssh.KeyAlgoECDSA384, wantComment: "user@host"},
		{name: "ecdsa p521 accepted", line: ecdsa521Line, wantType: ssh.KeyAlgoECDSA521, wantComment: "user@host"},
		{name: "rsa 2048 accepted", line: rsa2048Line, wantType: ssh.KeyAlgoRSA, wantComment: "user@host"},
		{name: "sk-ed25519 accepted", line: skEd25519FixtureLine, wantType: ssh.KeyAlgoSKED25519, wantComment: "test-sk-ed25519"},
		{name: "sk-ecdsa accepted", line: skECDSAFixtureLine, wantType: ssh.KeyAlgoSKECDSA256, wantComment: "test-sk-ecdsa"},
		{name: "comment extraction: no comment present", line: noCommentLine, wantType: ssh.KeyAlgoED25519, wantComment: ""},
		{name: "rsa 1024 refused as too weak", line: rsa1024Line, wantErr: ErrTooWeak},
		{name: "dsa refused as unsupported", line: dsaFixtureLine, wantErr: ErrUnsupportedType},
		{name: "certificate refused as unsupported", line: certFixtureLine, wantErr: ErrUnsupportedType},
		{name: "options-prefixed line refused as invalid", line: `command="/bin/true" ` + ed25519Line, wantErr: ErrInvalid},
		{name: "two-key input refused as invalid", line: ed25519Line + "\n" + ecdsa256Line, wantErr: ErrInvalid},
		{name: "garbage refused as invalid", line: "not an ssh key at all", wantErr: ErrInvalid},
		{name: "empty input refused as invalid", line: "", wantErr: ErrInvalid},
		{name: "input over 16 KiB refused", line: longInput, wantErr: ErrInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tc.line)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse() error = %v, want errors.Is match for %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() unexpected error: %v", err)
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
			if got.Comment != tc.wantComment {
				t.Errorf("Comment = %q, want %q", got.Comment, tc.wantComment)
			}
			if got.Canonical == "" {
				t.Error("Canonical is empty, want non-empty canonical authorized_keys text")
			}
			if !strings.HasPrefix(got.Fingerprint, "SHA256:") {
				t.Errorf("Fingerprint = %q, want SHA256:... form", got.Fingerprint)
			}
			if got.Key == nil {
				t.Error("Key is nil, want the parsed ssh.PublicKey")
			}
		})
	}
}

// TestParse_CanonicalAndFingerprintAgreeWithDirectHelpers covers requirement
// 3: Parse must derive Canonical/Fingerprint through the same helpers
// exposed for registration and resolution, so all three agree byte-for-byte
// on the same key.
func TestParse_CanonicalAndFingerprintAgreeWithDirectHelpers(t *testing.T) {
	key := genEd25519(t)
	line := authorizedKeysLine(t, key, "user@host")

	parsed, err := Parse(line)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got, want := parsed.Canonical, Canonical(key); got != want {
		t.Errorf("Parsed.Canonical = %q, want %q (direct Canonical helper)", got, want)
	}
	if got, want := parsed.Fingerprint, Fingerprint(key); got != want {
		t.Errorf("Parsed.Fingerprint = %q, want %q (direct Fingerprint helper)", got, want)
	}
}

func TestParse_ErrorsNeverEchoKeyMaterial(t *testing.T) {
	// A regression guard for the "error messages must not echo the
	// submitted key material" requirement: no error message may contain
	// the base64 key blob it was asked to refuse.
	rsa1024Line := authorizedKeysLine(t, genRSA(t, 1024), "user@host")
	fields := strings.Fields(rsa1024Line)
	if len(fields) < 2 {
		t.Fatalf("test fixture malformed: %q", rsa1024Line)
	}
	base64Blob := fields[1]

	_, err := Parse(rsa1024Line)
	if err == nil {
		t.Fatalf("Parse() unexpectedly accepted a weak key")
	}
	if strings.Contains(err.Error(), base64Blob) {
		t.Fatalf("Parse() error echoes submitted key material: %v", err)
	}
}
