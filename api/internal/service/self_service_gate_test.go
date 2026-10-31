package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
)

func authCtx(ac opctx.AuthContext) context.Context {
	return opctx.WithAuthContext(context.Background(), ac)
}

func TestRequireFreshAuth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		maxAge     time.Duration
		authTime   time.Time
		wantStepUp bool
	}{
		{"fresh", 10 * time.Minute, now.Add(-time.Minute), false},
		{"exactly at the limit", 10 * time.Minute, now.Add(-10 * time.Minute), false},
		{"stale", 10 * time.Minute, now.Add(-10*time.Minute - time.Second), true},
		{"zero auth_time fails closed", 10 * time.Minute, time.Time{}, true},
		{"unset max age uses the 10 minute default (fresh)", 0, now.Add(-9 * time.Minute), false},
		{"unset max age uses the 10 minute default (stale)", 0, now.Add(-11 * time.Minute), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &UserService{now: func() time.Time { return now }, maxAuthAge: tc.maxAge}
			err := s.RequireFreshAuth(authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, AuthTime: tc.authTime}))
			if tc.wantStepUp != errors.Is(err, ErrStepUpRequired) {
				t.Fatalf("RequireFreshAuth = %v, want step-up %v", err, tc.wantStepUp)
			}
			if !tc.wantStepUp && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	s := &UserService{now: func() time.Time { return now }}
	if err := s.RequireFreshAuth(context.Background()); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Fatalf("no AuthContext = %v, want unauthenticated", err)
	}
}

func TestSelfCallerAuthority(t *testing.T) {
	t.Parallel()
	session := opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3}
	tests := []struct {
		name       string
		ctx        context.Context
		wantReader error
		wantWriter error
	}{
		{"no AuthContext", context.Background(), apiresp.ErrUnauthenticated, apiresp.ErrUnauthenticated},
		{"anonymous", authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, Anonymous: true}), apiresp.ErrUnauthenticated, apiresp.ErrUnauthenticated},
		{"no acting profile", authCtx(opctx.AuthContext{PrincipalID: 1, SessionID: 3}), apiresp.ErrUnauthenticated, apiresp.ErrUnauthenticated},
		{"client credentials (no session)", authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, CredentialID: 9}), apiresp.ErrForbidden, apiresp.ErrForbidden},
		{"interactive session", authCtx(session), nil, nil},
		{"impersonating: reads allowed, writes refused", authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, ImpersonatorPrincipalID: 7}), nil, apiresp.ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := selfReader(tc.ctx); !errors.Is(err, tc.wantReader) || (tc.wantReader == nil && err != nil) {
				t.Fatalf("selfReader = %v, want %v", err, tc.wantReader)
			}
			if _, err := selfWriter(tc.ctx); !errors.Is(err, tc.wantWriter) || (tc.wantWriter == nil && err != nil) {
				t.Fatalf("selfWriter = %v, want %v", err, tc.wantWriter)
			}
		})
	}
}

func TestSelfAuthorityComesFromTheContextNotTheCaller(t *testing.T) {
	t.Parallel()
	// The target of every Self* method is the AuthContext principal; there is
	// no argument to name another one.
	ac, err := selfWriter(authCtx(opctx.AuthContext{PrincipalID: 42, ProfileID: 2, SessionID: 3}))
	if err != nil || ac.PrincipalID != 42 {
		t.Fatalf("selfWriter = %+v, %v", ac, err)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	blank, ok := "   ", "  Ada "
	long := strings.Repeat("x", maxNameRunes+1)
	if got, err := validateName("given_name", nil); got != "" || err != nil {
		t.Fatalf("nil = %q, %v", got, err)
	}
	if got, err := validateName("given_name", &ok); got != "Ada" || err != nil {
		t.Fatalf("ok = %q, %v", got, err)
	}
	if _, err := validateName("given_name", &blank); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Fatalf("blank = %v", err)
	}
	if _, err := validateName("given_name", &long); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Fatalf("long = %v", err)
	}
}

func TestProfileDisplayName(t *testing.T) {
	t.Parallel()
	if got := profileDisplayName("Ada Lovelace", false, ""); got != "Ada Lovelace (Personal)" {
		t.Fatalf("personal = %q", got)
	}
	if got := profileDisplayName("Ada Lovelace", true, "Acme Inc"); got != "Ada Lovelace @ Acme Inc" {
		t.Fatalf("party = %q", got)
	}
}

type fixedAuthorizer struct{ err error }

func (f fixedAuthorizer) Authorize(context.Context, string, *int64) error { return f.err }

func TestAuthorizeMasked(t *testing.T) {
	t.Parallel()
	stepUp := ErrStepUpRequired
	other := errors.New("db down")
	tests := []struct {
		name         string
		az           fixedAuthorizer
		noAuthorizer bool
		want         error
	}{
		{"allowed", fixedAuthorizer{}, false, nil},
		{"forbidden is masked as not found", fixedAuthorizer{err: apiresp.ErrForbidden}, false, apiresp.ErrNotFound},
		{"unauthenticated stays", fixedAuthorizer{err: apiresp.ErrUnauthenticated}, false, apiresp.ErrUnauthenticated},
		{"step-up action is not masked", fixedAuthorizer{err: stepUp}, false, stepUp},
		{"other errors stay", fixedAuthorizer{err: other}, false, other},
		{"no authorizer fails closed", fixedAuthorizer{}, true, apiresp.ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &UserService{}
			if !tc.noAuthorizer {
				s.az = tc.az
			}
			err := s.authorizeMasked(context.Background(), "read", 5)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestListPrincipalsValidatesBeforeTouchingState(t *testing.T) {
	t.Parallel()
	s := &UserService{}
	ctx := authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3})
	if _, err := s.ListPrincipals(ctx, ListPrincipalsInput{Kind: "robot"}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Fatalf("bad kind = %v", err)
	}
	if _, err := s.ListPrincipals(context.Background(), ListPrincipalsInput{}); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Fatalf("no auth = %v", err)
	}
	// A well-formed request on an unwired service is a configuration error,
	// not a panic.
	if _, err := s.ListPrincipals(ctx, ListPrincipalsInput{}); err == nil {
		t.Fatal("unwired service answered a list")
	}
}

func TestPrincipalResource(t *testing.T) {
	t.Parallel()
	if got := principalResource(PrincipalKindUser); got != resourceUser {
		t.Fatalf("user = %q", got)
	}
	if got := principalResource(PrincipalKindServiceAccount); got != resourceServiceAccount {
		t.Fatalf("service account = %q", got)
	}
}
