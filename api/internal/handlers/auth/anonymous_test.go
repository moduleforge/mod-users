package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// ---------------------------------------------------------------------------
// Stub anonUserCreator
// ---------------------------------------------------------------------------

type stubAnonCreator struct {
	result svc.CreateAnonymousUserResult
	err    error
}

func (s *stubAnonCreator) CreateAnonymousUser(_ context.Context, _ svc.CreateAnonymousUserInput) (svc.CreateAnonymousUserResult, error) {
	return s.result, s.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newAnonTestHandler builds a minimal Handler with the given stub service.
// pool, queries, and coreQ are nil — the Anonymous handler does not use them.
func newAnonTestHandler(t *testing.T, stub anonUserCreator) *Handler {
	t.Helper()
	h := &Handler{
		jwtSecret: "test-secret-at-least-32-bytes-xx",
		issuer:    "test-issuer",
		userSvc:   stub,
	}
	return h
}

// decodeJWTPayload returns the claims map from an unsigned (or signed) JWT
// without verifying the signature — sufficient for testing claim content.
func decodeJWTPayload(t *testing.T, rawToken string) map[string]any {
	t.Helper()
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAnonymous(t *testing.T) {
	t.Parallel()

	anonUUID := uuid.New()
	goodResult := svc.CreateAnonymousUserResult{
		UserAccount: svc.UserAccount{
			UUID:        anonUUID,
			IsAnonymous: true,
			Email:       nil,
		},
		AnonToken: svc.AnonToken{
			UUID:         uuid.New(),
			DeviceID:     "device-abc",
			SessionToken: strings.Repeat("a", 64),
			ExpiresAt:    pgtype.Timestamptz{},
		},
	}

	tests := []struct {
		name       string
		body       string
		stub       *stubAnonCreator
		wantStatus int
		wantCode   string // error code when status != 201
		checkJWT   bool
	}{
		{
			name:       "happy path returns 201 with jwt and session_token",
			body:       `{"device_id":"device-abc"}`,
			stub:       &stubAnonCreator{result: goodResult},
			wantStatus: http.StatusCreated,
			checkJWT:   true,
		},
		{
			name:       "missing device_id returns 400",
			body:       `{}`,
			stub:       &stubAnonCreator{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "empty device_id returns 400",
			body:       `{"device_id":""}`,
			stub:       &stubAnonCreator{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "invalid json returns 400",
			body:       `not json`,
			stub:       &stubAnonCreator{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_request",
		},
		{
			name:       "service error returns 500",
			body:       `{"device_id":"device-abc"}`,
			stub:       &stubAnonCreator{err: errors.New("db error")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newAnonTestHandler(t, tc.stub)
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/anonymous", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Anonymous(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var respBody map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			if tc.wantStatus == http.StatusCreated {
				// token must be present
				token, ok := respBody["token"].(string)
				if !ok || token == "" {
					t.Errorf("expected non-empty token, got: %v", respBody["token"])
				}
				// session_token must be present
				sessionToken, ok := respBody["session_token"].(string)
				if !ok || sessionToken == "" {
					t.Errorf("expected non-empty session_token, got: %v", respBody["session_token"])
				}
				// user object must carry uuid and is_anonymous=true
				userObj, ok := respBody["user"].(map[string]any)
				if !ok {
					t.Fatalf("user field missing or wrong type: %v", respBody["user"])
				}
				if userObj["uuid"] != anonUUID.String() {
					t.Errorf("user.uuid = %v, want %s", userObj["uuid"], anonUUID.String())
				}
				if userObj["is_anonymous"] != true {
					t.Errorf("user.is_anonymous = %v, want true", userObj["is_anonymous"])
				}

				if tc.checkJWT {
					claims := decodeJWTPayload(t, token)
					if claims["is_anonymous"] != true {
						t.Errorf("JWT is_anonymous = %v, want true", claims["is_anonymous"])
					}
					if claims["sub"] != anonUUID.String() {
						t.Errorf("JWT sub = %v, want %s", claims["sub"], anonUUID.String())
					}
				}
			} else {
				// error responses carry a nested error object
				errObj, ok := respBody["error"].(map[string]any)
				if !ok {
					t.Fatalf("error field missing: %v", respBody)
				}
				if errObj["code"] != tc.wantCode {
					t.Errorf("error.code = %v, want %s", errObj["code"], tc.wantCode)
				}
			}
		})
	}
}
