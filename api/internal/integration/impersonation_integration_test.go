//go:build integration

package integration

// Impersonation sessions (phase users-authorization-engine, package 3): the
// /v1/impersonations routes and the ImpersonationService over the real composed
// schema, authorization engine, authentication middleware and observers.
//
// Requirement 5 cases and the tests that cover them:
//
//	no nesting through the API and through SQL (MF101)                TestImpersonation_NoNesting
//	the administrator is the parent session's own principal           TestImpersonation_StartCreatesAChildSession
//	an Acme party admin reaches Acme profiles only                    TestImpersonation_PartyAdminReach
//	a personal profile needs operator authority; operators reach
//	members of restrictive-policy parties (decision D)                TestImpersonation_PersonalProfileNeedsOperatorAuthority
//	the impersonated member's rules apply across parties on the
//	administrator's factors; operators skip only the credential rule  TestImpersonation_ImpersonatedActorRulesApplyAcrossParties
//	the own arm never confers impersonate                             TestImpersonation_OwnArmNeverConfersImpersonate
//	stale auth_time is 409 users.step_up_required                     TestImpersonation_StaleAuthTimeNeedsStepUp
//	the TTL and the renewal cap are enforced                          TestImpersonation_TTLAndRenewalCap
//	renewal re-authorizes impersonate as the administrator's recorded
//	acting profile, without re-applying max_auth_age (decision I)     TestImpersonation_RenewRechecksTheAdministrator
//	ending the parent ends the child (parent_session_ended)           TestImpersonation_ParentLiveness
//	switch and fork inside an impersonation are refused               TestImpersonation_SwitchAndForkRefused
//	no refresh token is returned                                      TestImpersonation_StartCreatesAChildSession
//	start, renew and end emit impersonate, update and revoke on
//	resource impersonation, accepted by mod-audit's audit_log         TestImpersonation_Observations
//	route order with mod-core's impersonating identity                TestOrder_ImpersonationRoutes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/apiresp/ordertest"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/config"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func (s *stack) opID(t *testing.T, slug string) int64 {
	t.Helper()
	return s.scanInt64(t, "resolve operation "+slug, `SELECT id FROM authz_operations WHERE slug = $1`, slug)
}

