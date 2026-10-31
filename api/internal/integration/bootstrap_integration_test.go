//go:build integration

package integration

// The first-run bootstrap (decisions U9 and U10) against a database of its
// own: the operator party is set once for the life of a database, so every
// test here runs on a fresh copy of the migrated schema (CREATE DATABASE ...
// TEMPLATE), never on the suite's shared database whose operator party the
// fixtures claim.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const bootTemplateDB = "users_integ_boot_tpl"

var (
	bootTplOnce sync.Once
	bootTplErr  error
	bootDBSeq   struct {
		sync.Mutex
		n int
	}
)

// adminConn connects to the server's maintenance database.
func adminConn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, fmt.Sprintf("postgres://users:users@%s:%s/postgres?sslmode=disable", resolveHost(), resolvePort()))
}

// buildBootTemplate creates the migrated template database once per process.
func buildBootTemplate() error {
	ctx := context.Background()
	conn, err := adminConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + bootTemplateDB, "CREATE DATABASE " + bootTemplateDB} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	dsn := fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", resolveHost(), resolvePort(), bootTemplateDB)
	out, err := exec.Command("goose", "-dir", migrationsDir(), "postgres", dsn, "up").CombinedOutput() //nolint:gosec // fixed args, resolved paths
	if err != nil {
		return fmt.Errorf("goose up: %w\n%s", err, out)
	}
	return nil
}

