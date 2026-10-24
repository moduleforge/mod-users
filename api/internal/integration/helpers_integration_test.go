//go:build integration

package integration

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"golang.org/x/crypto/ssh"
)

// newSSHKey returns a fresh ed25519 key and its authorized_keys line.
func newSSHKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sshPub, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// newSSHPublicKey returns a fresh ed25519 key in authorized_keys format.
func newSSHPublicKey(t *testing.T) string {
	t.Helper()
	_, line := newSSHKey(t)
	return line
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

// doRaw issues a GET-style request with explicit headers.
func (s *stack) doRaw(t *testing.T, method, path string, headers map[string]string) *resp {
	t.Helper()
	req := newRequest(method, path, "")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := newRecorder()
	s.handler.ServeHTTP(rec, req)
	return &resp{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
}

func newRequest(method, path, token string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}
