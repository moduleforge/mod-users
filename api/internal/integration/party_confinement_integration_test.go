//go:build integration

package integration

// Who may confine a credential to a party, and who may clear that: the
// administrator door of a confinement reaches only party-controlled credentials
// (those of a managed user of the party, and OIDC credentials of a provider
// bound to the party), the holder door of a clear is refused for those, and the
// credential's holder must hold an active profile in the party.

import (
	"context"
	"net/http"
	"testing"
)

// boundOIDCCredential gives the user an active OIDC credential of a new
// provider bound to the party and returns the credential's uuid.
func (s *stack) boundOIDCCredential(t *testing.T, userID, partyID int64) string {
	t.Helper()
	provider := "bound-" + uniq("idp")
	s.exec(t, "bound provider", `INSERT INTO mod_users.oidc_providers (id, party_id) VALUES ($1, $2)`, provider, partyID)
	return s.credentialUUIDByID(t, s.addOIDCCredential(t, userID, provider))
}

func (s *stack) credentialUUIDByID(t *testing.T, credID int64) string {
	t.Helper()
	var u string
	if err := s.pool.QueryRow(context.Background(), `SELECT uuid::text FROM mod_users.credentials WHERE id = $1`, credID).Scan(&u); err != nil {
		t.Fatalf("credential uuid: %v", err)
	}
	return u
}