// freshStack composes the module over a new database copied from the
// migrated template, with no operator party, and drops it after the test.
func freshStack(t *testing.T) *stack {
	t.Helper()
	bootTplOnce.Do(func() { bootTplErr = buildBootTemplate() })
	if bootTplErr != nil {
		t.Fatalf("bootstrap template: %v", bootTplErr)
	}
	bootDBSeq.Lock()
	bootDBSeq.n++
	name := fmt.Sprintf("users_integ_boot_%d", bootDBSeq.n)
	bootDBSeq.Unlock()

	ctx := context.Background()
	conn, err := adminConn(ctx)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + name, "CREATE DATABASE " + name + " TEMPLATE " + bootTemplateDB} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = conn.Close(ctx)

	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", resolveHost(), resolvePort(), name))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	s, err := newStack(ctx, pool, true)
	if err != nil {
		pool.Close()
		t.Fatalf("compose stack: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := adminConn(context.Background())
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	return s
}

// mintSetupToken runs the boot-time token mint and returns the plaintext.
func (s *stack) mintSetupToken(t *testing.T) string {
	t.Helper()
	plain, err := s.onboarding.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	if plain == "" {
		t.Fatalf("no setup token minted")
	}
	return plain
}

// bootstrapBody is a POST /v1/bootstrap body.
func bootstrapBody(email string, operator map[string]string) map[string]any {
	return map[string]any{
		"operator": operator,
		"admin": map[string]string{
			"email": email, "password": testPassword, "given_name": "Ada", "family_name": "Admin",
		},
	}
}

func orgOperator() map[string]string {
	return map[string]string{"kind": "organization", "legal_name": "Operator Inc", "legal_form": "corporation", "jurisdiction": "US-DE"}
}

// postBootstrap issues POST /v1/bootstrap with the setup token header.
func (s *stack) postBootstrap(t *testing.T, token string, body any) *resp {
	t.Helper()
	return s.doWithHeaders(t, http.MethodPost, "/v1/bootstrap", body, map[string]string{"X-Setup-Token": token})
}

// doWithHeaders is do with extra request headers and no bearer token.
func (s *stack) doWithHeaders(t *testing.T, method, path string, body any, headers map[string]string) *resp {
	t.Helper()
	req := jsonRequest(t, method, path, body)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	rec := newRecorder()
	s.handler.ServeHTTP(rec, req)
	return &resp{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
}

// bootstrapStatus reads GET /v1/bootstrap/status.
func (s *stack) bootstrapStatus(t *testing.T) (bootstrapped, oidcConfirmed bool) {
	t.Helper()
	var st struct {
		Bootstrapped  bool `json:"bootstrapped"`
		OIDCConfirmed bool `json:"oidc_confirmed"`
	}
	s.do(t, http.MethodGet, "/v1/bootstrap/status", "", nil).wantStatus(t, http.StatusOK).into(t, &st)
	return st.Bootstrapped, st.OIDCConfirmed
}

// tokenHashSet reports whether the setup-token hash is stored.
func (s *stack) tokenHashSet(t *testing.T) bool {
	t.Helper()
	return s.count(t, "setup token hash", `SELECT count(*) FROM mod_users.oidc_config WHERE setup_token_hash IS NOT NULL`) == 1
}

// confirmOptOut confirms the OIDC configuration (opt-out) with the setup token
// in the header and returns the response.
func (s *stack) confirmOptOut(t *testing.T, setupToken, bearer string) *resp {
	t.Helper()
	headers := map[string]string{"X-Setup-Token": setupToken}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}
	return s.doWithHeaders(t, http.MethodPost, "/v1/oidc-config/confirm", map[string]any{"opt_out": true}, headers)
}

// bootRows is the state requirement 1 of the bootstrap writes.
type bootRows struct {
	userID, personID, personalProfileID int64
	partyID, adminProfileID             int64
	passwordID                          int64
}

// assertBootstrapRows checks every row the bootstrap must have written for
// the admin email, with partyKind organization or natural_person.
func (s *stack) assertBootstrapRows(t *testing.T, email, partyKind string, tk tokens) bootRows {
	t.Helper()
	ctx := context.Background()
	var b bootRows
	b.userID = s.principalID(t, email)

	// The user: bring-your-own, own person record owned by the user, the
	// personal profile.
	var personID *int64
	var isGuest bool
	var managing *int64
	if err := s.pool.QueryRow(ctx, `SELECT person_id, is_guest, managing_party_id FROM mod_users.users WHERE entity_id = $1`, b.userID).
		Scan(&personID, &isGuest, &managing); err != nil {
		t.Fatalf("users row: %v", err)
	}
	if personID == nil || isGuest || managing != nil {
		t.Fatalf("user: person_id=%v is_guest=%v managing=%v, want a bring-your-own user with a person", personID, isGuest, managing)
	}
	b.personID = *personID
	if owner := s.scanInt64(t, "person owner", `SELECT owner_id FROM entities WHERE id = $1`, b.personID); owner != b.userID {
		t.Errorf("person owner = %d, want the user %d", owner, b.userID)
	}
	b.personalProfileID = s.personalProfileID(t, b.userID)

	// The verified primary email, the password bound to it, the email OTP.
	if n := s.count(t, "identifier", `SELECT count(*) FROM mod_users.user_identifiers
		WHERE user_id = $1 AND kind = 'email' AND value_canon = $2 AND is_primary AND verified_at IS NOT NULL`, b.userID, email); n != 1 {
		t.Errorf("verified primary email identifiers = %d, want 1", n)
	}
	b.passwordID = s.scanInt64(t, "password", `SELECT c.id FROM mod_users.credentials c
		JOIN mod_users.cred_password p ON p.credential_id = c.id
		JOIN mod_users.user_identifiers i ON i.id = p.identifier_id
		WHERE c.principal_id = $1 AND c.kind = 'password' AND c.revoked_at IS NULL AND i.value_canon = $2`, b.userID, email)
	if n := s.count(t, "email otp", `SELECT count(*) FROM mod_users.credentials
		WHERE principal_id = $1 AND kind = 'email_otp' AND revoked_at IS NULL`, b.userID); n != 1 {
		t.Errorf("email_otp credentials = %d, want 1", n)
	}

	// The operator party, claimed.
	b.partyID = s.scanInt64(t, "operator party", `SELECT operator_party_id FROM platform_settings WHERE id = 1`)
	switch partyKind {
	case "organization":
		var name, form string
		var jurisdiction *string
		var owner *int64
		if err := s.pool.QueryRow(ctx, `SELECT o.legal_name, o.legal_form, o.jurisdiction, e.owner_id
			FROM organizations o JOIN entities e ON e.id = o.entity_id WHERE o.entity_id = $1`, b.partyID).
			Scan(&name, &form, &jurisdiction, &owner); err != nil {
			t.Fatalf("operator organization: %v", err)
		}
		if name != "Operator Inc" || form != "corporation" || jurisdiction == nil || *jurisdiction != "US-DE" || owner != nil {
			t.Errorf("organization = %q %q %v owner %v, want Operator Inc corporation US-DE with no owner", name, form, jurisdiction, owner)
		}
	case "natural_person":
		if b.partyID != b.personID {
			t.Errorf("operator party = %d, want the user's person %d", b.partyID, b.personID)
		}
	}

	// The admin profile: active, manual, the user in the operator party.
	var status, source string
	if err := s.pool.QueryRow(ctx, `SELECT entity_id, status, source FROM profiles WHERE holder_id = $1 AND party_id = $2`,
		b.userID, b.partyID).Scan(&b.adminProfileID, &status, &source); err != nil {
		t.Fatalf("admin profile: %v", err)
	}
	if status != "active" || source != "manual" {
		t.Errorf("admin profile = %s/%s, want active/manual", status, source)
	}

	// The wildcard manage grant held by the admin profile.
	if n := s.count(t, "wildcard grant", `SELECT count(*) FROM grants g JOIN authz_operations o ON o.id = g.operation_id
		WHERE g.actor_id = $1 AND o.slug = 'manage' AND g.target_id IS NULL AND g.scope = 'entity'`, b.adminProfileID); n != 1 {
		t.Errorf("wildcard manage grants of the admin profile = %d, want 1", n)
	}

	// The operator party's gate, the password's authorization and landing.
	if n := s.count(t, "policy", `SELECT count(*) FROM mod_users.party_authn_policies
		WHERE party_id = $1 AND credential_rule = 'authorized'`, b.partyID); n != 1 {
		t.Errorf("authorized-rule policies of the operator party = %d, want 1", n)
	}
	if n := s.count(t, "authorization", `SELECT count(*) FROM mod_users.credential_party_authorizations
		WHERE credential_id = $1 AND party_id = $2 AND authorized_by_profile_id = $3`, b.passwordID, b.partyID, b.adminProfileID); n != 1 {
		t.Errorf("password authorizations for the operator party = %d, want 1", n)
	}
	if def := s.scanInt64(t, "default profile", `SELECT COALESCE(default_profile_id, 0) FROM mod_users.credentials WHERE id = $1`, b.passwordID); def != b.adminProfileID {
		t.Errorf("password default_profile_id = %d, want the admin profile %d", def, b.adminProfileID)
	}

	// The response: a password session landing on the admin profile.
	if tk.Profile == nil || tk.Profile.UUID != s.entityUUID(t, b.adminProfileID) || tk.Profile.Kind != "party" {
		t.Errorf("landed on %+v, want the admin profile", tk.Profile)
	}
	if tk.Session == nil || len(tk.Session.AMR) != 1 || tk.Session.AMR[0] != "pwd" || tk.RefreshToken == nil {
		t.Errorf("session = %+v refresh=%v, want a password session with a refresh token", tk.Session, tk.RefreshToken != nil)
	}
	return b
}

func TestBootstrap_Organization(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	if b, c := s.bootstrapStatus(t); b || c {
		t.Fatalf("status before = bootstrapped %v, oidc_confirmed %v; want false, false", b, c)
	}

	s.obs.reset()
	email := "admin@operator.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, orgOperator())).wantStatus(t, http.StatusCreated).tok(t)
	rows := s.assertBootstrapRows(t, email, "organization", tk)

	// Observations: only section 10.1 ops; the user's writes as the personal
	// profile, the grant and the bootstrap event as the admin profile, the
	// bootstrap event with the new session and targeting the operator party.
	s.checkObservations(t, "bootstrap",
		"create user", "create natural_person", "create user_identifier", "create credential",
		"create organization", "create profile", "grant grant", "login session", "create bootstrap")
	for _, o := range s.obs.all() {
		switch o.Op + " " + o.Resource {
		case "create organization":
			// mod-core's OrganizationService.CreateInTx emits this in the
			// bootstrap transaction, as the first user's personal profile
			// (mod-audit needs a real actor), targeting the new organization.
			if o.Target != rows.partyID || o.Auth.ProfileID != rows.personalProfileID {
				t.Errorf("create organization observed as profile %d target %d, want the personal profile %d and the operator party %d",
					o.Auth.ProfileID, o.Target, rows.personalProfileID, rows.partyID)
			}
		case "create bootstrap":
			if o.Target != rows.partyID || o.Auth.ProfileID != rows.adminProfileID || o.Auth.PrincipalID != rows.userID || o.Auth.SessionID == 0 {
				t.Errorf("bootstrap observed as %+v target %d, want the admin profile, the user, the session and the operator party", o.Auth, o.Target)
			}
		case "grant grant":
			if o.Target != 0 || o.Auth.ProfileID != rows.adminProfileID {
				t.Errorf("grant observed as %+v target %d, want the admin profile and no target", o.Auth, o.Target)
			}
		case "login session":
			if o.Auth.ProfileID != rows.adminProfileID || o.Auth.SessionID == 0 {
				t.Errorf("login observed as %+v, want the admin profile and the session", o.Auth)
			}
		case "create user", "create credential", "create user_identifier", "create profile":
			if o.Auth.ProfileID != rows.personalProfileID {
				t.Errorf("%s %s observed as profile %d, want the personal profile %d", o.Op, o.Resource, o.Auth.ProfileID, rows.personalProfileID)
			}
		}
	}

	// The organization row's owner NULL and stored fields are asserted by
	// assertBootstrapRows; normalization of padded input is
	// TestBootstrap_OrganizationNormalized. With mod-audit linked, its real audit_log accepted the in-transaction
	// `create organization` row, written as the first user's personal profile.
	if s.auditApplied {
		if n := s.count(t, "organization audit rows", `SELECT count(*) FROM audit_log
			WHERE op = 'create' AND resource = 'organization' AND target_entity_id = $1 AND actor_entity_id = $2`,
			rows.partyID, rows.personalProfileID); n != 1 {
			t.Errorf("audit_log create organization rows for the operator party = %d, want 1", n)
		}
	}

	// OIDC is not confirmed yet, so the token stays for it.
	if b, c := s.bootstrapStatus(t); !b || c {
		t.Errorf("status after = bootstrapped %v, oidc_confirmed %v; want true, false", b, c)
	}
	if !s.tokenHashSet(t) {
		t.Errorf("setup token cleared although OIDC is unconfirmed")
	}

	// A second bootstrap is refused and writes nothing.
	users := s.count(t, "users", `SELECT count(*) FROM mod_users.users`)
	s.postBootstrap(t, token, bootstrapBody("second@operator.test", orgOperator())).
		wantError(t, http.StatusConflict, "conflict", "users.already_bootstrapped")
	if got := s.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
		t.Errorf("second bootstrap left users: %d -> %d", users, got)
	}
}

