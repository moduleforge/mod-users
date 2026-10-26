//go:build integration

package integration

// Outside collaborators and continuous compliance against the real services,
// database functions and routes: the policy-mismatch confirmation on an
// invitation, the CollaboratorGate mod-authz consults (called directly and
// through mod-authz's own routes), and the three ways compliance is enforced
// after the fact (credential revocation, party min_acr, credential downgrade).

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
)

const mismatchCode = "users.policy_mismatch_confirmation_required"

// confirmationRow returns the confirmer of a (profile, party) confirmation, or 0.
func (s *stack) confirmationRow(t *testing.T, profileID, partyID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "confirmation row",
		`SELECT COALESCE((SELECT confirmed_by_profile_id FROM profile_policy_confirmations WHERE profile_id = $1 AND party_id = $2), 0)`,
		profileID, partyID)
}

// profileCount counts the profiles a user holds in the party, ended ones included.
func (s *stack) profileCount(t *testing.T, userID, partyID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "profile count", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2`, userID, partyID)
}

// availability is mod_users.profile_availability for a session and a profile.
func (s *stack) availability(t *testing.T, sessionUUID string, profileID int64) string {
	t.Helper()
	var got string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT mod_users.profile_availability(s.id, $2) FROM mod_users.sessions s WHERE s.uuid = $1::uuid`,
		sessionUUID, profileID).Scan(&got); err != nil {
		t.Fatalf("profile_availability: %v", err)
	}
	return got
}

// mismatch is mod_users.holder_policy_mismatch for a holder and a party in
// invite-time (relaxed) mode: the reason ("" for none) and the holder's best acr (0 for none).
func (s *stack) mismatch(t *testing.T, holderID, partyID int64) (reason string, holderACR int16) {
	t.Helper()
	var r *string
	var acr *int16
	if err := s.pool.QueryRow(context.Background(),
		`SELECT reason, holder_acr FROM mod_users.holder_policy_mismatch($1, $2, true)`, holderID, partyID).Scan(&r, &acr); err != nil {
		t.Fatalf("holder_policy_mismatch: %v", err)
	}
	if r != nil {
		reason = *r
	}
	if acr != nil {
		holderACR = *acr
	}
	return reason, holderACR
}

// addOIDCCredential gives the user an active OIDC credential of the provider.
func (s *stack) addOIDCCredential(t *testing.T, userID int64, provider string) int64 {
	t.Helper()
	id := s.scanInt64(t, "oidc credential", `INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, 'oidc') RETURNING id`, userID)
	s.exec(t, "cred_oidc", `INSERT INTO mod_users.cred_oidc (credential_id, issuer, provider_id, subject) VALUES ($1, $2, $3, $4)`,
		id, "https://idp.example.test/"+provider, provider, uniq("sub"))
	return id
}

// wantMismatch fails unless the response is the 409 confirmation-required
// refusal whose second detail names the failing rule.
func (r *resp) wantMismatch(t *testing.T, reason string) {
	t.Helper()
	r.wantError(t, http.StatusConflict, "conflict", mismatchCode)
	var e struct {
		Error struct {
			Details []struct{ Field, Code, Message string } `json:"details"`
		} `json:"error"`
	}
	r.into(t, &e)
	if len(e.Error.Details) < 2 || e.Error.Details[1].Code != "users."+reason || e.Error.Details[1].Message == "" {
		t.Fatalf("mismatch details = %+v, want a second detail users.%s with a message; body=%s", e.Error.Details, reason, r.raw)
	}
}

// twoFactorSession signs the member in with the password and steps up with an
// emailed code, without switching profile: a personal session holding both
// factors (acr 2). The member's email must be verified.
func (s *stack) twoFactorSession(t *testing.T, m gateMember) tokens {
	t.Helper()
	tk := s.login(t, m.acct)
	seen := s.mail.count(m.acct.Email)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", tk.access(), map[string]string{"email": m.acct.Email}).
		wantStatus(t, http.StatusNoContent)
	code := s.mail.codeAfter(t, m.acct.Email, seen)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", tk.access(),
		map[string]string{"email": m.acct.Email, "code": code}).wantStatus(t, http.StatusOK)
	return tk
}

func (g gateParty) invite(t *testing.T, body map[string]any) *resp {
	t.Helper()
	return env.do(t, http.MethodPost, g.path("/profiles"), g.adminTok.access(), body)
}

