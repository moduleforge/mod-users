//go:build integration

package integration

// Owned-scope re-test of the party-administration flows (decision U27).
//
// Phase 7 used operator-party administrators holding wildcard grants for
// every party flow, because mod-authz's trigger allows wildcard and
// type-entity grants only for operator actors. These tests re-run the flows
// with a NON-OPERATOR administrator: a member of the administered party that
// holds an owned-scope `manage` grant on it. Operator actors are exempt from
// the owning-party gate, so an operator administrator could mask a gate
// result; a non-operator administrator cannot.
//
// The existing suites are parameterized rather than copied: ownedScopeAdmins
// switches their fixtures (newGateParty, newParty, newAcme, joinBeta,
// setPolicy) to non-operator administrators, and each TestOwnedScope_* wrapper
// below runs one phase 7 test function in that mode.

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

// ownedScopeAdmins is true while a TestOwnedScope_* wrapper runs a phase 7 test
// with non-operator administrators. The integration suite runs its tests
// sequentially (none calls t.Parallel), so a plain variable is enough.
var ownedScopeAdmins bool

// ownedAdmin is the non-operator administrator of one party.
type ownedAdmin struct {
	acct      account
	userID    int64
	profileID int64
}

var ownedAdmins sync.Map // party id -> ownedAdmin

// inOwnedScopeMode runs fn with the fixtures producing non-operator
// administrators.
func inOwnedScopeMode(t *testing.T, fn func(*testing.T)) {
	t.Helper()
	ownedScopeAdmins = true
	defer func() { ownedScopeAdmins = false }()
	fn(t)
}

// makeOwnedAdmin makes the account the non-operator administrator of the party
// and returns an access token acting as its party profile. The administrator
// is an active member of the party and holds:
//
//   - an owned-scope `manage` grant on the party: it reaches everything the
//     party owns (profiles, managed users, the invitation and departure
//     operations), and never the party's own record (mod-core D7);
//   - an entity-scope `manage` grant on the party record, which the policy
//     routes (`update` on the party) need. Neither grant is wildcard or
//     type-entity, so mod-authz's operator-actor rule does not apply.
func (s *stack) makeOwnedAdmin(t *testing.T, a account, partyID int64) (tok tokens, profileID int64) {
	t.Helper()
	userID := s.principalID(t, a.Email)
	profileID, profUUID := s.seedPartyProfile(t, userID, partyID, "active")
	s.exec(t, "owned-scope manage grant", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope)
SELECT $1, o.id, $2, NULL, 'owned' FROM authz_operations o WHERE o.slug = 'manage'`, profileID, partyID)
	s.exec(t, "entity-scope manage grant on the party record", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope)
SELECT $1, o.id, $2, NULL, 'entity' FROM authz_operations o WHERE o.slug = 'manage'`, profileID, partyID)
	ownedAdmins.Store(partyID, ownedAdmin{acct: a, userID: userID, profileID: profileID})
	r := s.do(t, http.MethodPost, "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).wantStatus(t, http.StatusOK)
	return r.tok(t), profileID
}

// keepOwnedAdminUnlocked stops a party's own policy from locking its
// administrator out after a policy write: an operator administrator sits
// outside the party, but a member administrator is gated like any member. When
// the administrator's password is not authorized for the party (the rule is
// `authorized`) or the rule is `provider`, the administrator's profile gets a
// policy confirmation, which exempts the credential rule (never min_acr). A
// rule change deletes the party's confirmations, so this runs after every
// successful write.
func (s *stack) keepOwnedAdminUnlocked(t *testing.T, partyID int64, body map[string]any) {
	t.Helper()
	v, ok := ownedAdmins.Load(partyID)
	if !ok {
		return
	}
	adm := v.(ownedAdmin)
	s.stepUpOwnedAdmin(t, adm, body)
	rule, _ := body["credential_rule"].(string)
	if rule == "" || rule == "any" {
		return
	}
	if rule == "authorized" {
		if s.scanInt64(t, "admin authorization", `
SELECT count(*) FROM mod_users.credential_party_authorizations a JOIN mod_users.credentials c ON c.id = a.credential_id
 WHERE c.principal_id = $1 AND a.party_id = $2 AND c.kind = 'password' AND c.revoked_at IS NULL`, adm.userID, partyID) > 0 {
			return
		}
	}
	s.exec(t, "confirm the administrator", `
INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1) ON CONFLICT DO NOTHING`,
		adm.profileID, partyID)
}