// makeOperatorWith is makeOperator with a wildcard grant of the named operation
// instead of manage: an operator-party profile and a token acting as it.
func (s *stack) makeOperatorWith(t *testing.T, a account, op string) (tokens, int64) {
	t.Helper()
	party := s.operatorPartyID(t)
	profileID, profUUID := s.seedPartyProfile(t, s.principalID(t, a.Email), party, "active")
	s.exec(t, "wildcard "+op+" grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, NULL, NULL) ON CONFLICT DO NOTHING`,
		profileID, s.opID(t, op))
	return s.do(t, http.MethodPost, "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).
		wantStatus(t, http.StatusOK).tok(t), profileID
}

// impAdmin is an administrator of a party: an active party profile holding an
// owned-scope manage grant on the party, with a token acting as it.
type impAdmin struct {
	acct      account
	userID    int64
	partyID   int64
	profileID int64
	tok       tokens
}

func (s *stack) newImpAdmin(t *testing.T, partyID int64) impAdmin {
	t.Helper()
	a := s.registerVerified(t)
	userID := s.principalID(t, a.Email)
	profileID, profUUID := s.seedPartyProfile(t, userID, partyID, "active")
	s.exec(t, "owned-scope manage grant on the party",
		`INSERT INTO grants (actor_id, operation_id, target_id, scope, target_type_id, include_subtypes, granted_by)
		 VALUES ($1, $2, $3, 'owned', NULL, false, NULL)`,
		profileID, s.opID(t, "manage"), partyID)
	tok := s.do(t, http.MethodPost, "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	return impAdmin{acct: a, userID: userID, partyID: partyID, profileID: profileID, tok: tok}
}

// member is a user with an active profile in a party.
type member struct {
	acct      account
	userID    int64
	profileID int64
	profUUID  string
	personal  string // uuid of the user's personal profile
}

func (s *stack) newMember(t *testing.T, partyID int64) member {
	t.Helper()
	a := s.registerVerified(t)
	userID := s.principalID(t, a.Email)
	profileID, profUUID := s.seedPartyProfile(t, userID, partyID, "active")
	return member{acct: a, userID: userID, profileID: profileID, profUUID: profUUID,
		personal: s.profileUUID(t, s.personalProfileID(t, userID))}
}

func (s *stack) startImp(t *testing.T, token, profileUUID, reason string) *resp {
	t.Helper()
	return s.do(t, http.MethodPost, "/v1/impersonations", token, map[string]string{"profile_uuid": profileUUID, "reason": reason})
}

func (s *stack) sessionID(t *testing.T, sessionUUID string) int64 {
	t.Helper()
	return s.scanInt64(t, "session id", `SELECT id FROM mod_users.sessions WHERE uuid = $1::uuid`, sessionUUID)
}

func (s *stack) childCount(t *testing.T, parentSessionID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "child count", `SELECT count(*) FROM mod_users.sessions WHERE impersonator_session_id = $1`, parentSessionID)
}

func (s *stack) setPartyPolicy(t *testing.T, partyID int64, rule string, minACR int) {
	t.Helper()
	s.exec(t, "party policy",
		`INSERT INTO mod_users.party_authn_policies (party_id, credential_rule, min_acr) VALUES ($1, $2, $3)
		 ON CONFLICT (party_id) DO UPDATE SET credential_rule = EXCLUDED.credential_rule, min_acr = EXCLUDED.min_acr`,
		partyID, rule, minACR)
}

const impTTLSeconds = int64(30 * 60)

// ---------------------------------------------------------------------------
// start: the child session, the administrator, no refresh token
// ---------------------------------------------------------------------------

func TestImpersonation_StartCreatesAChildSession(t *testing.T) {
	acme := env.seedParty(t, "Imp Acme "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	parentUUID := admin.acct.tk.Session.UUID
	parent := env.sessionRow(t, parentUUID)

	r := env.startImp(t, admin.tok.access(), m.profUUID, "  support ticket 4711  ").wantStatus(t, http.StatusCreated)
	tk := r.tok(t)

	// No refresh token: neither in the response nor in the database.
	if tk.RefreshToken != nil {
		t.Errorf("refresh_token = %q, want null", *tk.RefreshToken)
	}
	if !strings.Contains(string(r.raw), `"refresh_token":null`) {
		t.Errorf("response does not carry an explicit null refresh_token: %s", r.raw)
	}
	if tk.Session == nil || !tk.Session.Impersonating || tk.Session.UUID == parentUUID {
		t.Fatalf("session = %+v, want a new impersonating session", tk.Session)
	}
	if tk.ExpiresIn <= 0 || tk.ExpiresIn > impTTLSeconds {
		t.Errorf("expires_in = %d, want within (0, %d]", tk.ExpiresIn, impTTLSeconds)
	}
	if tk.Profile == nil || tk.Profile.UUID != m.profUUID || len(tk.Profiles) != 1 || tk.Profiles[0].UUID != m.profUUID {
		t.Errorf("profile = %+v, profiles = %+v, want only the impersonated profile %s", tk.Profile, tk.Profiles, m.profUUID)
	}

	// The child row.
	child := env.sessionRow(t, tk.Session.UUID)
	if child.PrincipalID != m.userID || child.ImpersonatedProfileID.Int64 != m.profileID ||
		child.ImpersonatorSessionID.Int64 != parent.ID {
		t.Errorf("child = principal %d profile %d parent %d; want %d, %d, %d",
			child.PrincipalID, child.ImpersonatedProfileID.Int64, child.ImpersonatorSessionID.Int64, m.userID, m.profileID, parent.ID)
	}
	if !child.ImpersonatorProfileID.Valid || child.ImpersonatorProfileID.Int64 != admin.profileID {
		t.Errorf("child impersonator_profile_id = %+v, want the administrator's acting profile %d", child.ImpersonatorProfileID, admin.profileID)
	}
	if child.ImpersonationReason.String != "support ticket 4711" {
		t.Errorf("reason = %q, want the trimmed reason", child.ImpersonationReason.String)
	}
	if child.Acr != parent.Acr || strings.Join(child.Amr, ",") != strings.Join(parent.Amr, ",") {
		t.Errorf("child acr/amr = %d %v, want the parent's %d %v", child.Acr, child.Amr, parent.Acr, parent.Amr)
	}
	if !child.AuthTime.Time.Equal(parent.AuthTime.Time) {
		t.Errorf("child auth_time = %v, want the parent's %v", child.AuthTime.Time, parent.AuthTime.Time)
	}
	if ttl := child.ExpiresAt.Time.Sub(child.CreatedAt.Time); ttl < 29*time.Minute || ttl > 31*time.Minute {
		t.Errorf("child lifetime = %v, want AUTH_IMPERSONATION_TTL (30m)", ttl)
	}
	if child.AppID.Valid {
		t.Errorf("child app_id = %d, want none", child.AppID.Int64)
	}
	if n := env.scanInt64(t, "refresh tokens", `SELECT count(*) FROM mod_users.session_refresh_tokens WHERE session_id = $1`, child.ID); n != 0 {
		t.Errorf("the impersonation session has %d refresh tokens, want none", n)
	}

	// The token: the target holder is the subject, the real administrator is
	// the actor (the parent session's own principal).
	claims := claimsOf(t, tk.access())
	act, _ := claims["act"].(map[string]any)
	if act["sub"] != env.entityUUID(t, admin.userID) {
		t.Errorf("act.sub = %v, want the administrator's principal uuid %s", act["sub"], env.entityUUID(t, admin.userID))
	}
	if claims["sub"] != env.entityUUID(t, m.userID) || claims["prof"] != m.profUUID || claims["sid"] != tk.Session.UUID {
		t.Errorf("claims sub/prof/sid = %v / %v / %v", claims["sub"], claims["prof"], claims["sid"])
	}

	// Every request inside the impersonation records the administrator from the
	// parent session's own principal, never from the token.
	status, p := env.probe(t, "/v1/probe", tk.access())
	if status != http.StatusOK || p.ImpersonatorP != admin.userID || p.PrincipalID != m.userID ||
		p.ProfileID != m.profileID || p.SessionID != child.ID || p.CredentialID != 0 {
		t.Fatalf("probe = %d %+v; want impersonator %d, principal %d, profile %d, session %d",
			status, p, admin.userID, m.userID, m.profileID, child.ID)
	}
	// The administrator's own session is unaffected.
	if _, p := env.probe(t, "/v1/probe", admin.tok.access()); p.ImpersonatorP != 0 || p.PrincipalID != admin.userID {
		t.Errorf("the administrator's own session probes as %+v", p)
	}
}

// The child session inherits the parent's auth_time: an impersonation never
// looks more recently authenticated than the administrator who started it, so
// it cannot pass a freshness gate the administrator's own session would fail.
func TestImpersonation_ChildInheritsTheParentAuthTime(t *testing.T) {
	acme := env.seedParty(t, "Imp AuthTime "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)

	// Age the parent within the impersonate operation's max_auth_age.
	env.exec(t, "age the parent auth_time",
		`UPDATE mod_users.sessions SET auth_time = now() - interval '5 minutes' WHERE uuid = $1::uuid`, admin.acct.tk.Session.UUID)
	parent := env.sessionRow(t, admin.acct.tk.Session.UUID)

	tk := env.startImp(t, admin.tok.access(), m.profUUID, "auth time").wantStatus(t, http.StatusCreated).tok(t)
	child := env.sessionRow(t, tk.Session.UUID)
	if !child.AuthTime.Time.Equal(parent.AuthTime.Time) {
		t.Fatalf("child auth_time = %v, want the parent's %v", child.AuthTime.Time, parent.AuthTime.Time)
	}
	if age := time.Since(child.AuthTime.Time); age < 4*time.Minute {
		t.Errorf("child auth_time is %v old, want about 5 minutes", age)
	}
}

func TestImpersonation_RequestShapeAndOwnProfile(t *testing.T) {
	acme := env.seedParty(t, "Imp Shape "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	parentID := env.sessionID(t, admin.acct.tk.Session.UUID)

	env.do(t, http.MethodPost, "/v1/impersonations", "", map[string]string{"profile_uuid": m.profUUID, "reason": "x"}).
		wantStatus(t, http.StatusUnauthorized)
	for name, body := range map[string]any{
		"no reason":     map[string]string{"profile_uuid": m.profUUID},
		"blank reason":  map[string]string{"profile_uuid": m.profUUID, "reason": "   "},
		"no profile":    map[string]string{"reason": "x"},
		"bad profile":   map[string]string{"profile_uuid": "nope", "reason": "x"},
		"unknown field": map[string]string{"profile_uuid": m.profUUID, "reason": "x", "extra": "y"},
		"malformed":     `{"profile_uuid":`,
	} {
		t.Run(name, func(t *testing.T) {
			env.do(t, http.MethodPost, "/v1/impersonations", admin.tok.access(), body).wantStatus(t, http.StatusBadRequest)
		})
	}

	// Impersonating one's own profile, personal or party, is refused with the
	// impersonation code, even for a wildcard holder.
	ownParty := env.profileUUID(t, admin.profileID)
	ownPersonal := env.profileUUID(t, env.personalProfileID(t, admin.userID))
	op := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, op)
	opPersonal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, op.Email)))
	for name, c := range map[string]struct{ token, target string }{
		"admin, own party profile": {admin.tok.access(), ownParty},
		"admin, own personal":      {admin.tok.access(), ownPersonal},
		"operator, own personal":   {opTok.access(), opPersonal},
	} {
		t.Run(name, func(t *testing.T) {
			env.startImp(t, c.token, c.target, "self").wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
		})
	}
	if n := env.childCount(t, parentID); n != 0 {
		t.Errorf("refused starts left %d child sessions", n)
	}
}