func TestCollaborator_InviteMismatchNeedsConfirmationAndWritesNothing(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "collab-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)

	env.obs.reset()
	g.invite(t, map[string]any{"holder_email": a.Email}).wantMismatch(t, "provider_required")
	g.invite(t, map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": false}).wantMismatch(t, "provider_required")
	if n := env.profileCount(t, uid, g.id); n != 0 {
		t.Fatalf("a refused invitation left %d profile(s)", n)
	}
	if got := env.obs.pairs(); slices.Contains(got, "create profile") {
		t.Errorf("a refused invitation observed %v", got)
	}

	// Confirmed: the profile and the confirmation are written together, and the
	// confirmation names the acting administrator.
	v := g.invite(t, map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).
		wantStatus(t, http.StatusCreated).obj(t)
	if v["status"] != "invited" {
		t.Fatalf("invited profile = %v", v)
	}
	var profileID int64
	if err := env.pool.QueryRow(context.Background(),
		`SELECT p.entity_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`, v["uuid"]).Scan(&profileID); err != nil {
		t.Fatalf("profile row: %v", err)
	}
	adminProfile := env.scanInt64(t, "admin profile", `SELECT p.entity_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`,
		env.profileOf(t, g.adminTok.access()))
	if got := env.confirmationRow(t, profileID, g.id); got != adminProfile {
		t.Fatalf("confirmed_by_profile_id = %d, want the acting profile %d", got, adminProfile)
	}
}

func TestCollaborator_InviteNoMismatchRecordsNoConfirmation(t *testing.T) {
	g := env.newGateParty(t)
	// `any` with min_acr 1 never mismatches, even with the flag set.
	a := env.registerVerified(t)
	g.invite(t, map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).wantStatus(t, http.StatusCreated)
	if n := env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, g.id); n != 0 {
		t.Fatalf("a matching holder got %d confirmation(s)", n)
	}
	// An unconfirmed invitation passes too.
	b := env.registerVerified(t)
	g.invite(t, map[string]any{"holder_email": b.Email}).wantStatus(t, http.StatusCreated)
}