// TestBootstrap_OrganizationNormalized drives the organization operator with
// padded fields and checks the stored row is the normalized one with owner
// NULL. It proves the organization comes from mod-core's CreateInTx, which
// trims legal_name, legal_form and jurisdiction itself.
func TestBootstrap_OrganizationNormalized(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	s.postBootstrap(t, token, bootstrapBody("norm@operator.test", map[string]string{
		"kind": "organization", "legal_name": "  Padded Inc  ", "legal_form": " llc ", "jurisdiction": "  US-NY ",
	})).wantStatus(t, http.StatusCreated)

	var name, form string
	var jurisdiction *string
	var owner *int64
	if err := s.pool.QueryRow(context.Background(), `SELECT o.legal_name, o.legal_form, o.jurisdiction, e.owner_id
		FROM organizations o JOIN entities e ON e.id = o.entity_id
		WHERE o.entity_id = (SELECT operator_party_id FROM platform_settings WHERE id = 1)`).
		Scan(&name, &form, &jurisdiction, &owner); err != nil {
		t.Fatalf("operator organization: %v", err)
	}
	if name != "Padded Inc" || form != "llc" || jurisdiction == nil || *jurisdiction != "US-NY" || owner != nil {
		t.Errorf("organization = %q %q %v owner %v, want Padded Inc llc US-NY with no owner", name, form, jurisdiction, owner)
	}
}