// ---------------------------------------------------------------------------
// no nesting
// ---------------------------------------------------------------------------

func TestImpersonation_NoNesting(t *testing.T) {
	acme := env.seedParty(t, "Imp Nest "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	other := env.newMember(t, acme)

	child := env.startImp(t, admin.tok.access(), m.profUUID, "nest").wantStatus(t, http.StatusCreated).tok(t)
	childID := env.sessionID(t, child.Session.UUID)

	// Through the API: the impersonation session cannot start another, whoever
	// the target is and whatever authority the impersonated profile holds.
	env.startImp(t, child.access(), other.profUUID, "nest").
		wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
	if n := env.childCount(t, childID); n != 0 {
		t.Errorf("an impersonation session has %d children", n)
	}

	// Through SQL: inserting a child of an impersonation session raises MF101.
	_, err := env.pool.Exec(context.Background(),
		`INSERT INTO mod_users.sessions (principal_id, acr, amr, auth_time, expires_at,
		        impersonator_session_id, impersonated_profile_id, impersonation_reason)
		 VALUES ($1, 1, ARRAY['pwd'], now(), now() + interval '10 minutes', $2, $3, 'nested')`,
		other.userID, childID, other.profileID)
	if err == nil {
		t.Fatal("inserting a child of an impersonation session succeeded")
	}
	if !svc.IsSessionInvariantError(err) {
		t.Errorf("err = %v, want SQLSTATE MF101", err)
	}
}

// ---------------------------------------------------------------------------
// authority: owned scope, operators, the own arm
// ---------------------------------------------------------------------------

func TestImpersonation_PartyAdminReach(t *testing.T) {
	acme := env.seedParty(t, "Imp Acme "+uniq("p"))
	beta := env.seedParty(t, "Imp Beta "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	acmeMember := env.newMember(t, acme)
	betaMember := env.newMember(t, beta)
	parentID := env.sessionID(t, admin.acct.tk.Session.UUID)

	// An Acme party profile: allowed through the owned-scope manage grant.
	env.startImp(t, admin.tok.access(), acmeMember.profUUID, "acme member").wantStatus(t, http.StatusCreated)
	if n := env.childCount(t, parentID); n != 1 {
		t.Fatalf("children after the allowed start = %d, want 1", n)
	}

	// A personal profile (owned by its holder, not by Acme) and a profile of
	// another party are 403; a profile that does not exist is the same 403.
	refusals := map[string]string{
		"a personal profile":       acmeMember.personal,
		"another party's profile":  betaMember.profUUID,
		"another party's personal": betaMember.personal,
		"an unknown profile":       uuid.NewString(),
	}
	var bodies []string
	for name, target := range refusals {
		r := env.startImp(t, admin.tok.access(), target, "out of reach")
		if r.status != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403; %s", name, r.status, r.raw)
		}
		code, _ := r.errCode(t)
		if code != "forbidden" {
			t.Errorf("%s: code = %q, want forbidden", name, code)
		}
		bodies = append(bodies, string(r.raw))
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("refusals are distinguishable: %s | %s", bodies[0], b)
		}
	}
	if n := env.childCount(t, parentID); n != 1 {
		t.Errorf("children after the refusals = %d, want still 1", n)
	}
}

func TestImpersonation_PersonalProfileNeedsOperatorAuthority(t *testing.T) {
	victim := env.registerVerified(t)
	victimPersonal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, victim.Email)))

	// An ordinary user and a holder of only a wildcard read are refused.
	ordinary := env.registerVerified(t)
	env.startImp(t, ordinary.tk.access(), victimPersonal, "nosy").wantError(t, http.StatusForbidden, "forbidden", "")
	reader := env.registerVerified(t)
	readerTok, _ := env.makeOperatorWith(t, reader, "read")
	env.startImp(t, readerTok.access(), victimPersonal, "nosy").wantError(t, http.StatusForbidden, "forbidden", "")

	// A party administrator with owned-scope manage on a party is not an operator.
	acme := env.seedParty(t, "Imp NotOp "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	env.startImp(t, admin.tok.access(), victimPersonal, "nosy").wantError(t, http.StatusForbidden, "forbidden", "")

	// An operator-party profile with a wildcard manage grant, or a wildcard
	// impersonate grant, may.
	for _, op := range []string{"manage", "impersonate"} {
		t.Run("operator with wildcard "+op, func(t *testing.T) {
			a := env.registerVerified(t)
			tok, _ := env.makeOperatorWith(t, a, op)
			r := env.startImp(t, tok.access(), victimPersonal, "support").wantStatus(t, http.StatusCreated).tok(t)
			if r.Profile == nil || r.Profile.UUID != victimPersonal {
				t.Errorf("profile = %+v, want the victim's personal profile", r.Profile)
			}
			// An unknown profile is 404 for a wildcard holder only.
			env.startImp(t, tok.access(), uuid.NewString(), "support").wantError(t, http.StatusNotFound, "not_found", "")
		})
	}
	env.startImp(t, ordinary.tk.access(), uuid.NewString(), "support").wantError(t, http.StatusForbidden, "forbidden", "")
}