// adminConfirmations counts the party's policy confirmations other than the
// administrator's own (the fixture's, see keepOwnedAdminUnlocked).
func (s *stack) adminConfirmations(t *testing.T, partyID int64) int64 {
	t.Helper()
	var skip int64
	if v, ok := ownedAdmins.Load(partyID); ok && ownedScopeAdmins {
		skip = v.(ownedAdmin).profileID
	}
	return s.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1 AND profile_id <> $2`, partyID, skip)
}

// ownedAdminFor returns the party's non-operator administrator fixture.
func ownedAdminFor(partyID int64) (ownedAdmin, bool) {
	v, ok := ownedAdmins.Load(partyID)
	if !ok {
		return ownedAdmin{}, false
	}
	return v.(ownedAdmin), true
}

// ---------------------------------------------------------------------------
// the fixture itself: the administrator is not an operator
// ---------------------------------------------------------------------------

func TestOwnedScope_AdministratorIsNotAnOperator(t *testing.T) {
	inOwnedScopeMode(t, func(t *testing.T) {
		g := env.newGateParty(t)
		adm, ok := ownedAdminFor(g.id)
		if !ok {
			t.Fatal("no owned-scope administrator recorded")
		}
		var isOperator bool
		if err := env.pool.QueryRow(context.Background(), `SELECT core_is_operator_profile($1)`, adm.profileID).Scan(&isOperator); err != nil {
			t.Fatal(err)
		}
		if isOperator {
			t.Fatal("the administrator is an operator profile; the re-test would prove nothing")
		}
		if n := env.scanInt64(t, "wildcard or type grants", `
SELECT count(*) FROM grants WHERE actor_id = $1 AND (target_id IS NULL OR target_id IN (SELECT entity_id FROM types))`, adm.profileID); n != 0 {
			t.Fatalf("the administrator holds %d wildcard or type-entity grants", n)
		}
		if n := env.scanInt64(t, "owned grants", `SELECT count(*) FROM grants WHERE actor_id = $1 AND scope = 'owned' AND target_id = $2`, adm.profileID, g.id); n != 1 {
			t.Fatalf("the administrator holds %d owned-scope grants on the party, want 1", n)
		}
	})
}

// ---------------------------------------------------------------------------
// the phase 7 flows, re-run with non-operator owned-scope administrators
// ---------------------------------------------------------------------------

// ownedScopeFlows lists the phase 7 test functions re-run with owned-scope
// administrators, grouped by the flow they exercise.
var ownedScopeFlows = []struct {
	name string
	fn   func(*testing.T)
}{
	// The ADR worked example (John, Acme and Beta) with a non-operator Acme.
	{"ADRWorkedExample", TestPartyFlows_ADRWorkedExample},
	{"ADRWorkedExample_ConfinedVariant", TestPartyFlows_ADRWorkedExample_ConfinedVariant},
	{"SuspendedProfileTokenIsRefusedUntilResumed", TestPartyFlows_SuspendedProfileTokenIsRefusedUntilResumed},
	{"HolderAndAdministratorAuthority", TestPartyFlows_HolderAndAdministratorAuthority},
	{"ManagedUserCreatedThroughTheRouteIsNotInvitable", TestPartyFlows_ManagedUserCreatedThroughTheRouteIsNotInvitable},
	{"ProviderPolicyRemedy", TestPartyFlows_ProviderPolicyRemedy},
	{"MinACRLockAndStepUpUnlock", TestPartyFlows_MinACRLockAndStepUpUnlock},
	{"ConfinedFactorNeverListsThePersonalProfileAsAvailable", TestPartyFlows_ConfinedFactorNeverListsThePersonalProfileAsAvailable},
	{"OutsideCollaboratorThroughTheRoutes", TestPartyFlows_OutsideCollaboratorThroughTheRoutes},
	{"TrustedAdmissionIsReachableThroughTheRoutes", TestPartyFlows_TrustedAdmissionIsReachableThroughTheRoutes},

	// Invitation, suspend/resume/end and managed users.
	{"Profiles_InviteCreatesAnInvitedProfile", TestProfiles_InviteCreatesAnInvitedProfile},
	{"Profiles_InviteByEmailRequiresAVerifiedIdentifier", TestProfiles_InviteByEmailRequiresAVerifiedIdentifier},
	{"Profiles_InviteByUserUUIDAndPolicyConfirmationFlag", TestProfiles_InviteByUserUUIDAndPolicyConfirmationFlag},
	{"Profiles_InviteRefusals", TestProfiles_InviteRefusals},
	{"Profiles_AcceptDeclineLeave", TestProfiles_AcceptDeclineLeave},
	{"Profiles_AdminStatusChanges", TestProfiles_AdminStatusChanges},
	{"Profiles_UnauthorizedCallersSeeNotFound", TestProfiles_UnauthorizedCallersSeeNotFound},
	{"Profiles_DepartureCleanup", TestProfiles_DepartureCleanup},
	{"Profiles_ManagedUsers", TestProfiles_ManagedUsers},
	{"Profiles_ObserverErrorAbortsTheTransaction", TestProfiles_ObserverErrorAbortsTheTransaction},
	{"Profiles_EndInTxIsIdempotentAndRequiresAReason", TestProfiles_EndInTxIsIdempotentAndRequiresAReason},

	// Authn-policy and credential-authorization administration.
	{"PartyPolicy_AuthnPolicyCRUDAndChecks", TestPartyPolicy_AuthnPolicyCRUDAndChecks},
	{"PartyPolicy_PolicyWriteNeedsFreshAuthenticationAndAnAdministrator", TestPartyPolicy_PolicyWriteNeedsFreshAuthenticationAndAnAdministrator},
	{"PartyPolicy_AdminAuthorizesListsAndRemovesACredential", TestPartyPolicy_AdminAuthorizesListsAndRemovesACredential},
	{"PartyPolicy_HolderAuthorizesThroughAnotherFactor", TestPartyPolicy_HolderAuthorizesThroughAnotherFactor},
	{"PartyPolicy_SelfAuthorizationUsingOnlyTheCredentialBeingAuthorizedFails", TestPartyPolicy_SelfAuthorizationUsingOnlyTheCredentialBeingAuthorizedFails},
	{"PartyPolicy_APersonalOnlySessionCannotAuthorizeForAnAuthorizedPolicy", TestPartyPolicy_APersonalOnlySessionCannotAuthorizeForAnAuthorizedPolicy},
	{"PartyPolicy_AvailabilityChangesTakeEffectOnTheNextRequest", TestPartyPolicy_AvailabilityChangesTakeEffectOnTheNextRequest},
	{"PartyPolicy_ConfinementSetClearAndConflict", TestPartyPolicy_ConfinementSetClearAndConflict},
	{"Confinement_AdministratorConfinesPartyControlledCredentials", TestConfinement_AdministratorConfinesPartyControlledCredentials},
	{"Confinement_HolderDoorClearsOnlyANonPartyControlledCredential", TestConfinement_HolderDoorClearsOnlyANonPartyControlledCredential},
	{"Confinement_DepartureStillRevokesEveryConfinedCredential", TestConfinement_DepartureStillRevokesEveryConfinedCredential},

	// Remediation tests: leave gating.
	{"LeaveGate_LockedSessionCannotLeave", TestLeaveGate_LockedSessionCannotLeave},
	{"LeaveGate_SuspendedProfileLockedSessionCannotLeave", TestLeaveGate_SuspendedProfileLockedSessionCannotLeave},
	{"LeaveGate_SuspendedProfileGateMetCanLeave", TestLeaveGate_SuspendedProfileGateMetCanLeave},

	// Remediation tests: managed-user authorization.
	{"ManagedUserProvisioning_SerializesWithPolicySwitch_NoRow", TestManagedUserProvisioning_SerializesWithPolicySwitch_NoRow},
	{"ManagedUserProvisioning_SerializesWithPolicySwitch_ExistingRow", TestManagedUserProvisioning_SerializesWithPolicySwitch_ExistingRow},

	// Remediation tests: invite verification.
	{"AuthorizedInvite_SatisfiedAtInviteThenGatedUntilAuthorized", TestAuthorizedInvite_SatisfiedAtInviteThenGatedUntilAuthorized},
	{"AuthorizedInvite_NoUsableInteractiveCredentialStillNeedsConfirmation", TestAuthorizedInvite_NoUsableInteractiveCredentialStillNeedsConfirmation},
	{"AuthorizedInvite_RuleChangeDropsPolicyConfirmations", TestAuthorizedInvite_RuleChangeDropsPolicyConfirmations},
	{"Collaborator_InviteMismatchNeedsConfirmationAndWritesNothing", TestCollaborator_InviteMismatchNeedsConfirmationAndWritesNothing},
	{"Collaborator_InviteNoMismatchRecordsNoConfirmation", TestCollaborator_InviteNoMismatchRecordsNoConfirmation},
	{"Collaborator_ConfirmationExemptsCredentialRuleButNotMinACR", TestCollaborator_ConfirmationExemptsCredentialRuleButNotMinACR},
	{"Collaborator_Compliance_RaisingMinACRLocksEvenAConfirmedCollaborator", TestCollaborator_Compliance_RaisingMinACRLocksEvenAConfirmedCollaborator},
}

func TestOwnedScope_Flows(t *testing.T) {
	for _, f := range ownedScopeFlows {
		t.Run(f.name, func(t *testing.T) { inOwnedScopeMode(t, f.fn) })
	}
}

// stepUpOwnedAdmin raises the administrator's session to the party's new
// min_acr, as a real administrator would: a confirmation never exempts min_acr,
// so a member administrator is locked by a min_acr above the session's acr
// until it steps up with a second factor class (the email code).
func (s *stack) stepUpOwnedAdmin(t *testing.T, adm ownedAdmin, body map[string]any) {
	t.Helper()
	minACR, _ := body["min_acr"].(int)
	// acr 3 is unreachable with the seeded factor kinds: nothing can unlock a
	// member administrator then (only an operator is exempt from the gate).
	if minACR <= 1 || minACR > 2 {
		return
	}
	if s.scanInt64(t, "admin session acr", `SELECT acr FROM mod_users.sessions WHERE uuid = $1::uuid`, adm.acct.tk.Session.UUID) >= int64(minACR) {
		return
	}
	if s.scanInt64(t, "verified email", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'email_otp' AND revoked_at IS NULL`, adm.userID) == 0 {
		s.verifyRegisteredEmail(t, adm.acct)
	}
	seen := s.mail.count(adm.acct.Email)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", adm.acct.tk.access(), map[string]string{"email": adm.acct.Email}).
		wantStatus(t, http.StatusNoContent)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", adm.acct.tk.access(),
		map[string]string{"email": adm.acct.Email, "code": s.mail.codeAfter(t, adm.acct.Email, seen)}).wantStatus(t, http.StatusOK)
}

