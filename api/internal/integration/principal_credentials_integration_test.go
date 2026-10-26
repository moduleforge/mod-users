//go:build integration

package integration

// Credential administration of other principals (phase users-authorization-engine,
// package 4): the /v1/principals/{uuid}/credentials* routes authorized by the
// `credentials` operation, and the invite-only app gate through
// Authorize(login, app).
//
// Requirement 7 cases and the tests that cover them:
//
//	operator with a wildcard grant lists, issues and revokes credentials
//	  of a service account and installs an SSH key for a user          TestPrincipalCredentials_OperatorWildcard
//	party admin with an owned-scope grant manages a party-owned service
//	  account and is refused for a personal user                       TestPrincipalCredentials_PartyAdminOwnedScope
//	personal-profile owner rotates its self-owned service account's key;
//	  another user is refused                                          TestPrincipalCredentials_OwnerRotation
//	stale auth_time is 409 users.step_up_required (shared action)      TestPrincipalCredentials_StaleAuthTimeNeedsStepUp
//	revocation ends dependent sessions on the next request             TestPrincipalCredentials_RevocationEndsSessions
//	invite-only app: no grant is locked, an actor-group grant and a
//	  wildcard holder are admitted, a public app is unaffected,
//	  and the per-request check follows the grant                      TestApp_InviteOnlyLoginGrants
//	route order with mod-core's AuthContext identities                 TestOrder_PrincipalCredentialsRoutes
//	creates and revokes emit create and revoke on credential           TestPrincipalCredentials_Observations

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp/ordertest"
	"github.com/moduleforge/core-api/opctx"
)

// seedServiceAccountOwnedBy inserts a service account owned by ownerID (a party
// or a user) with its profile, and no credentials. It returns the account's
// entity id and uuid.
func (s *stack) seedServiceAccountOwnedBy(t *testing.T, ownerID int64) (int64, string) {
	t.Helper()
	var id int64
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		ctx := context.Background()
		id = s.entityOf(t, tx, "service_account", &ownerID)
		if _, err := tx.Exec(ctx, `INSERT INTO principals (entity_id) VALUES ($1)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_accounts (entity_id, label) VALUES ($1, $2)`, id, uniq("svc")); err != nil {
			return err
		}
		profileID := s.entityOf(t, tx, "profile", &ownerID)
		_, err := tx.Exec(ctx,
			`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at) VALUES ($1, $2, NULL, 'active', now())`,
			profileID, id)
		return err
	})
	if err != nil {
		t.Fatalf("seed service account: %v", err)
	}
	return id, s.entityUUID(t, id)
}

func credsPath(principalUUID string) string {
	return "/v1/principals/" + principalUUID + "/credentials"
}

// credentialList returns the credentials of a principal as the route lists them.
func (s *stack) credentialList(t *testing.T, token, principalUUID string) []map[string]any {
	t.Helper()
	var out struct {
		Credentials []map[string]any `json:"credentials"`
	}
	s.do(t, http.MethodGet, credsPath(principalUUID), token, nil).wantStatus(t, http.StatusOK).into(t, &out)
	return out.Credentials
}

func credentialOfKind(list []map[string]any, kind string) map[string]any {
	for _, c := range list {
		if c["kind"] == kind {
			return c
		}
	}
	return nil
}

