//go:build integration

package integration

// Party gating administration over HTTP against the real services, database
// functions and the real mod-audit observer: the authn policy, credential
// authorizations and confinements, who may write them (a party administrator,
// or the credential's holder through a session that already satisfies the
// party's gate through another factor), and that a policy change takes effect
// on the next request.

import (
	"context"
	"net/http"
	"slices"
	"testing"
)

// gateParty is a party with an operator administrator.
type gateParty struct {
	id       int64
	uuid     string
	admin    account
	adminTok tokens
}

func (g gateParty) path(rest string) string { return "/v1/parties/" + g.uuid + rest }

func (s *stack) newGateParty(t *testing.T) gateParty {
	t.Helper()
	g := gateParty{admin: s.register(t)}
	if ownedScopeAdmins {
		g.id = s.seedParty(t, "Gate Party "+uniq("p"))
		g.uuid = s.entityUUID(t, g.id)
		g.adminTok, _ = s.makeOwnedAdmin(t, g.admin, g.id)
		return g
	}
	g.adminTok, _ = s.makeOperator(t, g.admin)
	g.id = s.seedParty(t, "Gate Party "+uniq("p"))
	g.uuid = s.entityUUID(t, g.id)
	return g
}

// setPolicy PUTs the policy as the administrator.
func (s *stack) setPolicy(t *testing.T, g gateParty, body map[string]any) *resp {
	t.Helper()
	r := s.do(t, http.MethodPut, g.path("/authn-policy"), g.adminTok.access(), body)
	if ownedScopeAdmins && r.status == http.StatusOK {
		s.keepOwnedAdminUnlocked(t, g.id, body)
	}
	return r
}

// gateMember is a user holding an active profile in the party.
type gateMember struct {
	acct      account
	userID    int64
	profileID int64
	profUUID  string
}

func (s *stack) newGateMember(t *testing.T, g gateParty) gateMember {
	t.Helper()
	a := s.register(t)
	m := gateMember{acct: a, userID: s.principalID(t, a.Email)}
	m.profileID, m.profUUID = s.seedPartyProfile(t, m.userID, g.id, "active")
	return m
}

// partySession signs the member in with the password and acts as the party
// profile. It returns the party token and the personal token of the session.
func (s *stack) partySession(t *testing.T, m gateMember) (party, personal tokens) {
	t.Helper()
	personal = s.login(t, m.acct)
	party = s.do(t, http.MethodPost, "/v1/session/switch", personal.access(), map[string]string{"profile_uuid": m.profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	return party, personal
}

// withEmailFactor signs the member in with the password, steps up with an email
// code and acts as the party profile: a session holding the password and
// email_otp factors. The member's email must already be verified (that creates
// the email_otp credential).
func (s *stack) withEmailFactor(t *testing.T, m gateMember) tokens {
	t.Helper()
	tk := s.login(t, m.acct)
	seen := s.mail.count(m.acct.Email)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", tk.access(), map[string]string{"email": m.acct.Email}).
		wantStatus(t, http.StatusNoContent)
	code := s.mail.codeAfter(t, m.acct.Email, seen)
	s.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", tk.access(),
		map[string]string{"email": m.acct.Email, "code": code}).wantStatus(t, http.StatusOK)
	return s.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": m.profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
}

// addProviderRow inserts an OIDC provider row (a required provider of a party
// policy references one) and returns its id.
func (s *stack) addProviderRow(t *testing.T, id string) string {
	t.Helper()
	s.exec(t, "provider row", `INSERT INTO mod_users.oidc_providers (id) VALUES ($1)`, id)
	return id
}

func (s *stack) credentialID(t *testing.T, credUUID string) int64 {
	t.Helper()
	return s.scanInt64(t, "credential id", `SELECT id FROM mod_users.credentials WHERE uuid = $1::uuid`, credUUID)
}

func (s *stack) authorizationCount(t *testing.T, credUUID string, partyID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "authorization count",
		`SELECT count(*) FROM mod_users.credential_party_authorizations a JOIN mod_users.credentials c ON c.id = a.credential_id
		  WHERE c.uuid = $1::uuid AND a.party_id = $2`, credUUID, partyID)
}

func (s *stack) confinedTo(t *testing.T, credUUID string) int64 {
	t.Helper()
	return s.scanInt64(t, "confined party",
		`SELECT COALESCE(confined_to_party_id, 0) FROM mod_users.credentials WHERE uuid = $1::uuid`, credUUID)
}