// Decision D: a customer party's policy never binds the platform operator, so
// an operator impersonates a member of a party with a restrictive policy. The
// impersonation session still has to satisfy the policy's min_acr, which is
// never skipped, and a refusal writes nothing.
func TestImpersonation_OperatorReachesMembersOfRestrictiveParties(t *testing.T) {
	party := env.seedParty(t, "Imp Restrictive "+uniq("p"))
	m := env.newMember(t, party)
	opAcct := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, opAcct)

	// credential_rule authorized: the member holds no credential authorized for
	// the party, and the operator is not bound by that.
	env.setPartyPolicy(t, party, "authorized", 1)
	env.startImp(t, opTok.access(), m.profUUID, "restrictive party").wantStatus(t, http.StatusCreated)

	// min_acr above the session's acr: the impersonation session (the
	// operator's acr) cannot act as the profile. 409, and no child is left.
	env.setPartyPolicy(t, party, "authorized", 3)
	before := env.scanInt64(t, "children of the profile", `SELECT count(*) FROM mod_users.sessions WHERE impersonated_profile_id = $1`, m.profileID)
	env.startImp(t, opTok.access(), m.profUUID, "high acr party").
		wantError(t, http.StatusConflict, "conflict", "users.impersonation_target_unavailable")
	after := env.scanInt64(t, "children of the profile", `SELECT count(*) FROM mod_users.sessions WHERE impersonated_profile_id = $1`, m.profileID)
	if after != before {
		t.Errorf("a refused start left %d sessions, was %d", after, before)
	}

	// A suspended profile cannot be acted as either.
	other := env.newMember(t, env.seedParty(t, "Imp Suspended "+uniq("p")))
	env.exec(t, "suspend", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, other.profileID)
	env.startImp(t, opTok.access(), other.profUUID, "suspended").
		wantError(t, http.StatusConflict, "conflict", "users.impersonation_target_unavailable")
}

// impCtx returns the AuthContext the middleware installs for an impersonation
// access token, impersonator included.
func (s *stack) impCtx(t *testing.T, token string) context.Context {
	t.Helper()
	status, p := s.probe(t, "/v1/probe", token)
	if status != http.StatusOK || p.ImpersonatorP == 0 {
		t.Fatalf("probe for an impersonation context = %d %+v", status, p)
	}
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: p.PrincipalID, ProfileID: p.ProfileID, SessionID: p.SessionID,
		ImpersonatorPrincipalID: p.ImpersonatorP, ACR: p.ACR, AuthTime: p.AuthTime,
	})
}