func TestBootstrap_NaturalPersonOperator(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	email := "solo@operator.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, map[string]string{"kind": "natural_person"})).
		wantStatus(t, http.StatusCreated).tok(t)
	s.assertBootstrapRows(t, email, "natural_person", tk)
}

func TestBootstrap_ValidationAndErrorOrder(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)

	// The header token is evaluated before the body.
	for _, bad := range []string{"", "not-the-token"} {
		s.postBootstrap(t, bad, "{not json").wantError(t, http.StatusUnauthorized, "users.setup_token_invalid", "")
		s.postBootstrap(t, bad, bootstrapBody("a@operator.test", orgOperator())).
			wantError(t, http.StatusUnauthorized, "users.setup_token_invalid", "")
	}
	// Then the body.
	s.postBootstrap(t, token, "{not json").wantStatus(t, http.StatusBadRequest)
	s.postBootstrap(t, token, bootstrapBody("a@operator.test", map[string]string{"kind": "organization", "legal_name": "X", "legal_form": "guild"})).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.legal_form_invalid")
	s.postBootstrap(t, token, bootstrapBody("a@operator.test", map[string]string{"kind": "cooperative"})).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.operator_kind_invalid")
	if b, _ := s.bootstrapStatus(t); b {
		t.Errorf("a refused bootstrap set the operator party")
	}
}

