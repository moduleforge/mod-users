package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/moduleforge/mod-users/model/db"
)

// newResolverWithStub builds a UserResolver whose only moving part is a stub
// uuidLookup. Pool and queries are intentionally nil — the tests below only
// exercise the local-issuer fast path, which must not reach for either.
func newResolverWithStub(t *testing.T, localIssuer string, lookup uuidLookupFn) *UserResolver {
	t.Helper()
	return &UserResolver{
		pool:        nil,
		queries:     nil,
		localIssuer: localIssuer,
		uuidLookup:  lookup,
	}
}

// newOIDCResolver builds a UserResolver wired with injectable function stubs
// for the OIDC path. pool, queries, coreQ are all nil; no DB is needed.
func newOIDCResolver(t *testing.T,
	oidcLookup oidcIdentityLookupFn,
	emailLookup emailAccountLookupFn,
	idLookup idAccountLookupFn,
	linkFn linkIdentityFn,
	vlFn verifyAndLinkFn,
	acFn autoCreateFn,
) *UserResolver {
	t.Helper()
	return &UserResolver{
		pool:               nil,
		queries:            nil,
		localIssuer:        "users-module-local",
		oidcIdentityLookup: oidcLookup,
		emailAccountLookup: emailLookup,
		idAccountLookup:    idLookup,
		linkIdentityFn:     linkFn,
		verifyAndLinkFn:    vlFn,
		autoCreateFn:       acFn,
	}
}

// -------------------------------------------------------------------------
// Local-issuer fast path tests
// -------------------------------------------------------------------------

func TestResolver_LocalIssuerFastPath_Success(t *testing.T) {
	wantUUID := uuid.New()
	wantUser := db.UserAccount{
		ID:            42,
		Uuid:          wantUUID,
		AccountHolder: 41,
		Email:         pgtype.Text{String: "alice@example.com", Valid: true},
	}

	calls := 0
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		calls++
		if u != wantUUID {
			t.Errorf("uuidLookup got %s, want %s", u, wantUUID)
		}
		return wantUser, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: wantUUID.String(),
		Issuer:  "users-module-local",
		Email:   "", // local JWT does not carry email
	}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if calls != 1 {
		t.Errorf("uuidLookup call count = %d, want 1", calls)
	}
	if uc.UserAccountID != wantUser.ID {
		t.Errorf("UserAccountID = %d, want %d", uc.UserAccountID, wantUser.ID)
	}
	if uc.UserUUID != wantUUID.String() {
		t.Errorf("UserUUID = %q, want %q", uc.UserUUID, wantUUID.String())
	}
	if uc.Email != wantUser.Email.String {
		t.Errorf("Email = %q, want %q", uc.Email, wantUser.Email.String)
	}
}

func TestResolver_LocalIssuerFastPath_DeletedUser(t *testing.T) {
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		return db.UserAccount{}, pgx.ErrNoRows
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: uuid.New().String(),
		Issuer:  "users-module-local",
	}
	_, err := r.Resolve(context.Background(), p)
	if err == nil {
		t.Fatal("expected ErrUserGone, got nil")
	}
	if !errors.Is(err, ErrUserGone) {
		t.Errorf("expected ErrUserGone, got %v", err)
	}
}

func TestResolver_LocalIssuerFastPath_BadSubject(t *testing.T) {
	// uuidLookup should never be called if sub is not a valid UUID.
	called := false
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		called = true
		return db.UserAccount{}, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: "not-a-uuid",
		Issuer:  "users-module-local",
	}
	_, err := r.Resolve(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for non-UUID subject")
	}
	if called {
		t.Error("uuidLookup should not be called for malformed subject")
	}
}

// TestResolver_OIDCPath_FastPathSkipped confirms that a principal whose issuer
// is something other than LocalIssuer does not take the fast path. We stub the
// fast path to fail loudly if it runs, then watch Resolve fall through to the
// OIDC code — which in turn fails because queries is nil. The specific failure
// mode isn't the point; the point is that fast path gets skipped.
func TestResolver_OIDCPath_FastPathSkipped(t *testing.T) {
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		t.Fatal("local-issuer fast path must not run for non-local issuer")
		return db.UserAccount{}, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: "google-sub-123",
		Issuer:  "https://accounts.google.com",
		Email:   "user@example.com",
	}

	// Resolve will panic/fail inside the OIDC path because queries is nil.
	// We recover so the test passes as long as the lookup stub was not called.
	defer func() {
		_ = recover()
	}()
	_, _ = r.Resolve(context.Background(), p)
}