// wantRefused fails unless the response has the refusal status. A caller that
// holds neither door and cannot read the party gets the masked 404 (no member
// profile reads the party entity in these fixtures); a caller that can read it
// gets 403.
func (r *resp) wantRefused(t *testing.T, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body=%s", r.status, status, r.raw)
	}
}

func authPath(g gateParty, credUUID string) string {
	return g.path("/credential-authorizations/" + credUUID)
}
func confPath(g gateParty, credUUID string) string { return g.path("/confinements/" + credUUID) }

func TestPartyPolicy_AuthnPolicyCRUDAndChecks(t *testing.T) {
	g := env.newGateParty(t)

	// No row: the effective default.
	v := env.do(t, http.MethodGet, g.path("/authn-policy"), g.adminTok.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if v["credential_rule"] != "any" || v["min_acr"] != float64(1) || v["is_default"] != true || v["required_oidc_provider_id"] != nil {
		t.Fatalf("default policy = %v", v)
	}

	env.obs.reset()
	v = env.setPolicy(t, g, map[string]any{"credential_rule": "authorized", "min_acr": 2}).wantStatus(t, http.StatusOK).obj(t)
	if v["credential_rule"] != "authorized" || v["min_acr"] != float64(2) || v["is_default"] != false {
		t.Fatalf("created policy = %v", v)
	}
	// min_acr defaults to 1 when omitted; the same policy twice changes nothing.
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	got := env.do(t, http.MethodGet, g.path("/authn-policy"), g.adminTok.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if got["credential_rule"] != "authorized" || got["min_acr"] != float64(1) || got["is_default"] != false {
		t.Fatalf("stored policy = %v", got)
	}
	var pairs []string
	for _, p := range env.obs.pairs() {
		if p == "create party_authn_policy" || p == "update party_authn_policy" {
			pairs = append(pairs, p)
		}
	}
	if want := []string{"create party_authn_policy", "update party_authn_policy"}; !slices.Equal(pairs, want) {
		t.Errorf("policy observations = %v, want %v", pairs, want)
	}

	// The table's checks surface as 400 field errors.
	for _, tc := range map[string]struct {
		body map[string]any
		code string
	}{
		"unknown rule":               {map[string]any{"credential_rule": "sometimes"}, "users.credential_rule_invalid"},
		"no rule":                    {map[string]any{"min_acr": 1}, "users.credential_rule_required"},
		"provider rule, no provider": {map[string]any{"credential_rule": "provider"}, "users.provider_required"},
		"provider with other rule":   {map[string]any{"credential_rule": "any", "required_oidc_provider_id": "corp"}, "users.provider_unexpected"},
		"min_acr 0":                  {map[string]any{"credential_rule": "any", "min_acr": 0}, "users.min_acr_invalid"},
		"min_acr 4":                  {map[string]any{"credential_rule": "any", "min_acr": 4}, "users.min_acr_invalid"},
		"unknown provider":           {map[string]any{"credential_rule": "provider", "required_oidc_provider_id": "no-such-provider"}, "users.oidc_provider_unknown"},
	} {
		env.setPolicy(t, g, tc.body).wantError(t, http.StatusBadRequest, "invalid_input", tc.code)
	}

	// A provider rule with an existing provider.
	provider := env.addProviderRow(t, "gate-"+uniq("idp"))
	v = env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).
		wantStatus(t, http.StatusOK).obj(t)
	if v["required_oidc_provider_id"] != provider {
		t.Fatalf("provider policy = %v", v)
	}
	// Leaving the provider rule clears the provider.
	v = env.setPolicy(t, g, map[string]any{"credential_rule": "any"}).wantStatus(t, http.StatusOK).obj(t)
	if v["required_oidc_provider_id"] != nil {
		t.Fatalf("policy after leaving the provider rule = %v", v)
	}

	// An unknown party is a 404; a caller who is not an administrator is refused.
	env.do(t, http.MethodGet, "/v1/parties/00000000-0000-4000-8000-0000000000aa/authn-policy", g.adminTok.access(), nil).
		wantStatus(t, http.StatusNotFound)
	other := env.register(t)
	env.do(t, http.MethodPut, g.path("/authn-policy"), other.tk.access(), map[string]any{"credential_rule": "any"}).
		wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodGet, g.path("/authn-policy"), other.tk.access(), nil).wantStatus(t, http.StatusNotFound)
}