func TestPrincipalCredentials_OperatorWildcard(t *testing.T) {
	op := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, op)
	party := env.seedParty(t, "Cred Org "+uniq("p"))
	_, saUUID := env.seedServiceAccountOwnedBy(t, party)

	if got := env.credentialList(t, opTok.access(), saUUID); len(got) != 0 {
		t.Fatalf("a fresh service account lists %d credentials, want 0", len(got))
	}

	// Issue: the secret is shown once, the list never shows it.
	issued := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", opTok.access(), map[string]string{"label": "ci"}).
		wantStatus(t, http.StatusCreated)
	if cc := issued.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	key := issued.obj(t)
	secret, _ := key["client_secret"].(string)
	clientID, _ := key["client_id"].(string)
	keyUUID, _ := key["uuid"].(string)
	if secret == "" || clientID == "" || keyUUID == "" || key["kind"] != "api_key" || key["label"] != "ci" {
		t.Fatalf("issued key = %v, want kind api_key with uuid, client_id and a one-time client_secret", key)
	}
	listed := env.credentialList(t, opTok.access(), saUUID)
	if len(listed) != 1 || listed[0]["uuid"] != keyUUID || listed[0]["client_id"] != clientID {
		t.Fatalf("list = %v, want the issued key", listed)
	}
	if _, leaked := listed[0]["client_secret"]; leaked {
		t.Errorf("list leaks the client secret: %v", listed[0])
	}
	// The key works for client credentials until revoked.
	cc := map[string]string{"grant_type": "client_credentials", "client_id": clientID, "client_secret": secret}
	env.do(t, http.MethodPost, "/v1/auth/token", "", cc).wantStatus(t, http.StatusOK)

	// Revoke through the admin route; the key stops working and a second
	// revoke is a 404.
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+keyUUID, opTok.access(), nil).wantStatus(t, http.StatusNoContent)
	env.do(t, http.MethodPost, "/v1/auth/token", "", cc).wantStatus(t, http.StatusUnauthorized)
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+keyUUID, opTok.access(), nil).wantStatus(t, http.StatusNotFound)
	var reason string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT revoke_reason FROM mod_users.credentials WHERE uuid = $1::uuid`, keyUUID).Scan(&reason); err != nil || reason != "admin" {
		t.Fatalf("revoke_reason = %q, %v; want admin", reason, err)
	}
	if got := env.credentialList(t, opTok.access(), saUUID); len(got) != 0 {
		t.Errorf("a revoked key is still listed: %v", got)
	}

	// API keys are issued to service accounts only.
	user := env.registerVerified(t)
	userUUID := env.entityUUID(t, env.principalID(t, user.Email))
	env.do(t, http.MethodPost, credsPath(userUUID)+"/api-keys", opTok.access(), map[string]string{"label": "no"}).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.principal_not_service_account")

	// The operator installs an SSH key for a user; the same key cannot be
	// installed twice.
	pub := newSSHPublicKey(t)
	ssh := env.do(t, http.MethodPost, credsPath(userUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": pub, "label": "laptop"}).
		wantStatus(t, http.StatusCreated).obj(t)
	if ssh["kind"] != "ssh_key" || ssh["label"] != "laptop" || ssh["fingerprint"] == "" {
		t.Fatalf("installed ssh key = %v", ssh)
	}
	if credentialOfKind(env.credentialList(t, opTok.access(), userUUID), "ssh_key") == nil {
		t.Errorf("the installed key is not listed")
	}
	env.do(t, http.MethodPost, credsPath(userUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": pub}).
		wantError(t, http.StatusConflict, "conflict", "users.ssh_key_in_use")
	// A service account may hold an SSH key too.
	env.do(t, http.MethodPost, credsPath(saUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated)
	// A malformed key is a 400.
	env.do(t, http.MethodPost, credsPath(userUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": "not a key"}).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.ssh_key_invalid")
}

func TestPrincipalCredentials_PartyAdminOwnedScope(t *testing.T) {
	acme := env.seedParty(t, "Cred Acme "+uniq("p"))
	admin := env.newImpAdmin(t, acme)
	_, saUUID := env.seedServiceAccountOwnedBy(t, acme)
	_, otherSA := env.seedServiceAccountOwnedBy(t, env.seedParty(t, "Cred Other "+uniq("p")))
	personal := env.registerVerified(t)
	personalUUID := env.entityUUID(t, env.principalID(t, personal.Email))

	// The admin's owned-scope grant covers the party's service account.
	key := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", admin.tok.access(), map[string]string{"label": "ci"}).
		wantStatus(t, http.StatusCreated).obj(t)
	if len(env.credentialList(t, admin.tok.access(), saUUID)) != 1 {
		t.Fatalf("the party admin does not list the service account's key")
	}
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+key["uuid"].(string), admin.tok.access(), nil).wantStatus(t, http.StatusNoContent)

	// It does not reach a personal user or another party's service account: the
	// principal is unreadable, so every route masks it as a 404.
	for _, target := range []string{personalUUID, otherSA} {
		env.do(t, http.MethodGet, credsPath(target), admin.tok.access(), nil).wantStatus(t, http.StatusNotFound)
		env.do(t, http.MethodPost, credsPath(target)+"/ssh-keys", admin.tok.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
			wantStatus(t, http.StatusNotFound)
	}
	if n := env.count(t, "ssh keys of the personal user", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'ssh_key'`,
		env.principalID(t, personal.Email)); n != 0 {
		t.Errorf("a refused install created %d credentials", n)
	}
}