// principalCredentialUUID returns the uuid of the principal's live credential
// of the kind.
func (s *stack) principalCredentialUUID(t *testing.T, principalID int64, kind string) string {
	t.Helper()
	var u string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT uuid::text FROM mod_users.credentials WHERE principal_id = $1 AND kind = $2 AND revoked_at IS NULL`,
		principalID, kind).Scan(&u); err != nil {
		t.Fatalf("%s credential of %d: %v", kind, principalID, err)
	}
	return u
}

func (s *stack) revoked(t *testing.T, credUUID string) bool {
	t.Helper()
	return s.count(t, "revoked", `SELECT count(*) FROM mod_users.credentials WHERE uuid = $1::uuid AND revoked_at IS NOT NULL`, credUUID) == 1
}

// managedMember is a managed user of the party holding an active profile.
type managedMember struct {
	userID    int64
	email     string
	profileID int64
	profUUID  string
}

func (s *stack) newManagedMember(t *testing.T, g gateParty) managedMember {
	t.Helper()
	m := managedMember{email: uniq("managed") + "@example.test"}
	m.userID = s.seedManagedUser(t, g.id, m.email, testPassword)
	m.profileID, m.profUUID = s.seedPartyProfile(t, m.userID, g.id, "active")
	return m
}

// twoFactorPartySession signs the managed user in with the password, steps up
// with an emailed code (an email_otp credential is added first) and acts as the
// party profile: a session the holder door accepts.
func (s *stack) twoFactorPartySession(t *testing.T, m managedMember) tokens {
	t.Helper()
	identID := s.scanInt64(t, "identifier", `SELECT id FROM mod_users.user_identifiers WHERE user_id = $1 AND kind = 'email'`, m.userID)
	otpID := s.scanInt64(t, "email_otp credential",
		`INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, 'email_otp') RETURNING id`, m.userID)
	s.exec(t, "cred_email_otp", `INSERT INTO mod_users.cred_email_otp (credential_id, identifier_id) VALUES ($1, $2)`, otpID, identID)

	tk := s.loginRaw(t, m.email, testPassword, nil).wantStatus(t, http.StatusOK).tok(t)
	seen := s.mail.count(m.email)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", tk.access(), map[string]string{"email": m.email}).
		wantStatus(t, http.StatusNoContent)
	code := s.mail.codeAfter(t, m.email, seen)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", tk.access(),
		map[string]string{"email": m.email, "code": code}).wantStatus(t, http.StatusOK)
	return s.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": m.profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
}

// wantHidden fails unless the response is a 403 or 404 refusal.
func (r *resp) wantHidden(t *testing.T) {
	t.Helper()
	if r.status != http.StatusForbidden && r.status != http.StatusNotFound {
		t.Fatalf("status = %d, want a 403 or 404 refusal; body=%s", r.status, r.raw)
	}
}

func TestConfinement_AdministratorCannotConfineAnOutsideCollaboratorsPersonalCredential(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	pw := env.credentialUUID(t, m.acct.tk.access(), "password")

	env.do(t, http.MethodPut, confPath(g, pw), g.adminTok.access(), nil).wantHidden(t)
	if got := env.confinedTo(t, pw); got != 0 {
		t.Fatalf("the administrator confined a personal credential to %d", got)
	}
	// The same refusal as for an unknown credential.
	env.do(t, http.MethodPut, confPath(g, "00000000-0000-4000-8000-000000000001"), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
}

func TestConfinement_AdministratorConfinesPartyControlledCredentials(t *testing.T) {
	g := env.newGateParty(t)

	mm := env.newManagedMember(t, g)
	managedPw := env.principalCredentialUUID(t, mm.userID, "password")
	env.do(t, http.MethodPut, confPath(g, managedPw), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.confinedTo(t, managedPw); got != g.id {
		t.Errorf("managed credential confined to %d, want %d", got, g.id)
	}

	m := env.newGateMember(t, g)
	oidc := env.boundOIDCCredential(t, m.userID, g.id)
	env.do(t, http.MethodPut, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.confinedTo(t, oidc); got != g.id {
		t.Errorf("bound-provider credential confined to %d, want %d", got, g.id)
	}

	// An OIDC credential of an unbound provider is not the party's.
	unbound := env.addProviderRow(t, "free-"+uniq("idp"))
	unboundUUID := env.credentialUUIDByID(t, env.addOIDCCredential(t, m.userID, unbound))
	env.do(t, http.MethodPut, confPath(g, unboundUUID), g.adminTok.access(), nil).wantHidden(t)
	if got := env.confinedTo(t, unboundUUID); got != 0 {
		t.Errorf("unbound-provider credential confined to %d", got)
	}
	// Nor is a provider bound to another party.
	other := env.newGateParty(t)
	otherBound := env.boundOIDCCredential(t, m.userID, other.id)
	env.do(t, http.MethodPut, confPath(g, otherBound), g.adminTok.access(), nil).wantHidden(t)
}

func TestConfinement_HolderDoorClearsOnlyANonPartyControlledCredential(t *testing.T) {
	g := env.newGateParty(t)

	// A BYO member: a personal credential and a bound-provider OIDC credential.
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	pw := env.credentialUUID(t, m.acct.tk.access(), "password")
	oidc := env.boundOIDCCredential(t, m.userID, g.id)
	env.do(t, http.MethodPut, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	tok := env.withEmailFactor(t, m)

	// The holder confines and clears the confinement of the personal credential.
	env.do(t, http.MethodPut, confPath(g, pw), tok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.confinedTo(t, pw); got != g.id {
		t.Fatalf("personal credential confined to %d, want %d", got, g.id)
	}
	env.do(t, http.MethodDelete, confPath(g, pw), tok.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := env.confinedTo(t, pw); got != 0 {
		t.Errorf("personal credential confined to %d after the holder cleared it", got)
	}
	// The bound-provider credential stays confined.
	env.do(t, http.MethodDelete, confPath(g, oidc), tok.access(), nil).wantHidden(t)
	if got := env.confinedTo(t, oidc); got != g.id {
		t.Errorf("bound-provider credential confined to %d after a holder clear, want %d", got, g.id)
	}
	// The administrator door still clears it.
	env.do(t, http.MethodDelete, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusNoContent)

	// A managed user's credential.
	mm := env.newManagedMember(t, g)
	managedPw := env.principalCredentialUUID(t, mm.userID, "password")
	env.do(t, http.MethodPut, confPath(g, managedPw), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	mtok := env.twoFactorPartySession(t, mm)
	env.do(t, http.MethodDelete, confPath(g, managedPw), mtok.access(), nil).wantHidden(t)
	if got := env.confinedTo(t, managedPw); got != g.id {
		t.Errorf("managed credential confined to %d after a holder clear, want %d", got, g.id)
	}
}

func TestConfinement_AnInvitedHolderIsNotTheSubjectOfACredentialWrite(t *testing.T) {
	g := env.newGateParty(t)
	a := env.register(t)
	userID := env.principalID(t, a.Email)
	profileID, _ := env.seedPartyProfile(t, userID, g.id, "invited")
	pw := env.credentialUUID(t, a.tk.access(), "password")
	oidc := env.boundOIDCCredential(t, userID, g.id)

	env.do(t, http.MethodPut, authPath(g, pw), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodPut, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	if n := env.authorizationCount(t, pw, g.id); n != 0 {
		t.Errorf("an invited holder's credential was authorized (%d rows)", n)
	}
	if got := env.confinedTo(t, oidc); got != 0 {
		t.Errorf("an invited holder's credential was confined to %d", got)
	}

	// Once the profile is active, both writes land.
	env.exec(t, "activate", `UPDATE profiles SET status = 'active', started_at = now() WHERE entity_id = $1`, profileID)
	env.do(t, http.MethodPut, authPath(g, pw), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
}

func TestConfinement_DepartureStillRevokesEveryConfinedCredential(t *testing.T) {
	g := env.newGateParty(t)
	ctx := env.authCtx(t, g.adminTok.access())

	// A BYO member with a bound-provider OIDC credential the holder could not
	// unconfine, and a personal credential it confined itself.
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	pw := env.credentialUUID(t, m.acct.tk.access(), "password")
	oidc := env.boundOIDCCredential(t, m.userID, g.id)
	env.do(t, http.MethodPut, confPath(g, oidc), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	tok := env.withEmailFactor(t, m)
	env.do(t, http.MethodPut, confPath(g, pw), tok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodDelete, confPath(g, oidc), tok.access(), nil).wantHidden(t)

	// A managed user with a confined credential.
	mm := env.newManagedMember(t, g)
	managedPw := env.principalCredentialUUID(t, mm.userID, "password")
	env.do(t, http.MethodPut, confPath(g, managedPw), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)

	for _, profileID := range []int64{m.profileID, mm.profileID} {
		if err := env.inTx(ctx, func(tx pgxTx) error { return env.profiles.EndInTx(ctx, tx, profileID, "left") }); err != nil {
			t.Fatalf("EndInTx(%d): %v", profileID, err)
		}
	}
	for name, c := range map[string]string{"personal": pw, "bound-provider oidc": oidc, "managed": managedPw} {
		if !env.revoked(t, c) {
			t.Errorf("the %s credential confined to the party survived the holder's departure", name)
		}
	}
}