func TestPartyPolicy_PolicyWriteNeedsFreshAuthenticationAndAnAdministrator(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	memberTok, _ := env.partySession(t, m)

	// A member without `update` on the party is refused; this member cannot read
	// the party entity either, so the refusal is the masked 404.
	env.do(t, http.MethodPut, g.path("/authn-policy"), memberTok.access(), map[string]any{"credential_rule": "any"}).
		wantStatus(t, http.StatusNotFound)

	// The administrator past the freshness window gets the step-up action.
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '2 hours' WHERE uuid = $1::uuid`, g.adminTok.Session.UUID)
	env.setPolicy(t, g, map[string]any{"credential_rule": "any"}).wantAction(t, http.StatusConflict, "users.step_up_required")
	env.exec(t, "refresh auth_time", `UPDATE mod_users.sessions SET auth_time = now() WHERE uuid = $1::uuid`, g.adminTok.Session.UUID)
	env.setPolicy(t, g, map[string]any{"credential_rule": "any"}).wantStatus(t, http.StatusOK)
}

func TestPartyPolicy_EntityScopeAdministratorMayWrite(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	var opID int64
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM authz_operations WHERE slug = 'update'`).Scan(&opID); err != nil {
		t.Fatalf("resolve update: %v", err)
	}
	env.exec(t, "entity-scope update grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`, m.profileID, opID, g.id)
	tok, _ := env.partySession(t, m)
	env.do(t, http.MethodGet, g.path("/credential-authorizations"), tok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, g.path("/authn-policy"), tok.access(), map[string]any{"credential_rule": "any", "min_acr": 1}).
		wantStatus(t, http.StatusOK)
}