func TestPrincipalCredentials_OwnerRotation(t *testing.T) {
	owner := env.registerVerified(t)
	ownerID := env.principalID(t, owner.Email)
	_, saUUID := env.seedServiceAccountOwnedBy(t, ownerID) // a self-owned service account
	other := env.registerVerified(t)

	// The personal profile owns the account: the own arm confers `credentials`
	// (and `read`), with no manage or impersonate grant anywhere.
	first := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", owner.tk.access(), map[string]string{"label": "v1"}).
		wantStatus(t, http.StatusCreated).obj(t)
	second := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", owner.tk.access(), map[string]string{"label": "v2"}).
		wantStatus(t, http.StatusCreated).obj(t)
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+first["uuid"].(string), owner.tk.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := env.credentialList(t, owner.tk.access(), saUUID); len(got) != 1 || got[0]["uuid"] != second["uuid"] {
		t.Fatalf("after rotation the owner lists %v, want only the second key", got)
	}

	// Another user cannot read the account, so it gets the masked 404.
	env.do(t, http.MethodGet, credsPath(saUUID), other.tk.access(), nil).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", other.tk.access(), map[string]string{"label": "x"}).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+second["uuid"].(string), other.tk.access(), nil).wantStatus(t, http.StatusNotFound)

	// The owner's own-arm authority is credentials only, and a reader of the
	// account who lacks it is a 403 (read without credentials).
	reader := env.registerVerified(t)
	readerProfile := env.personalProfileID(t, env.principalID(t, reader.Email))
	env.exec(t, "read grant on the service account",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
		 SELECT $1, o.id, e.id, NULL FROM authz_operations o, entities e WHERE o.slug = 'read' AND e.uuid = $2::uuid`,
		readerProfile, saUUID)
	env.do(t, http.MethodGet, credsPath(saUUID), reader.tk.access(), nil).wantStatus(t, http.StatusForbidden)
}

func TestPrincipalCredentials_StaleAuthTimeNeedsStepUp(t *testing.T) {
	op := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, op)
	_, saUUID := env.seedServiceAccountOwnedBy(t, env.seedParty(t, "Cred Stale "+uniq("p")))
	key := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", opTok.access(), map[string]string{"label": "ci"}).
		wantStatus(t, http.StatusCreated).obj(t)

	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '11 minutes' WHERE uuid = $1::uuid`, op.tk.Session.UUID)
	pub := newSSHPublicKey(t)
	env.do(t, http.MethodGet, credsPath(saUUID), opTok.access(), nil).wantAction(t, http.StatusConflict, "users.step_up_required")
	env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", opTok.access(), map[string]string{"label": "x"}).
		wantAction(t, http.StatusConflict, "users.step_up_required")
	env.do(t, http.MethodPost, credsPath(saUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": pub}).
		wantAction(t, http.StatusConflict, "users.step_up_required")
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+key["uuid"].(string), opTok.access(), nil).
		wantAction(t, http.StatusConflict, "users.step_up_required")
	if n := env.count(t, "active credentials", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND revoked_at IS NULL`,
		env.principalID2(t, saUUID)); n != 1 {
		t.Fatalf("a stale call changed the credentials: %d active, want 1", n)
	}

	// A step-up refreshes auth_time and the routes work again.
	env.do(t, http.MethodPost, "/v1/session/step-up/password", opTok.access(), map[string]string{"password": op.Password}).
		wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, credsPath(saUUID), opTok.access(), nil).wantStatus(t, http.StatusOK)
}

// principalID2 resolves a principal entity id from its uuid.
func (s *stack) principalID2(t *testing.T, principalUUID string) int64 {
	t.Helper()
	return s.scanInt64(t, "principal id", `SELECT id FROM entities WHERE uuid = $1::uuid`, principalUUID)
}

func TestPrincipalCredentials_RevocationEndsSessions(t *testing.T) {
	op := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, op)
	victim := env.registerVerified(t)
	victimUUID := env.entityUUID(t, env.principalID(t, victim.Email))

	if status, _ := env.probe(t, "/v1/probe", victim.tk.access()); status != http.StatusOK {
		t.Fatalf("the victim's session = %d, want 200", status)
	}
	pw := credentialOfKind(env.credentialList(t, opTok.access(), victimUUID), "password")
	if pw == nil {
		t.Fatalf("the victim has no listed password credential")
	}
	env.do(t, http.MethodDelete, credsPath(victimUUID)+"/"+pw["uuid"].(string), opTok.access(), nil).wantStatus(t, http.StatusNoContent)

	// The session held the revoked password as a factor: it ends with the next
	// request.
	status, _ := env.probe(t, "/v1/probe", victim.tk.access())
	if status != http.StatusUnauthorized {
		t.Fatalf("the victim's session after revoke = %d, want 401", status)
	}
	env.do(t, http.MethodGet, "/v1/probe", victim.tk.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestPrincipalCredentials_Observations(t *testing.T) {
	op := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, op)
	_, saUUID := env.seedServiceAccountOwnedBy(t, env.seedParty(t, "Cred Audit "+uniq("p")))

	env.obs.reset()
	key := env.do(t, http.MethodPost, credsPath(saUUID)+"/api-keys", opTok.access(), map[string]string{"label": "ci"}).
		wantStatus(t, http.StatusCreated).obj(t)
	env.checkObservations(t, "issue api key", "create credential")

	env.obs.reset()
	env.do(t, http.MethodPost, credsPath(saUUID)+"/ssh-keys", opTok.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated)
	env.checkObservations(t, "install ssh key", "create credential")

	env.obs.reset()
	env.do(t, http.MethodDelete, credsPath(saUUID)+"/"+key["uuid"].(string), opTok.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "revoke", "revoke credential")
	saID := env.principalID2(t, saUUID)
	for _, o := range env.obs.all() {
		if o.Target != saID {
			t.Errorf("%s %s targets %d, want the service account %d", o.Op, o.Resource, o.Target, saID)
		}
	}
}

func TestApp_InviteOnlyLoginGrants(t *testing.T) {
	slug := uniq("grant-app-")
	appID := env.seedApp(t, slug, true)
	publicSlug := uniq("public-app-")
	env.seedApp(t, publicSlug, false)

	loginOp := env.opID(t, "login")
	outsider := env.register(t)
	env.loginRaw(t, outsider.Email, outsider.Password, map[string]string{"app": slug}).
		wantError(t, http.StatusForbidden, "users.app_login_not_granted", "")
	// A non-invite-only app is unaffected.
	env.loginRaw(t, outsider.Email, outsider.Password, map[string]string{"app": publicSlug}).wantStatus(t, http.StatusOK)

	// A login grant to an actor group admits its member's profile.
	member := env.register(t)
	memberProfile := env.personalProfileID(t, env.principalID(t, member.Email))
	owner := env.seedParty(t, "App Grant Org "+uniq("p"))
	groupID := env.scanInt64(t, "group entity",
		`INSERT INTO entities (fundamental_type_id, owner_id) SELECT id, $1 FROM types WHERE slug = 'authz_actor_group' RETURNING id`, owner)
	env.exec(t, "group row", `INSERT INTO authz_actor_groups (entity_id, name) VALUES ($1, $2)`, groupID, uniq("login-grp"))
	env.exec(t, "group member", `INSERT INTO authz_actor_group_members (group_id, member_id) VALUES ($1, $2)`, groupID, memberProfile)
	env.loginRaw(t, member.Email, member.Password, map[string]string{"app": slug}).
		wantError(t, http.StatusForbidden, "users.app_login_not_granted", "")
	env.exec(t, "login grant to the group on the app",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`, groupID, loginOp, appID)
	tk := env.loginRaw(t, member.Email, member.Password, map[string]string{"app": slug}).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || !tk.Profile.Available {
		t.Fatalf("the granted profile is not landed on: %+v", tk.Profile)
	}
	// The per-request check follows the grant: the token works while it holds
	// and the profile is unavailable the request after it is removed.
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Fatalf("a granted profile's request = %d, want 200", status)
	}
	env.exec(t, "remove the login grant", `DELETE FROM grants WHERE actor_id = $1 AND operation_id = $2 AND target_id = $3`, groupID, loginOp, appID)
	env.do(t, http.MethodGet, "/v1/probe", tk.access(), nil).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "app_login_not_granted")

	// A wildcard holder is admitted without a login grant on the app.
	op := env.register(t)
	_, opProfileID := env.makeOperator(t, op)
	opTk := env.loginRaw(t, op.Email, op.Password, map[string]string{"app": slug}).wantStatus(t, http.StatusOK).tok(t)
	if opTk.Profile == nil || opTk.Profile.UUID != env.profileUUID(t, opProfileID) {
		t.Fatalf("the wildcard holder landed on %+v", opTk.Profile)
	}
	if status, _ := env.probe(t, "/v1/probe", opTk.access()); status != http.StatusOK {
		t.Fatalf("a wildcard holder's request = %d, want 200", status)
	}
}