// Decision G, as resolved: while impersonating, the impersonated member's
// authorizations decide what the session may do, and every party's policy
// applies as for that member, evaluated on the administrator's (parent
// session's) factors. A party administrator gets no skip of a third party's
// credential_rule; a platform operator's impersonation is exempt from the
// credential rule (decision D extended) but not from min_acr.
func TestImpersonation_ImpersonatedActorRulesApplyAcrossParties(t *testing.T) {
	acme := env.seedParty(t, "Imp Rules Acme "+uniq("p"))
	beta := env.seedParty(t, "Imp Rules Beta "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	// Beta's content, which the Acme member reaches as an outside collaborator.
	content := env.entityOf(t, env.pool, "organization", &beta)
	env.exec(t, "member read grant on beta content",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`,
		m.profileID, env.opID(t, "read"), content)
	env.setPartyPolicy(t, beta, "authorized", 1)
	betaAdminProfile, _ := env.seedPartyProfile(t, env.principalID(t, env.registerVerified(t).Email), beta, "active")

	t.Run("party administrator: beta's credential rule binds", func(t *testing.T) {
		child := env.startImp(t, admin.tok.access(), m.profUUID, "rules").wantStatus(t, http.StatusCreated).tok(t)
		ctx := env.impCtx(t, child.access())
		if err := env.az.Authorize(ctx, "read", &content); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("administrator's credentials not authorized for beta: err = %v, want 403", err)
		}

		// Authorizing the administrator's own credential for beta satisfies the
		// rule: the factors are the parent session's.
		adminCred := env.scanInt64(t, "admin password credential",
			`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`, admin.userID)
		env.exec(t, "authorize admin credential for beta",
			`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			adminCred, beta, betaAdminProfile)
		if err := env.az.Authorize(ctx, "read", &content); err != nil {
			t.Fatalf("administrator's credential authorized for beta: err = %v, want allowed", err)
		}

		// The member's own credentials never count: they are not the session's.
		memberCred := env.scanInt64(t, "member password credential",
			`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`, m.userID)
		env.exec(t, "drop admin authorization", `DELETE FROM mod_users.credential_party_authorizations WHERE credential_id = $1`, adminCred)
		env.exec(t, "authorize member credential for beta",
			`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			memberCred, beta, betaAdminProfile)
		if err := env.az.Authorize(ctx, "read", &content); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("only the member's credential is authorized for beta: err = %v, want 403", err)
		}
	})

	t.Run("operator: exempt from the credential rule, not from min_acr", func(t *testing.T) {
		env.setPartyPolicy(t, beta, "authorized", 1)
		opAcct := env.registerVerified(t)
		opTok, _ := env.makeOperator(t, opAcct)
		child := env.startImp(t, opTok.access(), m.profUUID, "operator rules").wantStatus(t, http.StatusCreated).tok(t)
		ctx := env.impCtx(t, child.access())
		if err := env.az.Authorize(ctx, "read", &content); err != nil {
			t.Fatalf("operator impersonation under beta's credential rule: err = %v, want allowed", err)
		}
		env.setPartyPolicy(t, beta, "authorized", 3)
		if err := env.az.Authorize(ctx, "read", &content); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("operator impersonation below beta's min_acr: err = %v, want 403", err)
		}
		env.setPartyPolicy(t, beta, "authorized", 1)
	})
}

// The own arm confers an owner's operations over what it owns, never
// impersonate: a user reads what it owns and cannot impersonate it.
func TestImpersonation_OwnArmNeverConfersImpersonate(t *testing.T) {
	a := env.registerVerified(t)
	userID := env.principalID(t, a.Email)
	personal := env.personalProfileID(t, userID)
	owned := env.entityOf(t, env.pool, "organization", &userID) // an entity the user owns

	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: userID, ProfileID: personal, SessionID: env.sessionID(t, a.tk.Session.UUID), ACR: 1, AuthTime: time.Now(),
	})
	if err := env.az.Authorize(ctx, "read", &owned); err != nil {
		t.Fatalf("the own arm must confer read over an owned entity: %v", err)
	}
	for _, op := range []string{"impersonate", "manage"} {
		if err := env.az.Authorize(ctx, op, &owned); err == nil {
			t.Errorf("the own arm conferred %s", op)
		}
	}
}

// ---------------------------------------------------------------------------
// freshness
// ---------------------------------------------------------------------------

func TestImpersonation_StaleAuthTimeNeedsStepUp(t *testing.T) {
	victim := env.registerVerified(t)
	victimPersonal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, victim.Email)))
	opAcct := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, opAcct)
	parentID := env.sessionID(t, opAcct.tk.Session.UUID)

	// The authentication is older than the operation's ten minutes.
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '11 minutes' WHERE id = $1`, parentID)
	env.startImp(t, opTok.access(), victimPersonal, "stale").wantAction(t, http.StatusConflict, "users.step_up_required")
	if n := env.childCount(t, parentID); n != 0 {
		t.Fatalf("a stale start left %d children", n)
	}

	// A step-up refreshes auth_time, and the start goes through.
	env.do(t, http.MethodPost, "/v1/session/step-up/password", opTok.access(), map[string]string{"password": opAcct.Password}).
		wantStatus(t, http.StatusOK)
	child := env.startImp(t, opTok.access(), victimPersonal, "fresh").wantStatus(t, http.StatusCreated).tok(t)
	if age := time.Since(env.sessionRow(t, child.Session.UUID).AuthTime.Time); age > time.Minute {
		t.Errorf("the child's auth_time is %v old, want fresh", age)
	}

	// Inside the impersonation session the step-up routes refuse (its factors
	// are the impersonated holder's).
	env.do(t, http.MethodPost, "/v1/session/step-up/password", child.access(), map[string]string{"password": "whatever-it-is"}).
		wantAction(t, http.StatusConflict, "users.step_up_required")
}

// ---------------------------------------------------------------------------
// lifetime: TTL, renewal, cap
// ---------------------------------------------------------------------------

func TestImpersonation_TTLAndRenewalCap(t *testing.T) {
	victim := env.registerVerified(t)
	victimPersonal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, victim.Email)))
	opAcct := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, opAcct)

	child := env.startImp(t, opTok.access(), victimPersonal, "ttl").wantStatus(t, http.StatusCreated).tok(t)
	row := env.sessionRow(t, child.Session.UUID)
	cfg := config.DefaultImpersonationTTL
	if got := row.ExpiresAt.Time.Sub(row.CreatedAt.Time); got < cfg-5*time.Second || got > cfg+5*time.Second {
		t.Fatalf("start lifetime = %v, want AUTH_IMPERSONATION_TTL %v", got, cfg)
	}

	// A renewal extends the expiry to now + TTL and returns a fresh token, no
	// refresh token.
	renewed := env.do(t, http.MethodPost, "/v1/impersonations/current/renew", child.access(), nil).wantStatus(t, http.StatusOK).tok(t)
	if renewed.RefreshToken != nil {
		t.Error("a renewal minted a refresh token")
	}
	if renewed.ExpiresIn <= 0 || renewed.ExpiresIn > impTTLSeconds {
		t.Errorf("renewed expires_in = %d, want within (0, %d]", renewed.ExpiresIn, impTTLSeconds)
	}
	after := env.sessionRow(t, child.Session.UUID)
	if !after.ExpiresAt.Time.After(row.ExpiresAt.Time) {
		t.Errorf("expiry %v did not move past %v", after.ExpiresAt.Time, row.ExpiresAt.Time)
	}
	if status, p := env.probe(t, "/v1/probe", renewed.access()); status != http.StatusOK || p.ImpersonatorP == 0 {
		t.Errorf("the renewed token probes as %d %+v", status, p)
	}

	// Near the cap a renewal is cut at AUTH_IMPERSONATION_MAX from the start...
	env.exec(t, "age the session to 59 minutes old",
		`UPDATE mod_users.sessions SET created_at = now() - interval '59 minutes', expires_at = now() + interval '20 seconds' WHERE uuid = $1::uuid`,
		child.Session.UUID)
	capped := env.do(t, http.MethodPost, "/v1/impersonations/current/renew", child.access(), nil).wantStatus(t, http.StatusOK).tok(t)
	row = env.sessionRow(t, child.Session.UUID)
	limit := row.CreatedAt.Time.Add(config.DefaultImpersonationMax)
	if d := row.ExpiresAt.Time.Sub(limit); d < -time.Second || d > time.Second {
		t.Errorf("capped expiry = %v, want created_at + 60m = %v", row.ExpiresAt.Time, limit)
	}
	if capped.ExpiresIn > 61 {
		t.Errorf("capped expires_in = %d, want at most the minute left", capped.ExpiresIn)
	}
	// ... and then there is no room.
	env.do(t, http.MethodPost, "/v1/impersonations/current/renew", child.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.impersonation_limit_reached")
	if again := env.sessionRow(t, child.Session.UUID); !again.ExpiresAt.Time.Equal(row.ExpiresAt.Time) {
		t.Errorf("a refused renewal moved the expiry to %v", again.ExpiresAt.Time)
	}

	// An expired impersonation session is not live, and cannot be renewed.
	env.exec(t, "expire the session", `UPDATE mod_users.sessions SET expires_at = now() - interval '1 second' WHERE uuid = $1::uuid`, child.Session.UUID)
	env.do(t, http.MethodPost, "/v1/impersonations/current/renew", child.access(), nil).
		wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	if status, _ := env.probe(t, "/v1/probe", child.access()); status != http.StatusUnauthorized {
		t.Errorf("an expired impersonation probes as %d, want 401", status)
	}
}

