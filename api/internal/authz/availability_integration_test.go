//go:build integration

package authz_test

// availability_integration_test.go proves mod_users.profile_availability and
// mod_users.credential_profile_availability (through the shared
// profile_availability_for rule): every lock reason, confinement over every
// factor, the credential_rule values, the outside-collaborator confirmation
// that exempts the credential rule but never min_acr, the personal-profile
// bypass of the party gate, and impersonation evaluated on the parent
// session's factors (operators skip only the credential rule).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func sessionAvail(t *testing.T, sessionID, profileID int64) string {
	t.Helper()
	var s string
	if err := integPool.QueryRow(context.Background(), `SELECT mod_users.profile_availability($1, $2)`, sessionID, profileID).Scan(&s); err != nil {
		t.Fatalf("profile_availability: %v", err)
	}
	return s
}

func credAvail(t *testing.T, credID, profileID int64) string {
	t.Helper()
	var s string
	if err := integPool.QueryRow(context.Background(), `SELECT mod_users.credential_profile_availability($1, $2)`, credID, profileID).Scan(&s); err != nil {
		t.Fatalf("credential_profile_availability: %v", err)
	}
	return s
}

func wantAvail(t *testing.T, got, want, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: availability %q, want %q", what, got, want)
	}
}

// member is a bring-your-own user with a personal profile and an active profile
// in a party.
type member struct {
	User, Personal, PartyProfile, Party int64
}

func seedMember(t *testing.T, party int64) member {
	t.Helper()
	m := member{Party: party}
	fxMustTx(t, func(tx pgx.Tx) {
		m.User, m.Personal = fxUser(t, tx, userOpts{})
		m.PartyProfile = fxPartyProfile(t, tx, m.User, party, "active")
	})
	return m
}

func confine(t *testing.T, credID, party int64) {
	t.Helper()
	fxExec(t, integPool, "confine credential", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, credID, party)
}

// sessionWith creates a session of the user holding the given credentials as
// factors, with the given stored acr.
func sessionWith(t *testing.T, user int64, acr int, creds ...int64) int64 {
	t.Helper()
	id, _ := fxSession(t, integPool, user, sessionOpts{ACR: acr})
	for _, c := range creds {
		fxFactor(t, integPool, id, c, nil)
	}
	return id
}