// TestBootstrap_Concurrent runs two bootstraps with the valid token at once:
// exactly one succeeds, the other gets 409, and the loser leaves no user,
// party, profile or grant behind. Run with -count=5 to shake out flakes.
func TestBootstrap_Concurrent(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)

	counts := func() [5]int64 {
		return [5]int64{
			s.count(t, "users", `SELECT count(*) FROM mod_users.users`),
			s.count(t, "organizations", `SELECT count(*) FROM organizations`),
			s.count(t, "persons", `SELECT count(*) FROM natural_persons`),
			s.count(t, "profiles", `SELECT count(*) FROM profiles`),
			s.count(t, "grants", `SELECT count(*) FROM grants`),
		}
	}
	before := counts()

	var wg sync.WaitGroup
	results := make([]*resp, 2)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := jsonRequest(t, http.MethodPost, "/v1/bootstrap",
				bootstrapBody(fmt.Sprintf("racer%d@operator.test", i), orgOperator()))
			req.Header.Set("X-Setup-Token", token)
			rec := newRecorder()
			s.handler.ServeHTTP(rec, req)
			results[i] = &resp{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
		}(i)
	}
	close(start)
	wg.Wait()

	var won, lost int
	for _, r := range results {
		switch r.status {
		case http.StatusCreated:
			won++
		case http.StatusConflict:
			if _, detail := r.errCode(t); detail != "users.already_bootstrapped" {
				t.Errorf("loser detail = %q, want users.already_bootstrapped; body=%s", detail, r.raw)
			}
			lost++
		default:
			t.Errorf("unexpected status %d: %s", r.status, r.raw)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("won %d lost %d, want exactly one of each", won, lost)
	}

	// The winner wrote one user (with its person), one organization, a
	// personal and an admin profile, and one grant; the loser nothing.
	after := counts()
	want := [5]int64{before[0] + 1, before[1] + 1, before[2] + 1, before[3] + 2, before[4] + 1}
	if after != want {
		t.Errorf("rows (users, organizations, persons, profiles, grants) = %v, want %v (before %v)", after, want, before)
	}
}