func TestOrder_PrincipalCredentialsRoutes(t *testing.T) {
	h := env.orderRouter()
	admin := env.registerVerified(t)
	_, opProfile := env.makeOperator(t, admin)
	operator := env.asSession(t, admin, opProfile)

	user := env.registerVerified(t)
	ordinary := env.asSession(t, user, env.personalProfileID(t, env.principalID(t, user.Email)))

	party := env.seedParty(t, "Order Cred Org "+uniq("p"))
	_, saUUID := env.seedServiceAccountOwnedBy(t, party)
	missing := "/v1/principals/22222222-2222-2222-2222-222222222222/credentials"
	existing := credsPath(saUUID)

	// A credential of the account to revoke through the authorized call.
	opTok, _ := env.makeOperator(t, env.registerVerified(t))
	revokable := env.do(t, http.MethodPost, existing+"/api-keys", opTok.access(), map[string]string{"label": "order"}).
		wantStatus(t, http.StatusCreated).obj(t)["uuid"].(string)
	const missingCred = "/44444444-4444-4444-4444-444444444444"

	sshBody := func() []byte { return []byte(`{"public_key":"` + newSSHPublicKey(t) + `"}`) }
	ordertest.Run(t,
		ordertest.Route{
			Name: "GET /v1/principals/{uuid}/credentials", Handler: h, Method: http.MethodGet,
			ExistingPath: existing, MissingPath: missing,
			Authorized: operator,
		},
		ordertest.Route{
			Name: "POST /v1/principals/{uuid}/credentials/api-keys", Handler: h, Method: http.MethodPost,
			ExistingPath: existing + "/api-keys", MissingPath: missing + "/api-keys",
			GoodBody: []byte(`{"label":"order"}`), BadBody: []byte(`{"label":`),
			Authorized: operator,
		},
		ordertest.Route{
			Name: "POST /v1/principals/{uuid}/credentials/ssh-keys", Handler: h, Method: http.MethodPost,
			ExistingPath: existing + "/ssh-keys", MissingPath: missing + "/ssh-keys",
			GoodBody: sshBody(), BadBody: []byte(`{"public_key":`),
			Authorized: operator,
		},
		ordertest.Route{
			Name: "DELETE /v1/principals/{uuid}/credentials/{credential_uuid}", Handler: h, Method: http.MethodDelete,
			ExistingPath: existing + "/" + revokable, MissingPath: missing + missingCred,
			Authorized: operator,
		},
	)

	// 400 precedes the masked 404 for a caller who may not read the principal,
	// and an unreadable principal is the same 404 as a missing one.
	for _, c := range []struct {
		name, method, suffix, body string
	}{
		{"list", http.MethodGet, "", ""},
		{"issue", http.MethodPost, "/api-keys", `{"label":"x"}`},
		{"install", http.MethodPost, "/ssh-keys", `{"public_key":"not a key"}`},
		{"revoke", http.MethodDelete, "/55555555-5555-5555-5555-555555555555", ""},
	} {
		t.Run("unauthorized "+c.name+" is masked", func(t *testing.T) {
			var body []byte
			if c.body != "" {
				body = []byte(c.body)
			}
			ex := doAs(t, h, ordinary, c.method, existing+c.suffix, body)
			mi := doAs(t, h, ordinary, c.method, missing+c.suffix, body)
			want := http.StatusNotFound
			if c.name == "install" {
				want = http.StatusBadRequest // a malformed key is stateless: 400 before authorization
			}
			if ex.status != want || mi.status != want {
				t.Fatalf("existing = %d, missing = %d, want %d for both; %s | %s", ex.status, mi.status, want, ex.raw, mi.raw)
			}
			if string(ex.raw) != string(mi.raw) {
				t.Errorf("a refused call on an existing principal is distinguishable from a missing one:\n%s\n%s", ex.raw, mi.raw)
			}
		})
	}
	t.Run("malformed uuids are 400 for an authenticated caller and 401 for none", func(t *testing.T) {
		for _, p := range []string{"/v1/principals/not-a-uuid/credentials", existing + "/not-a-uuid"} {
			method := http.MethodGet
			if p == existing+"/not-a-uuid" {
				method = http.MethodDelete
			}
			if r := doAs(t, h, ordinary, method, p, nil); r.status != http.StatusBadRequest {
				t.Errorf("%s %s authenticated: %d, want 400", method, p, r.status)
			}
			if r := doAs(t, h, ordertest.Anonymous, method, p, nil); r.status != http.StatusUnauthorized {
				t.Errorf("%s %s anonymous: %d, want 401", method, p, r.status)
			}
		}
	})
	t.Run("a readable principal without the credentials operation is 403", func(t *testing.T) {
		reader := env.registerVerified(t)
		readerProfile := env.personalProfileID(t, env.principalID(t, reader.Email))
		env.exec(t, "read grant", `INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
			SELECT $1, o.id, e.id, NULL FROM authz_operations o, entities e WHERE o.slug = 'read' AND e.uuid = $2::uuid`, readerProfile, saUUID)
		as := env.asSession(t, reader, readerProfile)
		if r := doAs(t, h, as, http.MethodGet, existing, nil); r.status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", r.status, r.raw)
		}
	})
	t.Run("a stale authentication is the shared action-required error", func(t *testing.T) {
		stale := env.asSessionAged(t, admin, opProfile, 11*time.Minute)
		r := doAs(t, h, stale, http.MethodGet, existing, nil)
		r.wantAction(t, http.StatusConflict, "users.step_up_required")
	})
}

// asSessionAged is asSession with an auth_time that is age old.
func (s *stack) asSessionAged(t *testing.T, a account, profileID int64, age time.Duration) ordertest.Identity {
	t.Helper()
	base := s.asSession(t, a, profileID)
	return func(ctx context.Context) context.Context {
		ctx = base(ctx)
		ac, _ := opctx.Auth(ctx)
		ac.AuthTime = time.Now().Add(-age)
		return opctx.WithAuthContext(ctx, ac)
	}
}