func TestCollaborator_InviteMismatchByRule(t *testing.T) {
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "collab-"+uniq("idp"))

	t.Run("provider rule needs an OIDC credential of the provider", func(t *testing.T) {
		skipOperatorUnderOwningPartyGate(t)
		env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
		a := env.registerVerified(t)
		g.invite(t, map[string]any{"holder_email": a.Email}).wantMismatch(t, "provider_required")
		// An OIDC credential of another provider does not help; the right one does.
		uid := env.principalID(t, a.Email)
		env.addOIDCCredential(t, uid, "some-other-idp")
		g.invite(t, map[string]any{"holder_email": a.Email}).wantMismatch(t, "provider_required")
		env.addOIDCCredential(t, uid, provider)
		g.invite(t, map[string]any{"holder_email": a.Email}).wantStatus(t, http.StatusCreated)
	})

	t.Run("authorized rule is met by any usable interactive credential", func(t *testing.T) {
		skipOperatorUnderOwningPartyGate(t)
		env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
		a := env.registerVerified(t)
		uid := env.principalID(t, a.Email)
		adminProfile := env.scanInt64(t, "admin profile", `SELECT p.entity_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`,
			env.profileOf(t, g.adminTok.access()))
		cred := env.scanInt64(t, "password credential", `SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password'`, uid)
		env.exec(t, "authorize", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			cred, g.id, adminProfile)
		g.invite(t, map[string]any{"holder_email": a.Email}).wantStatus(t, http.StatusCreated)

		// An unauthorized password is just as good: no credential can be
		// authorized before the profile exists, so authorization follows acceptance.
		c := env.registerVerified(t)
		g.invite(t, map[string]any{"holder_email": c.Email}).wantStatus(t, http.StatusCreated)

		// A credential confined to another party is unusable for this one.
		b := env.registerVerified(t)
		bid := env.principalID(t, b.Email)
		other := env.seedParty(t, "Other "+uniq("p"))
		env.exec(t, "confine", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE principal_id = $1`, bid, other)
		g.invite(t, map[string]any{"holder_email": b.Email}).wantMismatch(t, "credential_not_authorized")
	})

	t.Run("min_acr follows the session_acr composition rule", func(t *testing.T) {
		skipOperatorUnderOwningPartyGate(t)
		env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 2}).wantStatus(t, http.StatusOK)
		a := env.registerVerified(t)
		uid := env.principalID(t, a.Email)
		// A password alone reaches acr 1; no combination reaches 2.
		g.invite(t, map[string]any{"holder_email": a.Email}).wantMismatch(t, "acr_too_low")
		if reason, acr := env.mismatch(t, uid, g.id); reason != "acr_too_low" || acr != 1 {
			t.Fatalf("mismatch = %q, acr %d; want acr_too_low, 1", reason, acr)
		}
		// Two distinct factor classes (password: knowledge, email code: possession) reach 2.
		env.verifyRegisteredEmail(t, a)
		if reason, acr := env.mismatch(t, uid, g.id); reason != "" || acr != 2 {
			t.Fatalf("after verifying the email: mismatch = %q, acr %d; want none, 2", reason, acr)
		}
		g.invite(t, map[string]any{"holder_email": a.Email}).wantStatus(t, http.StatusCreated)

		// No seeded kind reaches 3: min_acr 3 always mismatches, and a confirmation
		// is accepted but changes nothing about min_acr (see the availability test).
		env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 3}).wantStatus(t, http.StatusOK)
		b := env.registerVerified(t)
		env.verifyRegisteredEmail(t, b)
		g.invite(t, map[string]any{"holder_email": b.Email}).wantMismatch(t, "acr_too_low")
	})

	t.Run("a revoked or non-interactive credential counts for nothing", func(t *testing.T) {
		env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 2}).wantStatus(t, http.StatusOK)
		a := env.registerVerified(t)
		env.verifyRegisteredEmail(t, a)
		uid := env.principalID(t, a.Email)
		if reason, _ := env.mismatch(t, uid, g.id); reason != "" {
			t.Fatalf("setup: mismatch = %q", reason)
		}
		env.exec(t, "revoke email_otp", `UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'user' WHERE principal_id = $1 AND kind = 'email_otp'`, uid)
		if reason, acr := env.mismatch(t, uid, g.id); reason != "acr_too_low" || acr != 1 {
			t.Fatalf("after revoking: mismatch = %q, acr %d; want acr_too_low, 1", reason, acr)
		}
	})

	t.Run("a holder with no usable credential has a NULL holder_acr", func(t *testing.T) {
		skipOperatorUnderOwningPartyGate(t)
		env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 2}).wantStatus(t, http.StatusOK)
		a := env.registerVerified(t)
		uid := env.principalID(t, a.Email)
		env.exec(t, "revoke all", `UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'user' WHERE principal_id = $1`, uid)
		var acr *int16
		if err := env.pool.QueryRow(context.Background(),
			`SELECT holder_acr FROM mod_users.holder_policy_mismatch($1, $2, true)`, uid, g.id).Scan(&acr); err != nil {
			t.Fatalf("holder_policy_mismatch: %v", err)
		}
		if acr != nil {
			t.Fatalf("holder_acr = %d, want NULL with no usable credential", *acr)
		}
		if reason, _ := env.mismatch(t, uid, g.id); reason != "acr_too_low" {
			t.Fatalf("mismatch = %q, want acr_too_low", reason)
		}
		r := g.invite(t, map[string]any{"holder_email": a.Email})
		r.wantMismatch(t, "acr_too_low")
		if strings.Contains(string(r.raw), "at most 1") {
			t.Fatalf("409 detail claims acr 1 for a holder with no usable credential: %s", r.raw)
		}
	})
}

func TestCollaborator_ConfirmationExemptsCredentialRuleButNotMinACR(t *testing.T) {
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized", "min_acr": 2}).wantStatus(t, http.StatusOK)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)

	// Password plus emailed code: acr 2, but neither credential is authorized.
	both := env.twoFactorSession(t, m)
	if got := env.availability(t, both.Session.UUID, m.profileID); got != "credential_not_authorized" {
		t.Fatalf("without a confirmation: %s, want credential_not_authorized", got)
	}

	env.exec(t, "confirm", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`, m.profileID, g.id)
	// The credential rule is exempted: two factors now satisfy the party.
	if got := env.availability(t, both.Session.UUID, m.profileID); got != "available" {
		t.Fatalf("confirmed, acr 2: %s, want available", got)
	}
	// min_acr is not exempted: the password-only session is acr 1.
	personal := env.login(t, m.acct)
	if got := env.availability(t, personal.Session.UUID, m.profileID); got != "acr_too_low" {
		t.Fatalf("confirmed, acr 1: %s, want acr_too_low (the confirmation never exempts min_acr)", got)
	}
	// Raising min_acr beyond what the credentials reach locks even the two-factor session.
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized", "min_acr": 3}).wantStatus(t, http.StatusOK)
	if got := env.availability(t, both.Session.UUID, m.profileID); got != "acr_too_low" {
		t.Fatalf("confirmed, min_acr 3: %s, want acr_too_low", got)
	}
}

// gateCtx is a context carrying the acting profile of a gate call.
func gateCtx(principalID, profileID int64) context.Context {
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: principalID, ProfileID: profileID, SessionID: 1, AuthTime: time.Now(),
	})
}

