package handlers

// Tests that stateless shape validation (SSH key and label format, StartLink
// return path) runs before the step-up gate: a malformed request is a 400,
// never consumes the single-use step-up token, and is a 400 (not 409) when no
// token is presented.

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// stepordValidKey is a well-formed ed25519 authorized_keys line.
var stepordValidKey = stepordGenKey()

func stepordGenKey() string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

func stepordConsumedLen(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

func stepordPtr(s string) *string { return &s }

func TestStepOrdering_SSHKeyRegister(t *testing.T) {
	uc := newTestUC(1, 10)
	tooLong := strings.Repeat("a", 500)
	routes := []struct {
		name string
		path string
	}{
		{"RegisterSelf", "/self/ssh-keys"},
		{"RegisterForAccount self-targeting", "/user-accounts/" + uc.UserUUID + "/ssh-keys"},
	}
	bad := []struct {
		name     string
		key      string
		label    *string
		wantCode string
	}{
		{"malformed key", "ssh-ed25519 AAAA", nil, "users.ssh_key_invalid"},
		{"label too long", stepordValidKey, &tooLong, "users.ssh_key_label_too_long"},
		{"label bad chars", stepordValidKey, stepordPtr("bad\x07label"), "users.ssh_key_label_invalid"},
	}
	for _, rt := range routes {
		for _, b := range bad {
			t.Run(rt.name+"/"+b.name+"/valid token", func(t *testing.T) {
				svc := &stubSSHKeyService{}
				consumed := &sync.Map{}
				r := newSSHKeysTestRouter(NewSSHKeysHandler(svc, stepUpTestSecret, consumed, true))
				req := jsonRequest(t, http.MethodPost, rt.path, registerSSHKeyRequest{PublicKey: b.key, Label: b.label})
				req.Header.Set("X-Step-Up-Token", issueTestStepUpToken(t, uc.UserAccountID))
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, withUC(req, uc))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				if code := decodeErrorCode(t, rec.Body.Bytes()); code != b.wantCode {
					t.Errorf("code = %q, want %q", code, b.wantCode)
				}
				if n := stepordConsumedLen(consumed); n != 0 {
					t.Errorf("consumed store has %d entries, want 0 (token must not be consumed)", n)
				}
				if svc.registerCalled {
					t.Error("service must not be called")
				}
			})
			t.Run(rt.name+"/"+b.name+"/no token", func(t *testing.T) {
				svc := &stubSSHKeyService{}
				r := newSSHKeysTestRouter(NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, true))
				req := jsonRequest(t, http.MethodPost, rt.path, registerSSHKeyRequest{PublicKey: b.key, Label: b.label})
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, withUC(req, uc))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (not 409); body=%s", rec.Code, rec.Body.String())
				}
			})
		}
		t.Run(rt.name+"/well-formed/no token still 409", func(t *testing.T) {
			svc := &stubSSHKeyService{}
			r := newSSHKeysTestRouter(NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, true))
			req := jsonRequest(t, http.MethodPost, rt.path, registerSSHKeyRequest{PublicKey: stepordValidKey})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, withUC(req, uc))
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "users.step_up_required") {
				t.Errorf("body missing users.step_up_required: %s", rec.Body.String())
			}
			if svc.registerCalled {
				t.Error("service must not be called")
			}
		})
	}
}

type stepordLinkFake struct {
	err    error
	called bool
}

func (f *stepordLinkFake) LinkAuthorizeURL(_, _, _ string) (string, string, error) {
	f.called = true
	if f.err != nil {
		return "", "", f.err
	}
	return "https://idp.test/authorize", "state-token", nil
}

func TestStepOrdering_StartLink(t *testing.T) {
	uc := newTestUC(1, 10)
	run := func(h *IdentitiesHandler, query, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/self/identities/oidc/google/start"+query, nil)
		if token != "" {
			req.Header.Set("X-Step-Up-Token", token)
		}
		req = setChiParam(withUC(req, uc), "provider", "google")
		rec := httptest.NewRecorder()
		h.StartLink(rec, req)
		return rec
	}
	newHandler := func(fake *stepordLinkFake, consumed *sync.Map) *IdentitiesHandler {
		return &IdentitiesHandler{oauth: fake, jwtSecret: stepUpTestSecret, consumed: consumed, stepUpRequired: true}
	}

	for _, ret := range []string{"?return=http://evil.test/x", "?return=//evil.test", "?return=relative"} {
		t.Run("malformed "+ret+"/valid token", func(t *testing.T) {
			fake, consumed := &stepordLinkFake{}, &sync.Map{}
			rec := run(newHandler(fake, consumed), ret, issueTestStepUpToken(t, uc.UserAccountID))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "users.return_path_invalid" {
				t.Errorf("code = %q", code)
			}
			if n := stepordConsumedLen(consumed); n != 0 {
				t.Errorf("consumed store has %d entries, want 0", n)
			}
			if fake.called {
				t.Error("LinkAuthorizeURL must not be called")
			}
		})
		t.Run("malformed "+ret+"/no token", func(t *testing.T) {
			rec := run(newHandler(&stepordLinkFake{}, &sync.Map{}), ret, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (not 409); body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("well-formed/no token still 409", func(t *testing.T) {
		rec := run(newHandler(&stepordLinkFake{}, &sync.Map{}), "?return=/self/identities", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "users.step_up_required") {
			t.Errorf("body missing users.step_up_required: %s", rec.Body.String())
		}
	})

	t.Run("well-formed/valid token succeeds", func(t *testing.T) {
		fake := &stepordLinkFake{}
		rec := run(newHandler(fake, &sync.Map{}), "?return=/self/identities", issueTestStepUpToken(t, uc.UserAccountID))
		if rec.Code != http.StatusOK || !fake.called {
			t.Fatalf("status = %d called=%v; body=%s", rec.Code, fake.called, rec.Body.String())
		}
	})

	t.Run("sign or rand failure is 500", func(t *testing.T) {
		fake := &stepordLinkFake{err: errors.New("oauth: random: entropy exhausted")}
		rec := run(newHandler(fake, &sync.Map{}), "", issueTestStepUpToken(t, uc.UserAccountID))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
		}
	})
}