// TestBootstrap_TokenLifecycle covers decision U9: one token for both steps,
// cleared only when both are done, whichever comes first.
func TestBootstrap_TokenLifecycle(t *testing.T) {
	t.Run("oidc confirmed first keeps the token for the bootstrap", func(t *testing.T) {
		s := freshStack(t)
		token := s.mintSetupToken(t)
		s.confirmOptOut(t, token, "").wantStatus(t, http.StatusOK)
		if !s.onboarding.CurrentState().Confirmed() {
			t.Fatalf("OIDC not confirmed after confirm")
		}
		if !s.tokenHashSet(t) {
			t.Fatalf("confirm cleared the token while the bootstrap is pending")
		}
		// The token still mints at boot while the bootstrap is pending, even
		// with OIDC confirmed (opt-out): a restart rotates it.
		if b, c := s.bootstrapStatus(t); b || !c {
			t.Errorf("status = %v, %v; want false, true", b, c)
		}
		// Once OIDC is confirmed the OIDC configuration routes refuse the
		// still-live setup token (no operator admin session exists yet), while
		// POST /v1/bootstrap below still accepts it.
		s.confirmOptOut(t, token, "").wantStatus(t, http.StatusUnauthorized)
		if !s.tokenHashSet(t) {
			t.Fatalf("a refused confirm cleared the token")
		}
		tk := s.postBootstrap(t, token, bootstrapBody("first@operator.test", orgOperator())).wantStatus(t, http.StatusCreated).tok(t)
		if s.tokenHashSet(t) {
			t.Errorf("token not cleared after both steps")
		}
		// The cleared token is refused everywhere; the admin session works.
		s.confirmOptOut(t, token, "").wantStatus(t, http.StatusUnauthorized)
		s.postBootstrap(t, token, bootstrapBody("again@operator.test", orgOperator())).
			wantError(t, http.StatusUnauthorized, "users.setup_token_invalid", "")
		s.confirmOptOut(t, "", tk.access()).wantStatus(t, http.StatusOK)
		if plain, err := s.onboarding.EnsureSetupToken(context.Background()); err != nil || plain != "" {
			t.Errorf("EnsureSetupToken after both steps = %q, %v; want no token", plain, err)
		}
	})

	t.Run("bootstrap first keeps the token for the oidc confirm", func(t *testing.T) {
		s := freshStack(t)
		token := s.mintSetupToken(t)
		s.postBootstrap(t, token, bootstrapBody("first@operator.test", orgOperator())).wantStatus(t, http.StatusCreated)
		if !s.tokenHashSet(t) {
			t.Fatalf("bootstrap cleared the token while OIDC is unconfirmed")
		}
		s.confirmOptOut(t, token, "").wantStatus(t, http.StatusOK)
		if s.tokenHashSet(t) {
			t.Errorf("token not cleared after both steps")
		}
		s.confirmOptOut(t, token, "").wantStatus(t, http.StatusUnauthorized)
		if b, c := s.bootstrapStatus(t); !b || !c {
			t.Errorf("status = %v, %v; want true, true", b, c)
		}
	})

	t.Run("a confirmed opt-out boot still mints while the bootstrap is pending", func(t *testing.T) {
		s := freshStack(t)
		s.exec(t, "opt out", `UPDATE mod_users.oidc_config SET opt_out = true, saved_at = now() WHERE id = 1`)
		if err := s.onboarding.RefreshState(context.Background()); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if !s.onboarding.CurrentState().Confirmed() {
			t.Fatalf("opt-out state not confirmed")
		}
		token := s.mintSetupToken(t)
		s.postBootstrap(t, token, bootstrapBody("first@operator.test", map[string]string{"kind": "natural_person"})).
			wantStatus(t, http.StatusCreated)
		if s.tokenHashSet(t) {
			t.Errorf("token not cleared: OIDC was already confirmed")
		}
	})

	t.Run("externally managed boot still mints while the bootstrap is pending", func(t *testing.T) {
		s := freshStack(t)
		s.onboarding.MarkExternallyManaged()
		s.mintSetupToken(t)
	})
}