// ---------------------------------------------------------------------------
// callers that lack `update` on the party, as non-operator members
// ---------------------------------------------------------------------------

// ownedMemberCaller is a member of the party holding owned-scope grants of the
// named operations on it, and no entity-scope grant on the party record (so no
// `update` on the party). The party's policy confirmation exempts its
// credential rule, so the policy under test does not lock the caller. Call it
// after the policy is set: a rule change deletes confirmations.
func (s *stack) ownedMemberCaller(t *testing.T, g gateParty, ops ...string) (tok tokens, profileID int64) {
	t.Helper()
	a := s.register(t)
	userID := s.principalID(t, a.Email)
	profileID, profUUID := s.seedPartyProfile(t, userID, g.id, "active")
	for _, op := range ops {
		s.exec(t, "owned-scope "+op+" grant", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope)
SELECT $1, o.id, $2, NULL, 'owned' FROM authz_operations o WHERE o.slug = $3`, profileID, g.id, op)
	}
	s.exec(t, "confirm the caller", `
INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1) ON CONFLICT DO NOTHING`,
		profileID, g.id)
	r := s.do(t, http.MethodPost, "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).wantStatus(t, http.StatusOK)
	return r.tok(t), profileID
}

// A confirmed invitation (which exempts the credential rule for good) needs
// `update` on the party; owned-scope `create` and `read` on the party's
// profiles do not give it. This is TestAuthorizedInvite_ConfirmationNeedsUpdateOnTheParty
// with a non-operator member caller.
func TestOwnedScope_ConfirmationNeedsUpdateOnTheParty(t *testing.T) {
	inOwnedScopeMode(t, func(t *testing.T) {
		g := env.newGateParty(t)
		provider := env.addProviderRow(t, "inv-"+uniq("idp"))
		env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
		tok, callerProf := env.ownedMemberCaller(t, g, "create", "read")
		invite := func(body map[string]any) *resp {
			return env.do(t, http.MethodPost, g.path("/profiles"), tok.access(), body)
		}

		a := env.registerVerified(t)
		uid := env.principalID(t, a.Email)
		invite(map[string]any{"holder_email": a.Email}).wantMismatch(t, "provider_required")
		invite(map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).wantStatus(t, http.StatusForbidden)
		if n := env.profileCount(t, uid, g.id); n != 0 {
			t.Fatalf("a refused confirmed invitation left %d profile(s)", n)
		}
		// With `update` on the party record the same request is honored.
		env.exec(t, "caller update grant on the party", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope)
SELECT $1, o.id, $2, NULL, 'entity' FROM authz_operations o WHERE o.slug = 'update'`, callerProf, g.id)
		invite(map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).wantStatus(t, http.StatusCreated)
		if n := env.profileCount(t, uid, g.id); n != 1 {
			t.Fatalf("profiles = %d, want 1", n)
		}
	})
}