func TestCollaborator_GateDirect(t *testing.T) {
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "collab-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	member := env.newGateMember(t, g)                // a profile of the party
	outsider := env.registerVerified(t)              // a user with only a personal profile
	outsiderID := env.principalID(t, outsider.Email) // the holder
	personal := env.personalProfileID(t, outsiderID)
	adminProfile := env.scanInt64(t, "admin profile", `SELECT p.entity_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`,
		env.profileOf(t, g.adminTok.access()))
	adminUser := env.scanInt64(t, "admin user", `SELECT holder_id FROM profiles WHERE entity_id = $1`, adminProfile)
	ctx := gateCtx(adminUser, adminProfile)

	check := func(profile int64, confirmed bool) error {
		return env.inTx(ctx, func(tx pgxTx) error {
			return env.gate.CheckOutsideCollaborator(ctx, tx, g.id, profile, confirmed)
		})
	}
	rows := func() int64 {
		return env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, g.id)
	}

	// Same party: passes with no write, whatever the credentials or the flag.
	if err := check(member.profileID, false); err != nil {
		t.Fatalf("same-party member: %v", err)
	}
	if err := check(member.profileID, true); err != nil || rows() != 0 {
		t.Fatalf("same-party member, confirmed: err=%v rows=%d; want no write", err, rows())
	}

	// A personal profile is outside; its holder's credentials mismatch the policy.
	err := check(personal, false)
	if err == nil {
		t.Fatal("outside profile, unconfirmed: want a refusal")
	}
	if !errorsIs(err, apiresp.ErrConflict) {
		t.Fatalf("refusal = %v, want an apiresp conflict", err)
	}
	if rows() != 0 {
		t.Fatalf("a refusal wrote %d confirmation(s)", rows())
	}

	// Confirmed: recorded with the acting profile, inside the caller's transaction.
	if err := check(personal, true); err != nil {
		t.Fatalf("outside profile, confirmed: %v", err)
	}
	if got := env.confirmationRow(t, personal, g.id); got != adminProfile {
		t.Fatalf("confirmed_by_profile_id = %d, want %d", got, adminProfile)
	}

	// Idempotent: confirming again changes nothing (the first confirmer stands),
	// and an already-confirmed profile passes even unconfirmed.
	other := env.newGateMember(t, env.newGateParty(t)) // another acting profile
	ctx2 := gateCtx(other.userID, other.profileID)
	if err := env.inTx(ctx2, func(tx pgxTx) error {
		return env.gate.CheckOutsideCollaborator(ctx2, tx, g.id, personal, true)
	}); err != nil {
		t.Fatalf("re-confirmation: %v", err)
	}
	if got := env.confirmationRow(t, personal, g.id); got != adminProfile || rows() != 1 {
		t.Fatalf("after re-confirmation: confirmer %d rows %d; want the original confirmer and one row", got, rows())
	}
	if err := check(personal, false); err != nil {
		t.Fatalf("already confirmed, unconfirmed call: %v", err)
	}

	// No mismatch, no write: with the default policy a confirmed call records nothing.
	g2 := env.newGateParty(t)
	ok := env.inTx(ctx, func(tx pgxTx) error {
		return env.gate.CheckOutsideCollaborator(ctx, tx, g2.id, personal, true)
	})
	if ok != nil || env.confirmationRow(t, personal, g2.id) != 0 {
		t.Fatalf("matching outsider: err=%v confirmation=%d; want pass and no write", ok, env.confirmationRow(t, personal, g2.id))
	}

	// A confirmation needs an acting profile.
	env.exec(t, "forget", `DELETE FROM profile_policy_confirmations WHERE party_id = $1`, g.id)
	anon := context.Background()
	if err := env.inTx(anon, func(tx pgxTx) error {
		return env.gate.CheckOutsideCollaborator(anon, tx, g.id, personal, true)
	}); err == nil {
		t.Fatal("confirmed call with no acting profile: want an error")
	}
	if rows() != 0 {
		t.Fatalf("an unauthenticated confirmation wrote %d row(s)", rows())
	}
}