// -------------------------------------------------------------------------
// 5-branch OIDC path tests
// -------------------------------------------------------------------------

// fakeAccount is a convenient helper for building test UserAccount rows.
func fakeAccount(id int64, email string, verified bool) db.UserAccount {
	ua := db.UserAccount{
		ID:            id,
		Uuid:          uuid.New(),
		AccountHolder: id * 10,
		Email:         pgtype.Text{String: email, Valid: email != ""},
	}
	if verified {
		t := time.Now()
		ua.EmailVerifiedAt = &t
	}
	return ua
}

// fakeIdentity builds a minimal AuthOidcIdentity pointing at userAccountID.
func fakeIdentity(userAccountID int64, issuer, subject string) db.AuthOidcIdentity {
	return db.AuthOidcIdentity{
		ID:            1,
		Uuid:          uuid.New(),
		UserAccountID: userAccountID,
		Issuer:        issuer,
		Subject:       subject,
	}
}

// TestResolver_Branch1_ExistingIdentity covers Branch 1: (issuer, subject) already known.
func TestResolver_Branch1_ExistingIdentity(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-001"
	ua := fakeAccount(10, "alice@example.com", true)
	identity := fakeIdentity(ua.ID, issuer, subject)

	r := newOIDCResolver(t,
		// oidcIdentityLookup: return the existing identity
		func(_ context.Context, iss, sub string) (db.AuthOidcIdentity, error) {
			if iss == issuer && sub == subject {
				return identity, nil
			}
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: should not be called
		func(_ context.Context, _ string) (db.UserAccount, error) {
			t.Error("Branch 1 must not call emailAccountLookup")
			return db.UserAccount{}, pgx.ErrNoRows
		},
		// idAccountLookup: return the user account
		func(_ context.Context, id int64) (db.UserAccount, error) {
			if id == ua.ID {
				return ua, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil, nil, nil,
	)

	p := Principal{Issuer: issuer, Subject: subject, Email: ua.Email.String}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Branch 1: unexpected error: %v", err)
	}
	if uc.UserAccountID != ua.ID {
		t.Errorf("Branch 1: UserAccountID = %d, want %d", uc.UserAccountID, ua.ID)
	}
}

// TestResolver_Branch2_VerifiedEmailMatch covers Branch 2: email match, account verified.
func TestResolver_Branch2_VerifiedEmailMatch(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-new"
	ua := fakeAccount(20, "bob@example.com", true)

	linkCalled := false

	r := newOIDCResolver(t,
		// oidcIdentityLookup: no row yet
		func(_ context.Context, _, _ string) (db.AuthOidcIdentity, error) {
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: return verified account
		func(_ context.Context, email string) (db.UserAccount, error) {
			if email == ua.Email.String {
				return ua, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil, // idAccountLookup not needed for branch 2 return
		// linkIdentityFn: branch 2 action
		func(_ context.Context, _ db.UserAccount, _ Principal, _ int64) error {
			linkCalled = true
			return nil
		},
		nil, nil,
	)

	p := Principal{Issuer: issuer, Subject: subject, Email: ua.Email.String, EmailVerified: false}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Branch 2: unexpected error: %v", err)
	}
	if !linkCalled {
		t.Error("Branch 2: linkIdentityFn was not called")
	}
	if uc.UserAccountID != ua.ID {
		t.Errorf("Branch 2: UserAccountID = %d, want %d", uc.UserAccountID, ua.ID)
	}
}

// TestResolver_Branch3_UnverifiedEmailIdPVerified covers Branch 3: unverified account +
// IdP asserts email_verified=true → verify + link.
func TestResolver_Branch3_UnverifiedEmailIdPVerified(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-003"
	ua := fakeAccount(30, "carol@example.com", false)
	// After verifyAndLink, the account gains email_verified_at.
	verifiedUA := ua
	now := time.Now()
	verifiedUA.EmailVerifiedAt = &now

	vlCalled := false

	r := newOIDCResolver(t,
		// oidcIdentityLookup: no row yet
		func(_ context.Context, _, _ string) (db.AuthOidcIdentity, error) {
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: return unverified account
		func(_ context.Context, email string) (db.UserAccount, error) {
			if email == ua.Email.String {
				return ua, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		// idAccountLookup: return the "after-verify" account for the reload
		func(_ context.Context, id int64) (db.UserAccount, error) {
			if id == ua.ID {
				return verifiedUA, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil,
		// verifyAndLinkFn: branch 3 action
		func(_ context.Context, _ db.UserAccount, _ Principal, _ int64) error {
			vlCalled = true
			return nil
		},
		nil,
	)

	p := Principal{Issuer: issuer, Subject: subject, Email: ua.Email.String, EmailVerified: true}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Branch 3: unexpected error: %v", err)
	}
	if !vlCalled {
		t.Error("Branch 3: verifyAndLinkFn was not called")
	}
	if uc.UserAccountID != ua.ID {
		t.Errorf("Branch 3: UserAccountID = %d, want %d", uc.UserAccountID, ua.ID)
	}
}

// TestResolver_Branch4_UnverifiedEmailIdPNotVerified covers Branch 4: unverified account +
// IdP also not verified → ErrUnverifiedTakeover.
func TestResolver_Branch4_UnverifiedTakeover(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-004"
	ua := fakeAccount(40, "dave@example.com", false)

	r := newOIDCResolver(t,
		// oidcIdentityLookup: no row yet
		func(_ context.Context, _, _ string) (db.AuthOidcIdentity, error) {
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: return unverified account
		func(_ context.Context, email string) (db.UserAccount, error) {
			if email == ua.Email.String {
				return ua, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil, nil, nil, nil,
	)

	p := Principal{Issuer: issuer, Subject: subject, Email: ua.Email.String, EmailVerified: false}
	_, err := r.Resolve(context.Background(), p)
	if err == nil {
		t.Fatal("Branch 4: expected ErrUnverifiedTakeover, got nil")
	}
	if !errors.Is(err, ErrUnverifiedTakeover) {
		t.Errorf("Branch 4: expected ErrUnverifiedTakeover, got %v", err)
	}
}

// TestResolver_Branch5_NewUser covers Branch 5: no existing account → auto-create.
func TestResolver_Branch5_NewUser(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-005"
	email := "eve@example.com"
	createdUA := fakeAccount(50, email, false)

	acCalled := false

	r := newOIDCResolver(t,
		// oidcIdentityLookup: no row
		func(_ context.Context, _, _ string) (db.AuthOidcIdentity, error) {
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: no existing account
		func(_ context.Context, _ string) (db.UserAccount, error) {
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil, nil, nil,
		// autoCreateFn: branch 5 action
		func(_ context.Context, p Principal) (db.UserAccount, error) {
			acCalled = true
			if p.Email != email {
				return db.UserAccount{}, errors.New("unexpected email")
			}
			return createdUA, nil
		},
	)

	p := Principal{Issuer: issuer, Subject: subject, Email: email, EmailVerified: false}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Branch 5: unexpected error: %v", err)
	}
	if !acCalled {
		t.Error("Branch 5: autoCreateFn was not called")
	}
	if uc.UserAccountID != createdUA.ID {
		t.Errorf("Branch 5: UserAccountID = %d, want %d", uc.UserAccountID, createdUA.ID)
	}
}

// -------------------------------------------------------------------------
// Email canonicalization
// -------------------------------------------------------------------------

// TestAutoCreateEmailFields proves that autoCreate's email-derivation helper
// canonicalizes (trims + lowercases) an IdP-asserted email exactly like
// every other write path to user_accounts.email (see
// internal/service/user_accounts.go's Create and the auth handlers'
// register/reset/emailcode/login request normalization) before it is
// persisted or used to derive given_name. autoCreate itself requires a live
// Postgres connection for its surrounding transaction (it is only ever
// exercised indirectly here via the autoCreateFn stub), so this pure helper
// is where the canonicalization logic is unit-tested.
func TestAutoCreateEmailFields(t *testing.T) {
	tests := []struct {
		name          string
		rawEmail      string
		wantEmail     string
		wantGivenName string
	}{
		{
			name:          "mixed case is lowercased",
			rawEmail:      "Alice.Example@Example.COM",
			wantEmail:     "alice.example@example.com",
			wantGivenName: "alice.example",
		},
		{
			name:          "surrounding whitespace is trimmed",
			rawEmail:      "  bob@example.com  ",
			wantEmail:     "bob@example.com",
			wantGivenName: "bob",
		},
		{
			name:          "mixed case and whitespace together",
			rawEmail:      "  Carol.Example@Example.COM ",
			wantEmail:     "carol.example@example.com",
			wantGivenName: "carol.example",
		},
		{
			name:          "already canonical is unchanged",
			rawEmail:      "dave@example.com",
			wantEmail:     "dave@example.com",
			wantGivenName: "dave",
		},
		{
			name:          "no @ falls back to the full canonical email",
			rawEmail:      " NotAnEmail ",
			wantEmail:     "notanemail",
			wantGivenName: "notanemail",
		},
		{
			name:          "empty email stays empty",
			rawEmail:      "",
			wantEmail:     "",
			wantGivenName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEmail, gotGivenName := autoCreateEmailFields(tt.rawEmail)
			if gotEmail != tt.wantEmail {
				t.Errorf("autoCreateEmailFields(%q) email = %q, want %q", tt.rawEmail, gotEmail, tt.wantEmail)
			}
			if gotGivenName != tt.wantGivenName {
				t.Errorf("autoCreateEmailFields(%q) givenName = %q, want %q", tt.rawEmail, gotGivenName, tt.wantGivenName)
			}
		})
	}
}

// TestResolver_Branch2_CanonicalizesEmailForLookup proves that Resolve
// canonicalizes (trims + lowercases) the principal's email before using it
// to look up an existing account by email. user_accounts.email is stored
// canonicalized by every write path, so a raw IdP claim with mismatched
// case or surrounding whitespace must still match an existing account
// rather than incorrectly falling through to auto-create (branch 5).
func TestResolver_Branch2_CanonicalizesEmailForLookup(t *testing.T) {
	issuer := "https://accounts.google.com"
	subject := "google-sub-canon"
	ua := fakeAccount(21, "frank@example.com", true)

	var gotLookupEmail string
	linkCalled := false

	r := newOIDCResolver(t,
		// oidcIdentityLookup: no row yet
		func(_ context.Context, _, _ string) (db.AuthOidcIdentity, error) {
			return db.AuthOidcIdentity{}, pgx.ErrNoRows
		},
		// emailAccountLookup: records exactly what it was called with.
		func(_ context.Context, email string) (db.UserAccount, error) {
			gotLookupEmail = email
			if email == ua.Email.String {
				return ua, nil
			}
			return db.UserAccount{}, pgx.ErrNoRows
		},
		nil, // idAccountLookup not needed for branch 2 return
		// linkIdentityFn: branch 2 action
		func(_ context.Context, _ db.UserAccount, _ Principal, _ int64) error {
			linkCalled = true
			return nil
		},
		nil, nil,
	)

	// The IdP asserts the email with mismatched case and surrounding
	// whitespace; the stored account's email is already canonical.
	p := Principal{Issuer: issuer, Subject: subject, Email: "  Frank@Example.COM  ", EmailVerified: false}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotLookupEmail != ua.Email.String {
		t.Errorf("emailAccountLookup was called with %q, want canonicalized %q", gotLookupEmail, ua.Email.String)
	}
	if !linkCalled {
		t.Error("linkIdentityFn was not called — canonicalization failure caused the email match to be missed")
	}
	if uc.UserAccountID != ua.ID {
		t.Errorf("UserAccountID = %d, want %d", uc.UserAccountID, ua.ID)
	}
}