func TestInteg_Availability_LockReasons(t *testing.T) {
	party := seedOrg(t, "Avail party")
	m := seedMember(t, party)
	cred := fxPassword(t, integPool, m.User, nil)
	s := sessionWith(t, m.User, 1, cred)

	wantAvail(t, sessionAvail(t, s, m.Personal), "available", "personal profile")
	wantAvail(t, sessionAvail(t, s, m.PartyProfile), "available", "party profile, no policy")

	t.Run("not_held", func(t *testing.T) {
		other := seedMember(t, party)
		wantAvail(t, sessionAvail(t, s, other.Personal), "not_held", "another user's profile")
		wantAvail(t, sessionAvail(t, s, 999999999), "not_held", "unknown profile")
		wantAvail(t, sessionAvail(t, 999999999, m.Personal), "not_held", "unknown session")
		wantAvail(t, credAvail(t, cred, other.Personal), "not_held", "credential, another user's profile")
		wantAvail(t, credAvail(t, 999999999, m.Personal), "not_held", "unknown credential")
	})

	t.Run("inactive", func(t *testing.T) {
		mm := seedMember(t, party)
		c := fxPassword(t, integPool, mm.User, nil)
		ss := sessionWith(t, mm.User, 1, c)
		fxExec(t, integPool, "suspend party profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, mm.PartyProfile)
		wantAvail(t, sessionAvail(t, ss, mm.PartyProfile), "inactive", "suspended party profile")
		wantAvail(t, credAvail(t, c, mm.PartyProfile), "inactive", "credential, suspended party profile")
		fxExec(t, integPool, "suspend personal profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, mm.Personal)
		wantAvail(t, sessionAvail(t, ss, mm.Personal), "inactive", "suspended personal profile")
	})

	t.Run("invited profile is inactive", func(t *testing.T) {
		mm := seedMember(t, party)
		invitedParty := seedOrg(t, "Avail invite party")
		var invited int64
		fxMustTx(t, func(tx pgx.Tx) { invited = fxPartyProfile(t, tx, mm.User, invitedParty, "invited") })
		ss := sessionWith(t, mm.User, 1, fxPassword(t, integPool, mm.User, nil))
		wantAvail(t, sessionAvail(t, ss, invited), "inactive", "invited profile")
	})

	t.Run("principal_disabled", func(t *testing.T) {
		mm := seedMember(t, party)
		c := fxPassword(t, integPool, mm.User, nil)
		ss := sessionWith(t, mm.User, 1, c)
		fxExec(t, integPool, "disable principal", `UPDATE principals SET disabled_at = now() WHERE entity_id = $1`, mm.User)
		wantAvail(t, sessionAvail(t, ss, mm.Personal), "principal_disabled", "disabled principal, personal profile")
		wantAvail(t, sessionAvail(t, ss, mm.PartyProfile), "principal_disabled", "disabled principal, party profile")
		wantAvail(t, credAvail(t, c, mm.Personal), "principal_disabled", "credential, disabled principal")
	})
}

func TestInteg_Availability_ConfinementOverEveryFactor(t *testing.T) {
	partyA := seedOrg(t, "Confine A")
	partyB := seedOrg(t, "Confine B")
	m := seedMember(t, partyA)
	var inB int64
	fxMustTx(t, func(tx pgx.Tx) { inB = fxPartyProfile(t, tx, m.User, partyB, "active") })

	free := fxPassword(t, integPool, m.User, nil)
	confinedA := fxOIDC(t, integPool, m.User, "https://"+fxTag("iss"), "p", fxTag("sub"))
	confine(t, confinedA, partyA)

	// One confined factor among several confines the whole session.
	both := sessionWith(t, m.User, 2, free, confinedA)
	wantAvail(t, sessionAvail(t, both, m.PartyProfile), "available", "party A profile, factor confined to A")
	wantAvail(t, sessionAvail(t, both, inB), "confined", "party B profile, one factor confined to A")
	wantAvail(t, sessionAvail(t, both, m.Personal), "confined", "personal profile hidden by any confinement")

	onlyFree := sessionWith(t, m.User, 1, free)
	wantAvail(t, sessionAvail(t, onlyFree, inB), "available", "unconfined factor reaches party B")
	wantAvail(t, sessionAvail(t, onlyFree, m.Personal), "available", "unconfined factor reaches the personal profile")

	wantAvail(t, credAvail(t, confinedA, inB), "confined", "credential confined to A, B profile")
	wantAvail(t, credAvail(t, confinedA, m.Personal), "confined", "credential confined to A, personal profile")
	wantAvail(t, credAvail(t, confinedA, m.PartyProfile), "available", "credential confined to A, A profile")
}

func TestInteg_Availability_CredentialRuleAuthorized(t *testing.T) {
	party := seedOrg(t, "Authorized rule party")
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, party, "authorized", nil, 1) })
	m := seedMember(t, party)
	admin := seedMember(t, party)

	cred := fxPassword(t, integPool, m.User, nil)
	s := sessionWith(t, m.User, 1, cred)
	wantAvail(t, sessionAvail(t, s, m.PartyProfile), "credential_not_authorized", "unauthorized credential")
	wantAvail(t, credAvail(t, cred, m.PartyProfile), "credential_not_authorized", "credential path, unauthorized")
	wantAvail(t, sessionAvail(t, s, m.Personal), "available", "personal profile skips the party gate")

	fxExec(t, integPool, "authorize credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		cred, party, admin.PartyProfile)
	wantAvail(t, sessionAvail(t, s, m.PartyProfile), "available", "authorized credential")
	wantAvail(t, credAvail(t, cred, m.PartyProfile), "available", "credential path, authorized")

	// A credential confined to the party also satisfies the rule.
	conf := fxOIDC(t, integPool, m.User, "https://"+fxTag("iss"), "p", fxTag("sub"))
	confine(t, conf, party)
	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 1, conf), m.PartyProfile), "available", "credential confined to the party")

	// One authorized factor among unauthorized ones is enough.
	other, _ := fxSSHKey(t, integPool, m.User, "SHA256:"+fxTag("fp"))
	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 2, other, cred), m.PartyProfile), "available", "one authorized factor of two")
}

func TestInteg_Availability_RevokedFactorDoesNotCount(t *testing.T) {
	party := seedOrg(t, "Revoked factor party")
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, party, "authorized", nil, 1) })
	m := seedMember(t, party)
	admin := seedMember(t, party)

	cred := fxPassword(t, integPool, m.User, nil)
	fxExec(t, integPool, "authorize credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		cred, party, admin.PartyProfile)
	s := sessionWith(t, m.User, 1, cred)
	wantAvail(t, sessionAvail(t, s, m.PartyProfile), "available", "authorized, unrevoked")

	// Revoke the credential without revoking its session: the revoked factor
	// no longer satisfies the rule.
	fxExec(t, integPool, "revoke credential",
		`UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'departure' WHERE id = $1`, cred)
	wantAvail(t, sessionAvail(t, s, m.PartyProfile), "credential_not_authorized", "revoked authorized factor")
}

