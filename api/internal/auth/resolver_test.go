package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// fakeResolverQuerier is an in-memory ResolverQuerier.
type fakeResolverQuerier struct {
	oidc   map[[2]string]usersdb.Credential // (issuer, subject) -> credential
	emails map[string]bool                  // existing email identifiers
	err    error
}

func (f fakeResolverQuerier) GetActiveOIDCByIssuerSubject(_ context.Context, arg usersdb.GetActiveOIDCByIssuerSubjectParams) (usersdb.GetActiveOIDCByIssuerSubjectRow, error) {
	if f.err != nil {
		return usersdb.GetActiveOIDCByIssuerSubjectRow{}, f.err
	}
	c, ok := f.oidc[[2]string{arg.Issuer, arg.Subject}]
	if !ok {
		return usersdb.GetActiveOIDCByIssuerSubjectRow{}, pgx.ErrNoRows
	}
	return usersdb.GetActiveOIDCByIssuerSubjectRow{Credential: c, Issuer: arg.Issuer, Subject: arg.Subject}, nil
}

func (f fakeResolverQuerier) GetIdentifierByKindValue(_ context.Context, arg usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error) {
	if arg.Kind == "email" && f.emails[arg.ValueCanon] {
		return usersdb.UserIdentifier{ID: 1, Kind: arg.Kind, ValueCanon: arg.ValueCanon}, nil
	}
	return usersdb.UserIdentifier{}, pgx.ErrNoRows
}

func TestOIDCResolver_Resolve(t *testing.T) {
	known := usersdb.Credential{ID: 7, PrincipalID: 42, Kind: "oidc"}
	q := fakeResolverQuerier{
		oidc:   map[[2]string]usersdb.Credential{{"https://idp", "known"}: known},
		emails: map[string]bool{"taken@example.com": true},
	}
	byo := ProviderPolicy{ProviderID: "p", JITMode: config.JITModeBYO}
	managed := ProviderPolicy{ProviderID: "p", JITMode: config.JITModeManaged, PartyID: 9}
	none := ProviderPolicy{ProviderID: "p", JITMode: config.JITModeNone}

	tests := []struct {
		name        string
		id          ExternalIdentity
		policy      ProviderPolicy
		wantAction  Action
		wantRefusal string
	}{
		{"known identity logs in", ExternalIdentity{Issuer: "https://idp", Subject: "known", Email: "x@example.com"}, byo, ActionLogin, ""},
		{"known identity logs in without email", ExternalIdentity{Issuer: "https://idp", Subject: "known"}, none, ActionLogin, ""},
		{"known identity beats an unrelated email match", ExternalIdentity{Issuer: "https://idp", Subject: "known", Email: "taken@example.com"}, byo, ActionLogin, ""},
		{"unknown identity with a taken email is never linked", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "taken@example.com"}, byo, ActionRefuse, RedirectErrAccountExists},
		{"email match is case and space insensitive", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: " Taken@Example.com "}, byo, ActionRefuse, RedirectErrAccountExists},
		{"taken email under jit none still points at sign-in", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "taken@example.com"}, none, ActionRefuse, RedirectErrAccountExists},
		{"taken email under managed is not linked either", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "taken@example.com"}, managed, ActionRefuse, RedirectErrAccountExists},
		{"unknown identity under jit none", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "free@example.com"}, none, ActionRefuse, RedirectErrNoAccount},
		{"byo creates a user", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "free@example.com"}, byo, ActionCreateBYO, ""},
		{"byo without an email", ExternalIdentity{Issuer: "https://idp", Subject: "new"}, byo, ActionRefuse, RedirectErrMissingEmail},
		{"managed creates a managed user", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "free@example.com"}, managed, ActionCreateManaged, ""},
		{"managed needs no email", ExternalIdentity{Issuer: "https://idp", Subject: "new"}, managed, ActionCreateManaged, ""},
		{"managed without a party fails closed", ExternalIdentity{Issuer: "https://idp", Subject: "new"}, ProviderPolicy{JITMode: config.JITModeManaged}, ActionRefuse, RedirectErrNoAccount},
		{"unknown jit mode fails closed", ExternalIdentity{Issuer: "https://idp", Subject: "new", Email: "free@example.com"}, ProviderPolicy{JITMode: "bogus"}, ActionRefuse, RedirectErrNoAccount},
		{"missing subject", ExternalIdentity{Issuer: "https://idp"}, byo, ActionRefuse, RedirectErrAuthFailed},
	}
	r := NewOIDCResolver(q)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), tc.id, tc.policy)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Action != tc.wantAction || got.Refusal != tc.wantRefusal {
				t.Fatalf("got action %d refusal %q, want %d %q", got.Action, got.Refusal, tc.wantAction, tc.wantRefusal)
			}
			if tc.wantAction == ActionLogin && got.Credential.ID != known.ID {
				t.Fatalf("login credential = %d, want %d", got.Credential.ID, known.ID)
			}
		})
	}
}

func TestOIDCResolver_Resolve_FaultsAreErrors(t *testing.T) {
	boom := errors.New("db down")
	r := NewOIDCResolver(fakeResolverQuerier{err: boom})
	if _, err := r.Resolve(context.Background(), ExternalIdentity{Issuer: "i", Subject: "s"}, ProviderPolicy{JITMode: config.JITModeBYO}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
}

func TestNewProviderPolicy(t *testing.T) {
	tests := []struct {
		name     string
		p        config.Provider
		optOut   bool
		wantMode string
	}{
		{"default is byo", config.Provider{}, false, config.JITModeBYO},
		{"byo capped by NO_OIDC_ACCOUNTS", config.Provider{JITMode: config.JITModeBYO}, true, config.JITModeNone},
		{"managed survives NO_OIDC_ACCOUNTS", config.Provider{JITMode: config.JITModeManaged, PartyID: 3}, true, config.JITModeManaged},
		{"none stays none", config.Provider{JITMode: config.JITModeNone}, false, config.JITModeNone},
		{"unknown fails closed", config.Provider{JITMode: "x"}, false, config.JITModeNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewProviderPolicy(tc.p, tc.optOut).JITMode; got != tc.wantMode {
				t.Fatalf("JITMode = %q, want %q", got, tc.wantMode)
			}
		})
	}
}

func TestProviderPolicy_AssertedACR(t *testing.T) {
	id := ExternalIdentity{AssertedACR: 2}
	if got := (ProviderPolicy{TrustACR: false}).AssertedACR(id); got != 0 {
		t.Fatalf("untrusted provider asserted acr = %d, want 0", got)
	}
	if got := (ProviderPolicy{TrustACR: true}).AssertedACR(id); got != 2 {
		t.Fatalf("trusted provider asserted acr = %d, want 2", got)
	}
}
