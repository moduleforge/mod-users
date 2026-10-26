package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

func TestValidateImpersonationReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want, wantCode string
	}{
		{"a reason is trimmed", "  support ticket 42 \n", "support ticket 42", ""},
		{"empty", "", "", "users.reason_required"},
		{"blank", " \t\n ", "", "users.reason_required"},
		{"at the limit", strings.Repeat("é", MaxImpersonationReasonLen), strings.Repeat("é", MaxImpersonationReasonLen), ""},
		{"over the limit counts characters", strings.Repeat("é", MaxImpersonationReasonLen+1), "", "users.reason_too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateImpersonationReason(tc.in)
			if tc.wantCode == "" {
				if err != nil || got != tc.want {
					t.Fatalf("ValidateImpersonationReason = %q, %v; want %q", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, apiresp.ErrInvalidInput) {
				t.Fatalf("err = %v, want invalid input", err)
			}
			rec := httptest.NewRecorder()
			apiresp.WriteError(rec, httptest.NewRequest(http.MethodPost, "/", nil), err)
			if body := rec.Body.String(); rec.Code != http.StatusBadRequest ||
				!strings.Contains(body, `"`+tc.wantCode+`"`) || !strings.Contains(body, `"reason"`) {
				t.Errorf("rendered %d %s, want 400 naming field reason with code %s", rec.Code, body, tc.wantCode)
			}
		})
	}
}

func TestImpersonationNotAllowedIs403WithItsOwnCode(t *testing.T) {
	t.Parallel()
	var ae *AuthError
	if !errors.As(ErrImpersonationNotAllowed, &ae) || ae.Status != http.StatusForbidden || ae.Code != "users.impersonation_not_allowed" {
		t.Fatalf("ErrImpersonationNotAllowed = %+v, want 403 users.impersonation_not_allowed", ae)
	}
	if !errors.Is(ErrImpersonationNotAllowed, apiresp.ErrForbidden) {
		t.Error("it must classify as a forbidden refusal")
	}
	if got := ae.Envelope().Error.Code; got != "users.impersonation_not_allowed" {
		t.Errorf("envelope code = %q", got)
	}
}

func TestRenewedExpiry(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	const ttl, maxLife = 30 * time.Minute, 60 * time.Minute
	tests := []struct {
		name      string
		now       time.Time
		expiresAt time.Time
		want      time.Time
		wantOK    bool
	}{
		{"renew shortly after the start extends by the ttl from now", created.Add(10 * time.Minute), created.Add(30 * time.Minute), created.Add(40 * time.Minute), true},
		{"the cap from the start limits the renewal", created.Add(45 * time.Minute), created.Add(30 * time.Minute), created.Add(60 * time.Minute), true},
		{"at the cap there is no room", created.Add(50 * time.Minute), created.Add(60 * time.Minute), created.Add(60 * time.Minute), false},
		{"past the cap there is no room", created.Add(70 * time.Minute), created.Add(60 * time.Minute), created.Add(60 * time.Minute), false},
		{"a renewal never shortens the session", created.Add(1 * time.Minute), created.Add(40 * time.Minute), created.Add(31 * time.Minute), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := renewedExpiry(tc.now, created, tc.expiresAt, ttl, maxLife)
			if ok != tc.wantOK || !got.Equal(tc.want) {
				t.Fatalf("renewedExpiry = %v, %v; want %v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// sessionByIDQ serves GetSessionByID from a fixed row.
type sessionByIDQ struct {
	usersdb.Querier
	sess usersdb.Session
	err  error
}

func (f sessionByIDQ) GetSessionByID(context.Context, int64) (usersdb.Session, error) {
	return f.sess, f.err
}

func liveSessionRow(impersonating bool) usersdb.Session {
	s := usersdb.Session{
		ID: 7, Uuid: uuid.New(), PrincipalID: 3,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
	if impersonating {
		s.ImpersonatorSessionID = pgtype.Int8{Int64: 5, Valid: true}
		s.ImpersonatedProfileID = pgtype.Int8{Int64: 9, Valid: true}
		s.ImpersonationReason = pgtype.Text{String: "why", Valid: true}
	}
	return s
}

func TestImpersonationStartRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	in := StartImpersonationInput{CallerSessionID: 7, ProfileUUID: uuid.New(), Reason: "support ticket"}
	adminCtx := func(impersonatorPrincipal int64) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: 3, ProfileID: 4, SessionID: 7, ImpersonatorPrincipalID: impersonatorPrincipal,
		})
	}
	newSvc := func(q usersdb.Querier) *ImpersonationService {
		// No pool and no authorizer: every refusal below must come before either.
		return &ImpersonationService{q: q, sessions: &SessionService{now: time.Now}}
	}

	t.Run("a blank reason is invalid input", func(t *testing.T) {
		t.Parallel()
		bad := in
		bad.Reason = "  "
		_, err := newSvc(sessionByIDQ{sess: liveSessionRow(false)}).Start(adminCtx(0), bad)
		if !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Fatalf("err = %v, want invalid input", err)
		}
	})
	t.Run("an impersonating AuthContext cannot start another", func(t *testing.T) {
		t.Parallel()
		_, err := newSvc(sessionByIDQ{sess: liveSessionRow(false)}).Start(adminCtx(11), in)
		if !errors.Is(err, ErrImpersonationNotAllowed) {
			t.Fatalf("err = %v, want ErrImpersonationNotAllowed", err)
		}
	})
	t.Run("a session row that is an impersonation cannot start another", func(t *testing.T) {
		t.Parallel()
		_, err := newSvc(sessionByIDQ{sess: liveSessionRow(true)}).Start(adminCtx(0), in)
		if !errors.Is(err, ErrImpersonationNotAllowed) {
			t.Fatalf("err = %v, want ErrImpersonationNotAllowed", err)
		}
	})
	t.Run("a revoked caller session is session_revoked", func(t *testing.T) {
		t.Parallel()
		revoked := liveSessionRow(false)
		now := time.Now()
		revoked.RevokedAt = &now
		_, err := newSvc(sessionByIDQ{sess: revoked}).Start(adminCtx(0), in)
		if !errors.Is(err, ErrSessionRevoked) {
			t.Fatalf("err = %v, want ErrSessionRevoked", err)
		}
	})
	t.Run("no authorizer fails closed", func(t *testing.T) {
		t.Parallel()
		_, err := newSvc(sessionByIDQ{sess: liveSessionRow(false)}).Start(adminCtx(0), in)
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("err = %v, want forbidden", err)
		}
	})
}

func TestSwitchRefusesInsideAnImpersonation(t *testing.T) {
	t.Parallel()
	s := &SessionService{q: sessionByIDQ{sess: liveSessionRow(true)}, now: time.Now}
	_, err := s.Switch(context.Background(), 7, uuid.New())
	if !errors.Is(err, ErrImpersonationNotAllowed) {
		t.Fatalf("Switch err = %v, want ErrImpersonationNotAllowed", err)
	}
}