func TestInteg_Availability_CredentialRuleProvider(t *testing.T) {
	party := seedOrg(t, "Provider rule party")
	provider := "mf-" + fxTag("prov")
	other := "mf-" + fxTag("prov")
	fxMustTx(t, func(tx pgx.Tx) {
		fxOIDCProvider(t, tx, provider, false)
		fxOIDCProvider(t, tx, other, false)
		fxPolicy(t, tx, party, "provider", &provider, 1)
	})
	m := seedMember(t, party)

	pw := fxPassword(t, integPool, m.User, nil)
	wrong := fxOIDC(t, integPool, m.User, "https://"+fxTag("iss"), other, fxTag("sub"))
	right := fxOIDC(t, integPool, m.User, "https://"+fxTag("iss"), provider, fxTag("sub"))

	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 1, pw), m.PartyProfile), "provider_required", "password only")
	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 1, wrong), m.PartyProfile), "provider_required", "oidc of another provider")
	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 1, right), m.PartyProfile), "available", "oidc of the required provider")
	wantAvail(t, sessionAvail(t, sessionWith(t, m.User, 2, pw, right), m.PartyProfile), "available", "required provider among several factors")
	wantAvail(t, credAvail(t, right, m.PartyProfile), "available", "credential path, required provider")
	wantAvail(t, credAvail(t, pw, m.PartyProfile), "provider_required", "credential path, password")
}

func TestInteg_Availability_MinACRAndConfirmations(t *testing.T) {
	party := seedOrg(t, "Min acr party")
	provider := "mf-" + fxTag("prov")
	fxMustTx(t, func(tx pgx.Tx) {
		fxOIDCProvider(t, tx, provider, false)
		fxPolicy(t, tx, party, "provider", &provider, 2)
	})
	confirmer := seedMember(t, party)
	m := seedMember(t, party)

	pw := fxPassword(t, integPool, m.User, nil)
	oidc := fxOIDC(t, integPool, m.User, "https://"+fxTag("iss"), provider, fxTag("sub"))

	// min_acr applies on top of the credential rule.
	low := sessionWith(t, m.User, 1, oidc)
	wantAvail(t, sessionAvail(t, low, m.PartyProfile), "acr_too_low", "right provider, session acr 1 below min_acr 2")
	high := sessionWith(t, m.User, 2, oidc)
	wantAvail(t, sessionAvail(t, high, m.PartyProfile), "available", "right provider, session acr 2")
	wantAvail(t, credAvail(t, oidc, m.PartyProfile), "acr_too_low", "credential path: base_acr 1 is below min_acr 2")

	// The personal profile is not subject to the party's min_acr.
	wantAvail(t, sessionAvail(t, low, m.Personal), "available", "personal profile skips min_acr")

	// An outside-collaborator confirmation exempts the credential rule only.
	pwLow := sessionWith(t, m.User, 1, pw)
	wantAvail(t, sessionAvail(t, pwLow, m.PartyProfile), "provider_required", "wrong factor before confirmation")
	fxExec(t, integPool, "confirm profile",
		`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $3)`,
		m.PartyProfile, party, confirmer.PartyProfile)
	wantAvail(t, sessionAvail(t, pwLow, m.PartyProfile), "acr_too_low", "confirmed profile still meets min_acr")
	pwHigh := sessionWith(t, m.User, 2, pw)
	wantAvail(t, sessionAvail(t, pwHigh, m.PartyProfile), "available", "confirmed profile with sufficient acr")
}