func TestPartyPolicy_AdminAuthorizesListsAndRemovesACredential(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	credUUID := env.credentialUUID(t, m.acct.tk.access(), "password")

	env.obs.reset()
	v := env.do(t, http.MethodPut, authPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if v["credential_uuid"] != credUUID || v["kind"] != "password" || v["holder_uuid"] != env.entityUUID(t, m.userID) || v["authorized_at"] == nil {
		t.Fatalf("authorization = %v", v)
	}
	// authorized_by_profile is the acting profile.
	var by int64
	if err := env.pool.QueryRow(context.Background(), `SELECT authorized_by_profile_id FROM mod_users.credential_party_authorizations a
	  JOIN mod_users.credentials c ON c.id = a.credential_id WHERE c.uuid = $1::uuid`, credUUID).Scan(&by); err != nil {
		t.Fatalf("authorized_by: %v", err)
	}
	if v["authorized_by_profile_uuid"] != env.profileUUID(t, by) {
		t.Errorf("authorized_by_profile_uuid = %v, want %s", v["authorized_by_profile_uuid"], env.profileUUID(t, by))
	}
	// Idempotent: a second PUT changes nothing and observes nothing more.
	env.do(t, http.MethodPut, authPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.obs.pairs(); !slices.Equal(got, []string{"create credential_party_authorization"}) {
		t.Errorf("observations = %v, want one create", got)
	}

	var list struct {
		Authorizations []map[string]any `json:"authorizations"`
	}
	env.do(t, http.MethodGet, g.path("/credential-authorizations"), g.adminTok.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
	if len(list.Authorizations) != 1 || list.Authorizations[0]["credential_uuid"] != credUUID {
		t.Fatalf("authorizations = %v", list.Authorizations)
	}

	env.obs.reset()
	env.do(t, http.MethodDelete, authPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusNoContent)
	if n := env.authorizationCount(t, credUUID, g.id); n != 0 {
		t.Errorf("authorizations after delete = %d", n)
	}
	if got := env.obs.pairs(); !slices.Equal(got, []string{"delete credential_party_authorization"}) {
		t.Errorf("observations = %v, want one delete", got)
	}
	env.do(t, http.MethodDelete, authPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)

	// The credential must belong to a principal holding a profile in the party.
	stranger := env.register(t)
	strangerCred := env.credentialUUID(t, stranger.tk.access(), "password")
	env.do(t, http.MethodPut, authPath(g, strangerCred), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodPut, authPath(g, "00000000-0000-4000-8000-000000000001"), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	// An ended profile no longer qualifies.
	env.exec(t, "end profile", `UPDATE profiles SET status = 'ended', ended_at = now(), end_reason = 'test' WHERE entity_id = $1`, m.profileID)
	env.do(t, http.MethodPut, authPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
}

func TestPartyPolicy_HolderAuthorizesThroughAnotherFactor(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	pwUUID := env.credentialUUID(t, m.acct.tk.access(), "password")
	emailUUID := env.credentialUUID(t, m.acct.tk.access(), "email_otp")
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, authPath(g, pwUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)

	tok := env.withEmailFactor(t, m)
	env.obs.reset()
	env.do(t, http.MethodPut, authPath(g, emailUUID), tok.access(), nil).wantStatus(t, http.StatusOK)
	if n := env.authorizationCount(t, emailUUID, g.id); n != 1 {
		t.Fatalf("email_otp authorizations = %d, want 1", n)
	}
	var by int64
	if err := env.pool.QueryRow(context.Background(), `SELECT a.authorized_by_profile_id FROM mod_users.credential_party_authorizations a
	  JOIN mod_users.credentials c ON c.id = a.credential_id WHERE c.uuid = $1::uuid`, emailUUID).Scan(&by); err != nil || by != m.profileID {
		t.Fatalf("authorized_by_profile_id = %d, %v; want the holder's party profile %d", by, err, m.profileID)
	}
	if got := env.obs.pairs(); !slices.Equal(got, []string{"create credential_party_authorization"}) {
		t.Errorf("observations = %v", got)
	}
	// The holder may also remove it again, and confine the credential.
	env.do(t, http.MethodDelete, authPath(g, emailUUID), tok.access(), nil).wantStatus(t, http.StatusNoContent)
	env.do(t, http.MethodPut, confPath(g, emailUUID), tok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.confinedTo(t, emailUUID); got != g.id {
		t.Errorf("confined to %d, want %d", got, g.id)
	}
	// The holder never reaches the administration reads (a member who cannot
	// read the party sees the masked 404).
	env.do(t, http.MethodGet, g.path("/credential-authorizations"), tok.access(), nil).wantStatus(t, http.StatusNotFound)
}

func TestPartyPolicy_SelfAuthorizationUsingOnlyTheCredentialBeingAuthorizedFails(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	pwUUID := env.credentialUUID(t, m.acct.tk.access(), "password")

	// Under the default `any` policy the member acts as the party profile with
	// the password alone; the password cannot authorize itself.
	tok, _ := env.partySession(t, m)
	if status, p := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK || p.ProfileID != m.profileID {
		t.Fatalf("probe as the party profile = %d %+v", status, p)
	}
	env.do(t, http.MethodPut, authPath(g, pwUUID), tok.access(), nil).wantRefused(t, http.StatusNotFound)
	env.do(t, http.MethodPut, confPath(g, pwUUID), tok.access(), nil).wantRefused(t, http.StatusNotFound)
	if n := env.authorizationCount(t, pwUUID, g.id); n != 0 {
		t.Fatalf("the credential authorized itself (%d rows)", n)
	}
	if got := env.confinedTo(t, pwUUID); got != 0 {
		t.Fatalf("the credential confined itself to %d", got)
	}

	// Authorized by an administrator, it is still the session's only factor, so
	// the holder's own write is refused (the evaluation without it is not
	// available).
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, authPath(g, pwUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, authPath(g, pwUUID), tok.access(), nil).wantRefused(t, http.StatusNotFound)
	env.do(t, http.MethodDelete, authPath(g, pwUUID), tok.access(), nil).wantRefused(t, http.StatusNotFound)
	if n := env.authorizationCount(t, pwUUID, g.id); n != 1 {
		t.Fatalf("authorizations = %d, want the administrator's row untouched", n)
	}
}

func TestPartyPolicy_APersonalOnlySessionCannotAuthorizeForAnAuthorizedPolicy(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	pwUUID := env.credentialUUID(t, m.acct.tk.access(), "password")
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)

	personal := env.login(t, m.acct)
	r := env.do(t, http.MethodPut, authPath(g, pwUUID), personal.access(), nil)
	if r.status != http.StatusForbidden && r.status != http.StatusNotFound {
		t.Fatalf("personal-only authorization = %d, want a refusal; body=%s", r.status, r.raw)
	}
	if n := env.authorizationCount(t, pwUUID, g.id); n != 0 {
		t.Fatalf("a personal-only session authorized a credential (%d rows)", n)
	}
	// Nor can another user's session, whatever it holds.
	other := env.register(t)
	r = env.do(t, http.MethodPut, authPath(g, pwUUID), other.tk.access(), nil)
	if r.status != http.StatusForbidden && r.status != http.StatusNotFound {
		t.Fatalf("another user's authorization = %d, want a refusal; body=%s", r.status, r.raw)
	}
	// The party profile is locked under the policy, so it cannot be acted as.
	env.do(t, http.MethodPost, "/v1/session/switch", personal.access(), map[string]string{"profile_uuid": m.profUUID}).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
}

func TestPartyPolicy_AvailabilityChangesTakeEffectOnTheNextRequest(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	pwUUID := env.credentialUUID(t, m.acct.tk.access(), "password")
	tok, _ := env.partySession(t, m)
	if status, _ := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK {
		t.Fatalf("probe under the default policy = %d", status)
	}

	// `any` to `authorized`: the profile reached only through an unauthorized
	// credential locks on the next request.
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, "/v1/probe", tok.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

	// Authorizing the credential unlocks it again, without a new token.
	env.do(t, http.MethodPut, authPath(g, pwUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if status, _ := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK {
		t.Fatalf("probe after authorizing = %d", status)
	}
	// A higher min_acr locks it again.
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized", "min_acr": 2}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, "/v1/probe", tok.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	env.setPolicy(t, g, map[string]any{"credential_rule": "any"}).wantStatus(t, http.StatusOK)
	if status, _ := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK {
		t.Fatalf("probe after relaxing the policy = %d", status)
	}
}

func TestPartyPolicy_ConfinementSetClearAndConflict(t *testing.T) {
	g := env.newGateParty(t)
	g2 := env.newGateParty(t)
	m := env.newGateMember(t, g)
	// The same user also belongs to the second party.
	env.seedPartyProfile(t, m.userID, g2.id, "active")
	// Party-controlled credentials of the member: an OIDC credential of a
	// provider bound to each party.
	credUUID := env.boundOIDCCredential(t, m.userID, g.id)
	cred2UUID := env.boundOIDCCredential(t, m.userID, g2.id)

	env.obs.reset()
	v := env.do(t, http.MethodPut, confPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if v["credential_uuid"] != credUUID || v["party_uuid"] != g.uuid {
		t.Fatalf("confinement = %v", v)
	}
	if got := env.confinedTo(t, credUUID); got != g.id {
		t.Fatalf("confined to %d, want %d", got, g.id)
	}
	// Idempotent, with a single observation.
	env.do(t, http.MethodPut, confPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.obs.pairs(); !slices.Equal(got, []string{"update credential"}) {
		t.Errorf("observations = %v, want one update credential", got)
	}

	// The second party's administrator reaches neither a credential that party
	// does not control nor the other party's confinement of it.
	env.do(t, http.MethodPut, confPath(g2, credUUID), g2.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	if got := env.confinedTo(t, credUUID); got != g.id {
		t.Fatalf("the other party changed the confinement to %d", got)
	}

	// Confined to another party: both the set and the clear are 409.
	env.exec(t, "confine elsewhere", `UPDATE mod_users.credentials SET confined_to_party_id = $1 WHERE uuid = $2::uuid`, g.id, cred2UUID)
	env.do(t, http.MethodPut, confPath(g2, cred2UUID), g2.adminTok.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.credential_confined")
	env.do(t, http.MethodDelete, confPath(g2, cred2UUID), g2.adminTok.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.credential_confined")
	if got := env.confinedTo(t, cred2UUID); got != g.id {
		t.Fatalf("the other party changed the confinement to %d", got)
	}

	env.obs.reset()
	env.do(t, http.MethodDelete, confPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := env.confinedTo(t, credUUID); got != 0 {
		t.Fatalf("confined to %d after the clear", got)
	}
	if got := env.obs.pairs(); !slices.Equal(got, []string{"update credential"}) {
		t.Errorf("observations = %v, want one update credential", got)
	}
	env.do(t, http.MethodDelete, confPath(g, credUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	// Free again, the second party may confine the credential it controls.
	env.exec(t, "free", `UPDATE mod_users.credentials SET confined_to_party_id = NULL WHERE uuid = $1::uuid`, cred2UUID)
	env.do(t, http.MethodPut, confPath(g2, cred2UUID), g2.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if got := env.confinedTo(t, cred2UUID); got != g2.id {
		t.Fatalf("confined to %d, want %d", got, g2.id)
	}
}

func TestPartyPolicy_GatingRoutesReturn401WithoutAToken(t *testing.T) {
	g := env.newGateParty(t)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, g.path("/authn-policy")},
		{http.MethodPut, g.path("/authn-policy")},
		{http.MethodGet, g.path("/credential-authorizations")},
		{http.MethodPut, authPath(g, "00000000-0000-4000-8000-000000000001")},
		{http.MethodDelete, confPath(g, "00000000-0000-4000-8000-000000000001")},
	} {
		env.do(t, rt.method, rt.path, "", map[string]any{"credential_rule": "any"}).wantStatus(t, http.StatusUnauthorized)
	}
}