// TestBootstrap_AdminCheck covers requirement 4: after the bootstrap, OIDC
// configuration needs an operator profile session holding wildcard manage.
func TestBootstrap_AdminCheck(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	s.confirmOptOut(t, token, "").wantStatus(t, http.StatusOK)
	email := "admin@operator.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, orgOperator())).wantStatus(t, http.StatusCreated).tok(t)

	// The operator admin session is accepted.
	s.confirmOptOut(t, "", tk.access()).wantStatus(t, http.StatusOK)

	// The same user's personal-profile session is refused.
	userID := s.principalID(t, email)
	personalUUID := s.entityUUID(t, s.personalProfileID(t, userID))
	personal := s.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": personalUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	s.confirmOptOut(t, "", personal.access()).wantStatus(t, http.StatusUnauthorized)

	// No session at all is refused.
	s.confirmOptOut(t, "", "").wantStatus(t, http.StatusUnauthorized)

	// A non-operator wildcard holder is impossible: mod-authz's operator-actor
	// trigger refuses a wildcard grant to the personal profile.
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO grants (actor_id, operation_id, target_id) SELECT $1, id, NULL FROM authz_operations WHERE slug = 'manage'`,
		s.personalProfileID(t, userID))
	var pgErr *pgconn.PgError
	if err == nil || !errorsAs(err, &pgErr) || pgErr.Code != "MF501" {
		t.Errorf("wildcard grant to a personal profile = %v, want MF501", err)
	}

	// An operator profile without wildcard manage is refused: a second
	// operator-party member with an active profile and no grant.
	member := s.register(t)
	memberID := s.principalID(t, member.Email)
	operatorParty := s.scanInt64(t, "operator party", `SELECT operator_party_id FROM platform_settings WHERE id = 1`)
	_, profUUID := s.seedPartyProfile(t, memberID, operatorParty, "active")
	s.exec(t, "authorize member password",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id)
		 SELECT c.id, $2, p.entity_id FROM mod_users.credentials c, profiles p
		  WHERE c.principal_id = $1 AND c.kind = 'password' AND p.holder_id = $3 AND p.party_id = $2`,
		memberID, operatorParty, userID)
	memberOp := s.do(t, http.MethodPost, "/v1/session/switch", member.tk.access(), map[string]string{"profile_uuid": profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	s.confirmOptOut(t, "", memberOp.access()).wantStatus(t, http.StatusUnauthorized)
}

// TestBootstrap_Landing: the bootstrap password lands on the admin profile;
// the email OTP credential lands on the personal profile with the admin
// profile locked (the operator party admits authorized credentials only).
func TestBootstrap_Landing(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	email := "admin@operator.test"
	s.postBootstrap(t, token, bootstrapBody(email, orgOperator())).wantStatus(t, http.StatusCreated)
	userID := s.principalID(t, email)
	adminUUID := s.scanUUIDText(t, `SELECT e.uuid::text FROM profiles p JOIN entities e ON e.id = p.entity_id
		WHERE p.holder_id = $1 AND p.party_id IS NOT NULL`, userID)
	personalUUID := s.entityUUID(t, s.personalProfileID(t, userID))

	pw := s.login(t, account{Email: email, Password: testPassword})
	if pw.Profile == nil || pw.Profile.UUID != adminUUID {
		t.Errorf("password login landed on %+v, want the admin profile %s", pw.Profile, adminUUID)
	}

	seen := s.mail.count(email)
	s.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": email}).wantStatus(t, http.StatusNoContent)
	code := s.mail.codeAfter(t, email, seen)
	otp := s.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": email, "code": code}).
		wantStatus(t, http.StatusOK).tok(t)
	if otp.Profile == nil || otp.Profile.UUID != personalUUID {
		t.Errorf("email-code login landed on %+v, want the personal profile %s", otp.Profile, personalUUID)
	}
	var admin *profileView
	for i := range otp.Profiles {
		if otp.Profiles[i].UUID == adminUUID {
			admin = &otp.Profiles[i]
		}
	}
	if admin == nil || admin.Available || admin.Lock == nil {
		t.Errorf("admin profile in the email-code session = %+v, want listed, unavailable and locked", admin)
	}
}

// scanUUIDText runs a one-row, one-column text query.
func (s *stack) scanUUIDText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var v string
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	return v
}

// jsonRequest builds a request whose body is a raw string or a JSON-encoded
// value.
func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytesReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