// TestInteg_Availability_ImpersonationUsesParentFactors proves decision G as
// resolved: an impersonation session (no factors of its own) meets the
// impersonated profile's party policy with the parent (administrator)
// session's factors and acr. Only an administrator acting through an operator
// profile (the recorded impersonator_profile_id, decision J) skips the
// credential rule; min_acr binds every administrator.
func TestInteg_Availability_ImpersonationUsesParentFactors(t *testing.T) {
	party := seedOrg(t, "Impersonation party")
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, party, "authorized", nil, 1) })
	target := seedMember(t, party)
	authorizer := seedMember(t, party)
	setMinACR := func(party int64, minACR int) {
		t.Helper()
		fxExec(t, integPool, "set min_acr", `UPDATE mod_users.party_authn_policies SET min_acr = $2 WHERE party_id = $1`, party, minACR)
	}

	// The target's own session fails the credential rule.
	targetCred := fxPassword(t, integPool, target.User, nil)
	own := sessionWith(t, target.User, 1, targetCred)
	wantAvail(t, sessionAvail(t, own, target.PartyProfile), "credential_not_authorized", "target's own session")

	// A non-operator administrator whose factor is not authorized for the party.
	admin, _ := seedUserEntity(t)
	adminCred := fxPassword(t, integPool, admin, nil)
	parent := sessionWith(t, admin, 1, adminCred)
	imp, _ := fxSession(t, integPool, target.User, sessionOpts{ParentID: &parent, ImpersonateProfile: &target.PartyProfile})
	wantAvail(t, sessionAvail(t, imp, target.PartyProfile), "credential_not_authorized", "non-operator administrator, unauthorized factor")
	wantAvail(t, sessionAvail(t, imp, target.Personal), "not_held", "impersonation reaches only the impersonated profile")

	// The target's own authorized credential does not help: it is not a factor
	// of the impersonation.
	fxExec(t, integPool, "authorize target credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		targetCred, party, authorizer.PartyProfile)
	wantAvail(t, sessionAvail(t, imp, target.PartyProfile), "credential_not_authorized", "target's credential is not the session's")

	// Authorizing the administrator's own factor satisfies the rule.
	fxExec(t, integPool, "authorize admin credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		adminCred, party, authorizer.PartyProfile)
	wantAvail(t, sessionAvail(t, imp, target.PartyProfile), "available", "administrator's authorized factor")
	var excl string
	if err := integPool.QueryRow(context.Background(),
		`SELECT mod_users.profile_availability_excluding($1, $2, $3)`, imp, target.PartyProfile, adminCred).Scan(&excl); err != nil {
		t.Fatalf("profile_availability_excluding: %v", err)
	}
	wantAvail(t, excl, "credential_not_authorized", "excluding the administrator's only authorized factor")

	// min_acr is the parent session's acr, for the non-operator administrator.
	setMinACR(party, 2)
	wantAvail(t, sessionAvail(t, imp, target.PartyProfile), "acr_too_low", "parent acr below min_acr")
	fxExec(t, integPool, "raise parent acr", `UPDATE mod_users.sessions SET acr = 2 WHERE id = $1`, parent)
	wantAvail(t, sessionAvail(t, imp, target.PartyProfile), "available", "parent acr meets min_acr")
	setMinACR(party, 1)

	// An operator administrator, with no factor authorized for the party: the
	// credential rule is skipped (decision D extended), min_acr is not. The
	// skip follows the recorded acting profile (decision J): the same principal
	// impersonating through its personal profile is bound by the rule.
	opUser, opProfile := seedOperatorUser(t)
	opParent := sessionWith(t, opUser, 1, fxPassword(t, integPool, opUser, nil))
	var opPersonal int64
	if err := integPool.QueryRow(context.Background(),
		`SELECT entity_id FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, opUser).Scan(&opPersonal); err != nil {
		t.Fatalf("operator's personal profile: %v", err)
	}
	viaPersonal, _ := fxSession(t, integPool, target.User, sessionOpts{ParentID: &opParent,
		ImpersonateProfile: &target.PartyProfile, ImpersonatorProfile: &opPersonal})
	wantAvail(t, sessionAvail(t, viaPersonal, target.PartyProfile), "credential_not_authorized", "operator's principal acting through its personal profile")
	opImp, _ := fxSession(t, integPool, target.User, sessionOpts{ParentID: &opParent,
		ImpersonateProfile: &target.PartyProfile, ImpersonatorProfile: &opProfile})
	wantAvail(t, sessionAvail(t, opImp, target.PartyProfile), "available", "operator administrator skips the credential rule")
	setMinACR(party, 3)
	wantAvail(t, sessionAvail(t, opImp, target.PartyProfile), "acr_too_low", "operator administrator below min_acr")
}

// TestInteg_Availability_ImpersonationConfinesNonOperatorParentFactors proves
// decision L: a non-operator administrator's parent-session factors are held
// to confinement against the impersonated profile's party, as an ordinary
// session's factors are; an impersonation started through an operator profile
// (the recorded impersonator_profile_id, decision J) is exempt, so an operator
// credential confined to the operator party still impersonates.
func TestInteg_Availability_ImpersonationConfinesNonOperatorParentFactors(t *testing.T) {
	partyX := seedOrg(t, "Confined imp X")
	partyY := seedOrg(t, "Confined imp Y")
	targetY := seedMember(t, partyY)
	targetX := seedMember(t, partyX)

	// A party X administrator whose only parent-session factor is confined to X.
	admin := seedMember(t, partyX)
	adminCred := fxPassword(t, integPool, admin.User, nil)
	confine(t, adminCred, partyX)
	parent := sessionWith(t, admin.User, 1, adminCred)
	impersonate := func(parentID int64, target member, profile int64, acting *int64) int64 {
		t.Helper()
		id, _ := fxSession(t, integPool, target.User, sessionOpts{ParentID: &parentID,
			ImpersonateProfile: &profile, ImpersonatorProfile: acting})
		return id
	}

	wantAvail(t, sessionAvail(t, parent, admin.PartyProfile), "available", "administrator's own party profile")
	impY := impersonate(parent, targetY, targetY.PartyProfile, &admin.PartyProfile)
	wantAvail(t, sessionAvail(t, impY, targetY.PartyProfile), "confined", "non-operator, parent factor confined to X, party Y member")
	impYPersonal := impersonate(parent, targetY, targetY.Personal, &admin.PartyProfile)
	wantAvail(t, sessionAvail(t, impYPersonal, targetY.Personal), "confined", "non-operator, parent factor confined to X, personal profile")
	impX := impersonate(parent, targetX, targetX.PartyProfile, &admin.PartyProfile)
	wantAvail(t, sessionAvail(t, impX, targetX.PartyProfile), "available", "non-operator, parent factor confined to the impersonated profile's party")

	// Lifting the confinement makes the same impersonation available again.
	fxExec(t, integPool, "lift confinement", `UPDATE mod_users.credentials SET confined_to_party_id = NULL WHERE id = $1`, adminCred)
	wantAvail(t, sessionAvail(t, impY, targetY.PartyProfile), "available", "non-operator, unconfined parent factor")

	// An operator whose credential is confined to the operator party: exempt
	// when acting through the operator profile, bound through its personal one.
	opUser, opProfile := seedOperatorUser(t)
	opCred := fxPassword(t, integPool, opUser, nil)
	confine(t, opCred, operatorPartyID(t))
	opParent := sessionWith(t, opUser, 1, opCred)
	var opPersonal int64
	if err := integPool.QueryRow(context.Background(),
		`SELECT entity_id FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, opUser).Scan(&opPersonal); err != nil {
		t.Fatalf("operator's personal profile: %v", err)
	}
	opImp := impersonate(opParent, targetY, targetY.PartyProfile, &opProfile)
	wantAvail(t, sessionAvail(t, opImp, targetY.PartyProfile), "available", "operator, credential confined to the operator party")
	viaPersonal := impersonate(opParent, targetY, targetY.PartyProfile, &opPersonal)
	wantAvail(t, sessionAvail(t, viaPersonal, targetY.PartyProfile), "confined", "operator's principal acting through its personal profile")
}

// TestInteg_Availability_ExcludingNullEqualsPlain proves a NULL excluded
// credential excludes nothing: profile_availability_excluding(s, p, NULL)
// equals profile_availability(s, p) for every lock outcome.
func TestInteg_Availability_ExcludingNullEqualsPlain(t *testing.T) {
	party := seedOrg(t, "Excluding null party")
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, party, "authorized", nil, 1) })
	m := seedMember(t, party)
	admin := seedMember(t, party)

	cred := fxPassword(t, integPool, m.User, nil)
	s := sessionWith(t, m.User, 1, cred)

	excludingNull := func(sessionID, profileID int64) string {
		t.Helper()
		var got string
		if err := integPool.QueryRow(context.Background(),
			`SELECT mod_users.profile_availability_excluding($1, $2, NULL)`, sessionID, profileID).Scan(&got); err != nil {
			t.Fatalf("profile_availability_excluding: %v", err)
		}
		return got
	}
	check := func(what string, want string) {
		t.Helper()
		plain := sessionAvail(t, s, m.PartyProfile)
		wantAvail(t, plain, want, what+" (profile_availability)")
		wantAvail(t, excludingNull(s, m.PartyProfile), plain, what+" (excluding NULL)")
	}

	check("unauthorized factor", "credential_not_authorized")
	fxExec(t, integPool, "authorize credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		cred, party, admin.PartyProfile)
	check("authorized factor", "available")
}