func TestCollaborator_AddToPartyActorGroupThroughAuthzRoute(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "collab-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	member := env.newGateMember(t, g)
	outsider := env.registerVerified(t)
	outsiderID := env.principalID(t, outsider.Email)
	personal := env.personalProfileID(t, outsiderID)

	// A party-owned actor group.
	groupID := env.scanInt64(t, "group entity",
		`INSERT INTO entities (fundamental_type_id, owner_id) SELECT id, $1 FROM types WHERE slug = 'authz_actor_group' RETURNING id`, g.id)
	env.exec(t, "group row", `INSERT INTO authz_actor_groups (entity_id, name) VALUES ($1, $2)`, groupID, "collab-"+uniq("grp"))
	path := "/v1/authz/actor-groups/" + env.entityUUID(t, groupID) + "/members"
	members := func() int64 {
		return env.scanInt64(t, "members", `SELECT count(*) FROM authz_actor_group_members WHERE group_id = $1`, groupID)
	}

	// A same-party profile never reaches the gate.
	env.do(t, http.MethodPost, path, g.adminTok.access(), map[string]any{"member_uuid": member.profUUID}).wantStatus(t, http.StatusNoContent)

	// An outside profile whose holder cannot meet the policy needs confirmation.
	before := members()
	body := map[string]any{"member_uuid": env.profileUUID(t, personal)}
	env.do(t, http.MethodPost, path, g.adminTok.access(), body).wantMismatch(t, "provider_required")
	if members() != before || env.confirmationRow(t, personal, g.id) != 0 {
		t.Fatalf("a refused add wrote a member or a confirmation")
	}
	body["confirm_policy_mismatch"] = true
	env.do(t, http.MethodPost, path, g.adminTok.access(), body).wantStatus(t, http.StatusNoContent)
	if members() != before+1 {
		t.Fatalf("members = %d, want %d", members(), before+1)
	}
	if env.confirmationRow(t, personal, g.id) == 0 {
		t.Fatal("the confirmed add recorded no confirmation")
	}
	// A repeated add is the group's own concern; the confirmation stays single.
	if n := env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE profile_id = $1 AND party_id = $2`, personal, g.id); n != 1 {
		t.Fatalf("confirmations = %d, want 1", n)
	}
}

// A collaborator holds no profile in the owning party, so under an `authorized`
// party it can never hold a credential authorization: the invite-time relaxation
// does not reach the collaborator gate, and a personal-password-only outsider
// needs an explicit confirmation (while the same holder's invitation passes).
func TestCollaborator_AuthorizedPartyStrictForOutsideCollaborator(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	outsider := env.registerVerified(t)
	outsiderID := env.principalID(t, outsider.Email)
	personal := env.personalProfileID(t, outsiderID)

	groupID := env.scanInt64(t, "group entity",
		`INSERT INTO entities (fundamental_type_id, owner_id) SELECT id, $1 FROM types WHERE slug = 'authz_actor_group' RETURNING id`, g.id)
	env.exec(t, "group row", `INSERT INTO authz_actor_groups (entity_id, name) VALUES ($1, $2)`, groupID, "collab-"+uniq("grp"))
	path := "/v1/authz/actor-groups/" + env.entityUUID(t, groupID) + "/members"
	members := func() int64 {
		return env.scanInt64(t, "members", `SELECT count(*) FROM authz_actor_group_members WHERE group_id = $1`, groupID)
	}

	// The strict check refuses where the relaxed invite-time check passes.
	if got, _ := env.mismatch(t, outsiderID, g.id); got != "" {
		t.Fatalf("invite-time mismatch = %q, want none for a usable password", got)
	}
	var strict *string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT reason FROM mod_users.holder_policy_mismatch($1, $2, false)`, outsiderID, g.id).Scan(&strict); err != nil {
		t.Fatalf("holder_policy_mismatch strict: %v", err)
	}
	if strict == nil || *strict != "credential_not_authorized" {
		t.Fatalf("strict mismatch = %v, want credential_not_authorized", strict)
	}

	body := map[string]any{"member_uuid": env.profileUUID(t, personal)}
	env.do(t, http.MethodPost, path, g.adminTok.access(), body).wantMismatch(t, "credential_not_authorized")
	if members() != 0 || env.confirmationRow(t, personal, g.id) != 0 {
		t.Fatal("a refused add wrote a member or a confirmation")
	}
	body["confirm_policy_mismatch"] = true
	env.do(t, http.MethodPost, path, g.adminTok.access(), body).wantStatus(t, http.StatusNoContent)
	if members() != 1 || env.confirmationRow(t, personal, g.id) == 0 {
		t.Fatalf("the confirmed add left members=%d confirmation=%d", members(), env.confirmationRow(t, personal, g.id))
	}

	// The invitation of a holder with the same credentials still passes unconfirmed.
	b := env.registerVerified(t)
	g.invite(t, map[string]any{"holder_email": b.Email}).wantStatus(t, http.StatusCreated)
}

