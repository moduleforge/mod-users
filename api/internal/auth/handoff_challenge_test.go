package auth

import (
	"strings"
	"testing"
)

// testHandoffVerifier is a well-formed verifier (43 unreserved characters).
const testHandoffVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

var testHandoffChallenge = HandoffChallenge(testHandoffVerifier)

func TestHandoffChallenge_MatchesRFC7636Vector(t *testing.T) {
	t.Parallel()
	// RFC 7636 appendix B.
	if got, want := testHandoffChallenge, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"; got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}
	if !ValidHandoffChallenge(testHandoffChallenge) {
		t.Error("a SHA-256 challenge must be valid")
	}
}

func TestValidHandoffChallenge_RejectsMalformed(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]string{
		"empty":     "",
		"short":     "abc",
		"long":      testHandoffChallenge + "A",
		"padded":    testHandoffChallenge[:42] + "=",
		"std alpha": strings.Repeat("+", 43),
		"non-b64":   strings.Repeat("!", 43),
	} {
		if ValidHandoffChallenge(c) {
			t.Errorf("%s: %q accepted", name, c)
		}
	}
}

func TestVerifyHandoff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name               string
		verifier, challeng string
		want               bool
	}{
		{"correct", testHandoffVerifier, testHandoffChallenge, true},
		{"wrong verifier", strings.Repeat("a", 43), testHandoffChallenge, false},
		{"empty verifier", "", testHandoffChallenge, false},
		{"short verifier", "short", HandoffChallenge("short"), false},
		{"illegal character", strings.Repeat("a", 42) + "!", HandoffChallenge(strings.Repeat("a", 42) + "!"), false},
		{"no stored challenge", testHandoffVerifier, "", false},
	}
	for _, tc := range cases {
		if got := VerifyHandoff(tc.verifier, tc.challeng); got != tc.want {
			t.Errorf("%s: VerifyHandoff = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStatePayload_HandoffChallengeShape(t *testing.T) {
	t.Parallel()
	signer, err := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	exp := int64(4102444800)
	roundTrip := func(p StatePayload) error {
		p.Expires = exp
		tok, err := signer.Sign(p)
		if err != nil {
			t.Fatal(err)
		}
		_, err = signer.Verify(tok)
		return err
	}
	if err := roundTrip(StatePayload{HandoffChallenge: testHandoffChallenge}); err != nil {
		t.Errorf("sign-in with a challenge: %v", err)
	}
	if err := roundTrip(StatePayload{HandoffChallenge: "bad"}); err == nil {
		t.Error("a malformed challenge in the state must be refused")
	}
	if err := roundTrip(StatePayload{Flow: FlowLink, SessionUUID: "s", PrincipalUUID: "p", HandoffChallenge: testHandoffChallenge}); err == nil {
		t.Error("a link state carrying a handoff challenge must be refused")
	}
}
