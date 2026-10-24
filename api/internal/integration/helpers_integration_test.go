//go:build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// newSSHPublicKey returns a fresh ed25519 key in authorized_keys format.
func newSSHPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// identifierUUID returns the uuid of the account's email identifier.
func (s *stack) identifierUUID(t *testing.T, token, email string) string {
	t.Helper()
	var body struct {
		Identifiers []struct {
			UUID     string `json:"uuid"`
			Value    string `json:"value"`
			Verified bool   `json:"verified"`
		} `json:"identifiers"`
	}
	s.do(t, http.MethodGet, "/v1/me/identifiers", token, nil).wantStatus(t, http.StatusOK).into(t, &body)
	for _, i := range body.Identifiers {
		if strings.EqualFold(i.Value, email) {
			return i.UUID
		}
	}
	t.Fatalf("no identifier %s in %+v", email, body.Identifiers)
	return ""
}

// verifyRegisteredEmail verifies the registration email of a (the code was
// mailed at sign-up); this creates the account's email_otp credential.
func (s *stack) verifyRegisteredEmail(t *testing.T, a account) {
	t.Helper()
	code := s.mail.codeAfter(t, a.Email, 0)
	id := s.identifierUUID(t, a.tk.access(), a.Email)
	s.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verify", a.tk.access(), map[string]string{"code": code}).
		wantStatus(t, http.StatusOK)
}