// Renewal re-authorizes impersonate on the impersonated profile from the
// parent session, as the acting profile recorded at the start (decision I): a
// revoked grant or an acting profile that is no longer available makes the next
// renewal fail with 403 and ends the impersonation; an unchanged grant renews
// up to AUTH_IMPERSONATION_MAX, even when the parent's authentication is older
// than the operation's max_auth_age.
func TestImpersonation_RenewRechecksTheAdministrator(t *testing.T) {
	acme := env.seedParty(t, "Imp Recheck "+uniq("p"))
	m := env.newMember(t, acme)
	renew := func(token string) *resp {
		return env.do(t, http.MethodPost, "/v1/impersonations/current/renew", token, nil)
	}
	wantEnded := func(t *testing.T, child tokens) {
		t.Helper()
		row := env.sessionRow(t, child.Session.UUID)
		if row.RevokedAt == nil || row.RevokeReason.String != svc.SessionRevokeImpersonationEnded {
			t.Errorf("child revoke = %v %q, want impersonation_ended", row.RevokedAt, row.RevokeReason.String)
		}
		if status, _ := env.probe(t, "/v1/probe", child.access()); status != http.StatusUnauthorized {
			t.Errorf("the refused impersonation probes as %d, want 401", status)
		}
		renew(child.access()).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	}

	t.Run("a revoked impersonate grant ends the impersonation on renewal", func(t *testing.T) {
		admin := env.newImpAdmin(t, acme)
		child := env.startImp(t, admin.tok.access(), m.profUUID, "revoked grant").wantStatus(t, http.StatusCreated).tok(t)
		renew(child.access()).wantStatus(t, http.StatusOK)

		env.exec(t, "revoke the administrator's grant",
			`DELETE FROM grants WHERE actor_id = $1 AND target_id = $2 AND scope = 'owned'`, admin.profileID, acme)
		before := env.sessionRow(t, child.Session.UUID)
		env.obs.reset()
		renew(child.access()).wantError(t, http.StatusForbidden, "forbidden", "")
		env.checkObservations(t, "refused renew", "revoke impersonation")
		if after := env.sessionRow(t, child.Session.UUID); !after.ExpiresAt.Time.Equal(before.ExpiresAt.Time) {
			t.Errorf("a refused renewal moved the expiry to %v", after.ExpiresAt.Time)
		}
		wantEnded(t, child)
		// The administrator's own session is not ended.
		if status, _ := env.probe(t, "/v1/probe", admin.tok.access()); status != http.StatusOK {
			t.Errorf("the administrator's session probes as %d after the refusal", status)
		}
	})

	t.Run("a suspended administrator profile ends the impersonation on renewal", func(t *testing.T) {
		admin := env.newImpAdmin(t, acme)
		child := env.startImp(t, admin.tok.access(), m.profUUID, "suspended admin").wantStatus(t, http.StatusCreated).tok(t)

		env.exec(t, "suspend the administrator's profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, admin.profileID)
		// The child itself stays live until the renewal: its liveness follows
		// the parent session, which does not carry a profile.
		if status, _ := env.probe(t, "/v1/probe", child.access()); status != http.StatusOK {
			t.Fatalf("child probe before the renewal = %d", status)
		}
		renew(child.access()).wantError(t, http.StatusForbidden, "forbidden", "")
		wantEnded(t, child)
	})

	t.Run("an unchanged grant renews up to AUTH_IMPERSONATION_MAX without re-applying max_auth_age", func(t *testing.T) {
		admin := env.newImpAdmin(t, acme)
		parentID := env.sessionID(t, admin.acct.tk.Session.UUID)
		child := env.startImp(t, admin.tok.access(), m.profUUID, "unchanged").wantStatus(t, http.StatusCreated).tok(t)

		// The administrator's authentication is older than impersonate's
		// max_auth_age (ten minutes): a new start needs a step-up, a renewal
		// does not.
		env.exec(t, "age the parent auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '11 minutes' WHERE id = $1`, parentID)
		env.startImp(t, admin.tok.access(), m.profUUID, "stale").wantAction(t, http.StatusConflict, "users.step_up_required")
		renewed := renew(child.access()).wantStatus(t, http.StatusOK).tok(t)
		if status, p := env.probe(t, "/v1/probe", renewed.access()); status != http.StatusOK || p.ImpersonatorP != admin.userID {
			t.Errorf("the renewed token probes as %d %+v", status, p)
		}

		env.exec(t, "age the session to 59 minutes old",
			`UPDATE mod_users.sessions SET created_at = now() - interval '59 minutes', expires_at = now() + interval '20 seconds' WHERE uuid = $1::uuid`,
			child.Session.UUID)
		renew(child.access()).wantStatus(t, http.StatusOK)
		row := env.sessionRow(t, child.Session.UUID)
		limit := row.CreatedAt.Time.Add(config.DefaultImpersonationMax)
		if d := row.ExpiresAt.Time.Sub(limit); d < -time.Second || d > time.Second {
			t.Errorf("capped expiry = %v, want created_at + AUTH_IMPERSONATION_MAX = %v", row.ExpiresAt.Time, limit)
		}
		renew(child.access()).wantError(t, http.StatusConflict, "conflict", "users.impersonation_limit_reached")
		if row := env.sessionRow(t, child.Session.UUID); row.RevokedAt != nil {
			t.Errorf("the capped impersonation was revoked (%q); want it live until it expires", row.RevokeReason.String)
		}
	})
}

func TestImpersonation_RenewAndEndOutsideAnImpersonation(t *testing.T) {
	a := env.registerVerified(t)
	env.do(t, http.MethodPost, "/v1/impersonations/current/renew", a.tk.access(), nil).
		wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
	env.do(t, http.MethodDelete, "/v1/impersonations/current", a.tk.access(), nil).
		wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
	env.do(t, http.MethodPost, "/v1/impersonations/current/renew", "", nil).wantStatus(t, http.StatusUnauthorized)
	env.do(t, http.MethodDelete, "/v1/impersonations/current", "", nil).wantStatus(t, http.StatusUnauthorized)
	// The caller's own session is untouched by the refusals.
	if status, _ := env.probe(t, "/v1/probe", a.tk.access()); status != http.StatusOK {
		t.Errorf("the caller's session probes as %d after the refusals", status)
	}
}

func TestImpersonation_EndRevokesTheChildOnly(t *testing.T) {
	acme := env.seedParty(t, "Imp End "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	child := env.startImp(t, admin.tok.access(), m.profUUID, "end").wantStatus(t, http.StatusCreated).tok(t)

	env.do(t, http.MethodDelete, "/v1/impersonations/current", child.access(), nil).wantStatus(t, http.StatusNoContent)
	row := env.sessionRow(t, child.Session.UUID)
	if row.RevokedAt == nil || row.RevokeReason.String != svc.SessionRevokeImpersonationEnded {
		t.Errorf("child revoke = %v %q, want impersonation_ended", row.RevokedAt, row.RevokeReason.String)
	}
	env.do(t, http.MethodDelete, "/v1/impersonations/current", child.access(), nil).wantStatus(t, http.StatusUnauthorized)
	if status, _ := env.probe(t, "/v1/probe", child.access()); status != http.StatusUnauthorized {
		t.Errorf("the ended impersonation probes as %d, want 401", status)
	}
	// The administrator's session lives on, and may impersonate again.
	if status, _ := env.probe(t, "/v1/probe", admin.tok.access()); status != http.StatusOK {
		t.Errorf("the administrator's session probes as %d after the end", status)
	}
	env.startImp(t, admin.tok.access(), m.profUUID, "again").wantStatus(t, http.StatusCreated)
}

// ---------------------------------------------------------------------------
// parent liveness
// ---------------------------------------------------------------------------

func TestImpersonation_ParentLiveness(t *testing.T) {
	acme := env.seedParty(t, "Imp Parent "+uniq("p"))
	m := env.newMember(t, acme)

	t.Run("an expired parent ends the child on its next request (parent_session_ended)", func(t *testing.T) {
		admin := env.newImpAdmin(t, acme)
		parentID := env.sessionID(t, admin.acct.tk.Session.UUID)
		child := env.startImp(t, admin.tok.access(), m.profUUID, "expiry").wantStatus(t, http.StatusCreated).tok(t)
		if status, _ := env.probe(t, "/v1/probe", child.access()); status != http.StatusOK {
			t.Fatalf("child probe before = %d", status)
		}

		env.exec(t, "expire the parent", `UPDATE mod_users.sessions SET expires_at = now() - interval '1 second' WHERE id = $1`, parentID)
		var status string
		if err := env.pool.QueryRow(context.Background(),
			`SELECT status FROM mod_users.session_liveness($1::uuid, $2::uuid, $3::uuid)`,
			child.Session.UUID, env.entityUUID(t, m.userID), m.profUUID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "parent_session_ended" {
			t.Errorf("child liveness = %q, want parent_session_ended", status)
		}
		env.do(t, http.MethodGet, "/v1/probe", child.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	})

	t.Run("revoking the parent revokes the child", func(t *testing.T) {
		admin := env.newImpAdmin(t, acme)
		child := env.startImp(t, admin.tok.access(), m.profUUID, "logout").wantStatus(t, http.StatusCreated).tok(t)
		env.do(t, http.MethodPost, "/v1/session/logout", admin.tok.access(), nil).wantStatus(t, http.StatusNoContent)
		env.do(t, http.MethodGet, "/v1/probe", child.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
		row := env.sessionRow(t, child.Session.UUID)
		if row.RevokedAt == nil || row.RevokeReason.String != svc.SessionRevokeParentRevoked {
			t.Errorf("child revoke = %v %q, want parent_revoked", row.RevokedAt, row.RevokeReason.String)
		}
	})
}

// ---------------------------------------------------------------------------
// no switching inside
// ---------------------------------------------------------------------------

func TestImpersonation_SwitchAndForkRefused(t *testing.T) {
	acme := env.seedParty(t, "Imp Switch "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	child := env.startImp(t, admin.tok.access(), m.profUUID, "switch").wantStatus(t, http.StatusCreated).tok(t)

	// The holder's personal profile and the impersonated profile itself: both
	// switches are refused, as is a fork.
	for _, target := range []string{m.personal, m.profUUID} {
		env.do(t, http.MethodPost, "/v1/session/switch", child.access(), map[string]string{"profile_uuid": target}).
			wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
	}
	env.do(t, http.MethodPost, "/v1/session/fork", child.access(), nil).
		wantError(t, http.StatusForbidden, svc.CodeImpersonationNotAllowed, "")
	if n := env.scanInt64(t, "refresh tokens", `SELECT count(*) FROM mod_users.session_refresh_tokens WHERE session_id = $1`,
		env.sessionID(t, child.Session.UUID)); n != 0 {
		t.Errorf("a refused fork left %d refresh tokens", n)
	}

	// The session's only profile is the impersonated one.
	var info struct {
		Session  *struct{ Impersonating bool } `json:"session"`
		Profile  *profileView                  `json:"profile"`
		Profiles []profileView                 `json:"profiles"`
	}
	env.do(t, http.MethodGet, "/v1/session", child.access(), nil).wantStatus(t, http.StatusOK).into(t, &info)
	if info.Session == nil || !info.Session.Impersonating || len(info.Profiles) != 1 || info.Profiles[0].UUID != m.profUUID ||
		info.Profile == nil || info.Profile.UUID != m.profUUID {
		t.Errorf("session info = %+v, want only the impersonated profile", info)
	}
}

// ---------------------------------------------------------------------------
// audit
// ---------------------------------------------------------------------------

func TestImpersonation_Observations(t *testing.T) {
	acme := env.seedParty(t, "Imp Audit "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	m := env.newMember(t, acme)
	parentID := env.sessionID(t, admin.acct.tk.Session.UUID)

	// Start: the administrator's own profile and session, no impersonator.
	env.obs.reset()
	child := env.startImp(t, admin.tok.access(), m.profUUID, "audit trail").wantStatus(t, http.StatusCreated).tok(t)
	env.checkObservations(t, "start", "impersonate impersonation")
	childID := env.sessionID(t, child.Session.UUID)
	o := env.obs.all()[0]
	if o.Target != m.profileID || o.Auth.ProfileID != admin.profileID || o.Auth.PrincipalID != admin.userID ||
		o.Auth.SessionID != parentID || o.Auth.ImpersonatorPrincipalID != 0 {
		t.Errorf("start observation = target %d, auth %+v; want target %d as the administrator (profile %d, session %d, no impersonator)",
			o.Target, o.Auth, m.profileID, admin.profileID, parentID)
	}
	after, _ := o.After.(map[string]any)
	if after["session_uuid"] != child.Session.UUID || after["reason"] != "audit trail" {
		t.Errorf("start after = %v, want the child session uuid and the reason", o.After)
	}

	// Renew and end run inside the child session: the impersonated profile acts,
	// the impersonator principal and the child session id are recorded.
	env.obs.reset()
	renewed := env.do(t, http.MethodPost, "/v1/impersonations/current/renew", child.access(), nil).wantStatus(t, http.StatusOK).tok(t)
	env.checkObservations(t, "renew", "update impersonation")
	env.obs.reset()
	env.do(t, http.MethodDelete, "/v1/impersonations/current", renewed.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "end", "revoke impersonation")
	o = env.obs.all()[0]
	if o.Target != m.profileID || o.Auth.ProfileID != m.profileID || o.Auth.PrincipalID != m.userID ||
		o.Auth.ImpersonatorPrincipalID != admin.userID || o.Auth.SessionID != childID {
		t.Errorf("end observation = target %d, auth %+v; want the impersonated profile acting for impersonator %d in session %d",
			o.Target, o.Auth, admin.userID, childID)
	}

	// mod-audit's real observer and audit_log accepted the three rows.
	if !env.auditApplied {
		t.Skip("mod-audit is not linked: audit_log was not applied")
	}
	rows, err := env.pool.Query(context.Background(),
		`SELECT op, resource, actor_entity_id, principal_id, impersonator_principal_id, session_id
		   FROM audit_log WHERE target_entity_id = $1 ORDER BY id`, m.profileID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		op, resource          string
		actor, principal      int64
		impersonator, session *int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.op, &r.resource, &r.actor, &r.principal, &r.impersonator, &r.session); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("audit rows about the impersonated profile = %d, want 3 (impersonate, update, revoke): %+v", len(got), got)
	}
	for i, want := range []string{"impersonate", "update", "revoke"} {
		if got[i].op != want || got[i].resource != "impersonation" {
			t.Errorf("audit row %d = %s %s, want %s impersonation", i, got[i].op, got[i].resource, want)
		}
	}
	if got[0].actor != admin.profileID || got[0].principal != admin.userID || got[0].impersonator != nil ||
		got[0].session == nil || *got[0].session != parentID {
		t.Errorf("start row = %+v, want the administrator's profile and session, no impersonator", got[0])
	}
	for _, r := range got[1:] {
		if r.actor != m.profileID || r.principal != m.userID || r.impersonator == nil || *r.impersonator != admin.userID ||
			r.session == nil || *r.session != childID {
			t.Errorf("%s row = %+v, want the impersonator principal %d and the child session %d", r.op, r, admin.userID, childID)
		}
	}
}

// ---------------------------------------------------------------------------
// route order (apiresp/ordertest), with mod-core's impersonating identity
// ---------------------------------------------------------------------------

func TestOrder_ImpersonationRoutes(t *testing.T) {
	h := env.orderRouter()
	opAcct := env.registerVerified(t)
	_, opProfile := env.makeOperator(t, opAcct)
	operator := env.asSession(t, opAcct, opProfile)
	opUser := env.principalID(t, opAcct.Email)

	ordinary := env.registerVerified(t)
	ordinaryID := env.principalID(t, ordinary.Email)
	ordinaryIdentity := env.asSession(t, ordinary, env.personalProfileID(t, ordinaryID))

	acme := env.seedParty(t, "Imp Order "+uniq("p"))
	target := env.newMember(t, acme)
	startBody := []byte(`{"profile_uuid":"` + target.profUUID + `","reason":"order test"}`)
	badBody := []byte(`{"profile_uuid":`)

	// A fresh impersonation session of the operator on every call: the identity
	// is mod-core's impersonating AuthContext, bound to a real child session so
	// the routes find it. The renew and end calls change it, so each invocation
	// gets its own.
	impersonating := func(ctx context.Context) context.Context {
		parentID := env.sessionID(t, opAcct.tk.Session.UUID)
		adminCtx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: opUser, ProfileID: opProfile, SessionID: parentID, ACR: 1, AuthTime: time.Now(),
		})
		tk, err := env.imps.Start(adminCtx, svc.StartImpersonationInput{
			CallerSessionID: parentID, ProfileUUID: parseUUID(target.profUUID), Reason: "order test child",
		})
		if err != nil {
			t.Fatalf("start the child impersonation: %v", err)
		}
		ctx = ordertest.Impersonating(target.profileID, opUser)(ctx)
		ac, _ := opctx.Auth(ctx)
		ac.PrincipalID = target.userID
		ac.SessionID = env.sessionID(t, tk.Session.UUID.String())
		ac.ACR, ac.AuthTime = 1, time.Now()
		return opctx.WithAuthContext(ctx, ac)
	}

	ordertest.Run(t,
		ordertest.Route{
			Name: "POST /v1/impersonations (no authority)", Handler: h, Method: http.MethodPost,
			ExistingPath: "/v1/impersonations", MissingPath: "/v1/impersonations", AuthorizedMissingStatus: http.StatusCreated,
			GoodBody: startBody, BadBody: badBody,
			Authorized: operator, Unauthorized: ordinaryIdentity,
		},
		ordertest.Route{
			Name: "POST /v1/impersonations (impersonating caller)", Handler: h, Method: http.MethodPost,
			ExistingPath: "/v1/impersonations", MissingPath: "/v1/impersonations", AuthorizedMissingStatus: http.StatusCreated,
			GoodBody: startBody, BadBody: badBody,
			Authorized: operator, Unauthorized: ordertest.Impersonating(target.profileID, opUser),
		},
		ordertest.Route{
			Name: "POST /v1/impersonations/current/renew", Handler: h, Method: http.MethodPost,
			ExistingPath: "/v1/impersonations/current/renew", MissingPath: "/v1/impersonations/current/renew",
			AuthorizedMissingStatus: http.StatusOK,
			Authorized:              impersonating, Unauthorized: operator,
		},
		ordertest.Route{
			Name: "DELETE /v1/impersonations/current", Handler: h, Method: http.MethodDelete,
			ExistingPath: "/v1/impersonations/current", MissingPath: "/v1/impersonations/current",
			AuthorizedMissingStatus: http.StatusNoContent,
			Authorized:              impersonating, Unauthorized: operator,
		},
	)
}