// A managed user created in an `authorized` party gets its password authorized
// only by a caller with `update` on the party. This is
// TestAuthorizedManagedUser_AutoAuthorizationNeedsUpdateOnTheParty with a
// non-operator member caller holding owned-scope create and read.
func TestOwnedScope_ManagedUserAutoAuthorizationNeedsUpdateOnTheParty(t *testing.T) {
	inOwnedScopeMode(t, func(t *testing.T) {
		g := env.newGateParty(t)
		env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
		tok, callerProf := env.ownedMemberCaller(t, g, "create", "read")

		create := func(email string) map[string]any {
			return env.do(t, http.MethodPost, g.path("/managed-users"), tok.access(), map[string]string{"email": email, "password": testPassword}).
				wantStatus(t, http.StatusCreated).obj(t)
		}
		email := uniq("managed") + "@example.test"
		created := create(email)
		profileUUID := created["profile"].(map[string]any)["uuid"].(string)
		if n := env.managedPasswordAuthorizations(t, email, g.id); n != 0 {
			t.Fatalf("authorizations of the managed user's password = %d, want 0 without update", n)
		}
		login := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusOK).obj(t)
		if login["profile"] != nil || login["access_token"] != nil {
			t.Fatalf("login selected a profile: %v", login)
		}
		profiles, _ := login["profiles"].([]any)
		if len(profiles) != 1 {
			t.Fatalf("profiles = %v, want the single party profile", profiles)
		}
		p := profiles[0].(map[string]any)
		lock, _ := p["lock"].(map[string]any)
		if p["uuid"] != profileUUID || p["available"] != false || lock["reason"] != "credential_not_authorized" {
			t.Fatalf("profile = %v, want %s locked with credential_not_authorized", p, profileUUID)
		}

		// With `update` on the party record, the password is authorized at creation.
		env.exec(t, "caller update grant on the party", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope)
SELECT $1, o.id, $2, NULL, 'entity' FROM authz_operations o WHERE o.slug = 'update'`, callerProf, g.id)
		second := uniq("managed") + "@example.test"
		create(second)
		if n := env.managedPasswordAuthorizations(t, second, g.id); n != 1 {
			t.Fatalf("authorizations with update = %d, want 1", n)
		}
	})
}

// ---------------------------------------------------------------------------
// the negative case per flow: an owned-scope grant on another party is refused
// ---------------------------------------------------------------------------

// The administrator of party A holds an owned-scope manage grant on A only.
// Every administration flow aimed at party B is refused and writes nothing,
// while the same flow on A succeeds: the grant's reach is exactly its target.
func TestOwnedScope_AnotherPartysAdministratorIsRefusedOnEveryFlow(t *testing.T) {
	inOwnedScopeMode(t, func(t *testing.T) {
		a := env.newGateParty(t)
		b := env.newGateParty(t)
		tok := a.adminTok.access()

		// B's member, with a profile to transition and a password to authorize.
		bm := env.newGateMember(t, b)
		bmCred := env.principalCredentialUUID(t, bm.userID, "password")
		// The same on A, for the positive control.
		am := env.newGateMember(t, a)
		amCred := env.principalCredentialUUID(t, am.userID, "password")

		outsider := env.registerVerified(t)
		oid := env.principalID(t, outsider.Email)
		outsider2 := env.registerVerified(t)

		flows := []struct {
			name      string
			method    string
			path      func(g gateParty, m gateMember, cred string) string
			body      any
			okStatus  int
			refused   []int
			unchanged func(t *testing.T)
		}{
			{
				name:   "invitation",
				method: http.MethodPost, path: func(g gateParty, _ gateMember, _ string) string { return g.path("/profiles") },
				body:     map[string]any{"holder_email": outsider.Email},
				okStatus: http.StatusCreated, refused: []int{http.StatusForbidden, http.StatusNotFound},
				unchanged: func(t *testing.T) {
					if n := env.profileCount(t, oid, b.id); n != 0 {
						t.Errorf("the refused invitation left %d profile(s) in B", n)
					}
				},
			},
			{
				name:   "managed-user creation",
				method: http.MethodPost, path: func(g gateParty, _ gateMember, _ string) string { return g.path("/managed-users") },
				body:     map[string]string{"email": uniq("managed") + "@example.test", "password": testPassword},
				okStatus: http.StatusCreated, refused: []int{http.StatusForbidden, http.StatusNotFound},
				unchanged: func(t *testing.T) {
					if n := env.count(t, "managed users of B", `SELECT count(*) FROM mod_users.users WHERE managing_party_id = $1`, b.id); n != 0 {
						t.Errorf("the refused creation left %d managed user(s) in B", n)
					}
				},
			},
			{
				name:   "authn-policy",
				method: http.MethodPut, path: func(g gateParty, _ gateMember, _ string) string { return g.path("/authn-policy") },
				body:     map[string]any{"credential_rule": "any", "min_acr": 1}, // no lock-out of the administrator on A
				okStatus: http.StatusOK, refused: []int{http.StatusForbidden, http.StatusNotFound},
				unchanged: func(t *testing.T) {
					if n := env.count(t, "policy rows of B", `SELECT count(*) FROM mod_users.party_authn_policies WHERE party_id = $1`, b.id); n != 0 {
						t.Errorf("the refused write left %d policy row(s) for B", n)
					}
				},
			},
			{
				name:   "credential authorization",
				method: http.MethodPut, path: func(g gateParty, _ gateMember, cred string) string { return authPath(g, cred) },
				okStatus: http.StatusOK, refused: []int{http.StatusForbidden, http.StatusNotFound},
				unchanged: func(t *testing.T) {
					if n := env.authorizationCount(t, bmCred, b.id); n != 0 {
						t.Errorf("the refused write authorized B's credential (%d)", n)
					}
				},
			},
			{
				name:   "suspend",
				method: http.MethodPatch, path: func(_ gateParty, m gateMember, _ string) string { return "/v1/profiles/" + m.profUUID },
				body:     map[string]string{"status": "suspended"},
				okStatus: http.StatusOK, refused: []int{http.StatusForbidden, http.StatusNotFound},
				unchanged: func(t *testing.T) {
					if got := env.profileStatus(t, bm.profUUID); got != "active" {
						t.Errorf("B's member profile is %s after the refused suspend, want active", got)
					}
				},
			},
		}
		for _, f := range flows {
			t.Run(f.name, func(t *testing.T) {
				// Refused on B.
				r := env.do(t, f.method, f.path(b, bm, bmCred), tok, f.body)
				if !containsInt(f.refused, r.status) {
					t.Fatalf("on B: status = %d, want one of %v; body=%s", r.status, f.refused, r.raw)
				}
				f.unchanged(t)
				// The same flow succeeds on A, with the same grant and token.
				body := f.body
				if f.name == "invitation" {
					body = map[string]any{"holder_email": outsider2.Email}
				}
				if f.name == "managed-user creation" {
					body = map[string]string{"email": uniq("managed") + "@example.test", "password": testPassword}
				}
				env.do(t, f.method, f.path(a, am, amCred), tok, body).wantStatus(t, f.okStatus)
			})
		}
	})
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// The administrator of another party also cannot reach a party's profiles by
// listing them, and the owned-scope administrator can list its own party.
func TestOwnedScope_ListPartyProfilesIsScopedToTheAdministeredParty(t *testing.T) {
	inOwnedScopeMode(t, func(t *testing.T) {
		a := env.newGateParty(t)
		b := env.newGateParty(t)
		env.newGateMember(t, a)
		env.do(t, http.MethodGet, a.path("/profiles"), a.adminTok.access(), nil).wantStatus(t, http.StatusOK)
		r := env.do(t, http.MethodGet, b.path("/profiles"), a.adminTok.access(), nil)
		if r.status != http.StatusForbidden && r.status != http.StatusNotFound {
			t.Fatalf("listing B's profiles as A's administrator = %d, want 403 or 404; body=%s", r.status, r.raw)
		}
	})
}