func TestCollaborator_Compliance_RevokingACredentialRevokesItsSessions(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	emailUUID := env.credentialUUID(t, m.acct.tk.access(), "email_otp")

	both := env.withEmailFactor(t, m) // password + email code factors
	if status, _ := env.probe(t, "/v1/probe", both.access()); status != http.StatusOK {
		t.Fatalf("probe before revoking = %d", status)
	}
	other := env.login(t, m.acct) // a password-only session
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+emailUUID, other.access(), nil).wantStatus(t, http.StatusNoContent)

	// The session that held the revoked credential as a factor is gone; the
	// session that did not is untouched.
	env.do(t, http.MethodGet, "/v1/probe", both.access(), nil).wantStatus(t, http.StatusUnauthorized)
	if status, _ := env.probe(t, "/v1/probe", other.access()); status != http.StatusOK {
		t.Fatalf("probe of the unrelated session = %d", status)
	}
}

func TestCollaborator_Compliance_RaisingMinACRLocksEvenAConfirmedCollaborator(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	env.exec(t, "confirm", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`, m.profileID, g.id)
	tok, _ := env.partySession(t, m)
	if status, p := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK || p.ProfileID != m.profileID {
		t.Fatalf("probe under the default policy = %d %+v", status, p)
	}
	env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 2}).wantStatus(t, http.StatusOK)
	// The same token, the next request: locked on acr, confirmation or not.
	env.do(t, http.MethodGet, "/v1/probe", tok.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	if got := env.availability(t, tok.Session.UUID, m.profileID); got != "acr_too_low" {
		t.Fatalf("availability = %s, want acr_too_low", got)
	}
}

func TestCollaborator_Compliance_DowngradedCredentialsLockTheProfileAtNextLogin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmed bool
		policy    map[string]any
		reason    string
	}{
		// The authorizing factor is revoked: the party's credential rule fails.
		{"authorizing factor revoked", false, map[string]any{"credential_rule": "authorized"}, "credential_not_authorized"},
		// A confirmed collaborator loses the second factor: min_acr fails.
		{"second factor revoked, confirmed", true, map[string]any{"credential_rule": "authorized", "min_acr": 2}, "acr_too_low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := env.newGateParty(t)
			m := env.newGateMember(t, g)
			env.verifyRegisteredEmail(t, m.acct)
			emailUUID := env.credentialUUID(t, m.acct.tk.access(), "email_otp")
			env.setPolicy(t, g, tc.policy).wantStatus(t, http.StatusOK)
			if tc.confirmed {
				env.exec(t, "confirm", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`, m.profileID, g.id)
			} else {
				env.do(t, http.MethodPut, authPath(g, emailUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
			}
			// The party profile is the user's default, so it is the landing profile.
			env.do(t, http.MethodPut, "/v1/me", m.acct.tk.access(), map[string]any{"default_profile_uuid": m.profUUID}).wantStatus(t, http.StatusOK)

			// Signing in with the password and the emailed code lands on it.
			good := env.withEmailFactor(t, m)
			if got := env.profileOf(t, good.access()); got != m.profUUID {
				t.Fatalf("before the downgrade the session acts as %s, want %s", got, m.profUUID)
			}

			// Downgrade: the emailed-code credential is revoked.
			env.do(t, http.MethodDelete, "/v1/me/credentials/"+emailUUID, m.acct.tk.access(), nil).wantStatus(t, http.StatusNoContent)

			tk := env.login(t, m.acct)
			personal := env.profileUUID(t, env.personalProfileID(t, m.userID))
			if tk.Profile == nil || tk.Profile.UUID != personal {
				t.Fatalf("landed on %+v, want the personal profile %s (the party profile is locked)", tk.Profile, personal)
			}
			var found *profileView
			for i := range tk.Profiles {
				if tk.Profiles[i].UUID == m.profUUID {
					found = &tk.Profiles[i]
				}
			}
			if found == nil || found.Available || found.Lock == nil || found.Lock.Reason != tc.reason {
				t.Fatalf("party profile in the login response = %+v, want locked with %s", found, tc.reason)
			}
			env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": m.profUUID}).
				wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
		})
	}
}
