package handlers

// Order tests for the 15 self-service routes: GET/PUT /self, the 7 identities
// and credential routes, and the 6 SSH-key routes (self and operator). The
// real SelfHandler, IdentitiesHandler (step-up on), SSHKeysHandler and
// SSHKeyService run behind fakes (the shared order harness in
// order_helpers_test.go plus the orderSelf* fakes below), mounted the way the
// manifest composes them: requireOIDCConfirmed, then the core RequireAuth
// standing in for mod-users' token-verifying requireAuth, then
// auth.RequireVerifiedEmail for the write routes only.
//
// Expected order: 401, then 400 (stateless shape, with or without a step-up
// token, and never consuming the token), then the step-up 409, then
// authorization (403, masked), then credential 401s and other late statuses.
//
// ordertest models the three operator SSH-key routes (they have a target
// UUID). The other self routes have no target, so ordertest's missing/existing
// pair does not apply; they are covered by an explicit table test.
//
// Limit: IdentitiesHandler holds a concrete *pgxpool.Pool and runs Unlink and
// RemovePassword transactions on it, so a request that passes step-up on those
// two routes cannot be faked here (their 409/404 outcomes stay with the
// Postgres suites). The order up to the step-up gate is covered.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/ssh"

	"github.com/moduleforge/core-api/apiresp/ordertest"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

const (
	// orderSelfStrangerAccountID is the user_accounts id of the stranger actor.
	orderSelfStrangerAccountID int64 = 20
	// orderSelfPassword is the existing local credential's plaintext.
	orderSelfPassword = "current-password-1"
	// orderSelfDupLabel makes the fake InsertSSHPublicKey report the
	// duplicate-fingerprint unique violation.
	orderSelfDupLabel = "dup"
)

// orderSelfKeyUUID is the fixed uuid of the existing SSH key, so request tables
// built before a router exists name the same key.
var orderSelfKeyUUID = uuid.MustParse("00000000-0000-0000-0000-0000000000b1")

// orderSelfWorld extends the shared seed data.
type orderSelfWorld struct {
	orderWorld
	keyUUID uuid.UUID // the existing active SSH key
}

func orderSelfNewWorld() orderSelfWorld {
	return orderSelfWorld{orderWorld: orderNewWorld(), keyUUID: orderSelfKeyUUID}
}

// orderSelfLocalHash is the argon2 hash of orderSelfPassword, computed once.
var orderSelfLocalHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(orderSelfPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// orderSelfValidKey is a well-formed ed25519 authorized_keys line.
var orderSelfValidKey = func() string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
}()

// --- fakes -----------------------------------------------------------------

func orderSelfKeyRow(w orderSelfWorld) db.ModUsersSshPublicKey {
	return db.ModUsersSshPublicKey{
		ID: 50, Uuid: w.keyUUID, UserAccountID: w.account.ID, KeyType: "ssh-ed25519",
		PublicKey: orderSelfValidKey, FingerprintSha256: "SHA256:existing", Label: "laptop",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

func orderSelfKeyVals(k db.ModUsersSshPublicKey) []any {
	return []any{k.ID, k.Uuid, k.UserAccountID, k.KeyType, k.PublicKey, k.FingerprintSha256, k.Label, k.CreatedAt}
}

// orderSelfQuerier adds the SSH-key reads to the shared querier fake.
type orderSelfQuerier struct {
	orderQuerier
	w orderSelfWorld
}

func (q orderSelfQuerier) ListActiveSSHPublicKeysByUserAccount(_ context.Context, _ db.ListActiveSSHPublicKeysByUserAccountParams) ([]db.ModUsersSshPublicKey, error) {
	return []db.ModUsersSshPublicKey{orderSelfKeyRow(q.w)}, nil
}

func (q orderSelfQuerier) CountActiveSSHPublicKeysByUserAccount(_ context.Context, _ int64) (int64, error) {
	return 1, nil
}

func (q orderSelfQuerier) GetActiveSSHPublicKeyByUUIDForUserAccount(_ context.Context, arg db.GetActiveSSHPublicKeyByUUIDForUserAccountParams) (db.ModUsersSshPublicKey, error) {
	if arg.Uuid == q.w.keyUUID {
		return orderSelfKeyRow(q.w), nil
	}
	return db.ModUsersSshPublicKey{}, pgx.ErrNoRows
}

// orderSelfDBTX layers the identities, email-code and SSH-key statements over
// the shared DBTX fake.
type orderSelfDBTX struct {
	orderDBTX
	w orderSelfWorld
}

func (d orderSelfDBTX) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if orderQueryName(sql) == "ArchiveSSHPublicKey" {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return d.orderDBTX.Exec(ctx, sql, args...)
}

func (d orderSelfDBTX) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if orderQueryName(sql) == "ListOIDCIdentitiesByUserAccount" {
		return orderNoRows{}, nil
	}
	return d.orderDBTX.Query(ctx, sql, args...)
}

func (d orderSelfDBTX) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	switch orderQueryName(sql) {
	case "GetAuthLocal":
		now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
		return orderRow{vals: []any{d.w.admin.ID, orderSelfLocalHash(), now, now}}
	case "GetActiveEmailCode":
		return orderRow{err: pgx.ErrNoRows}
	case "InsertSSHPublicKey":
		if len(args) == 5 && args[4] == orderSelfDupLabel {
			return orderRow{err: &pgconn.PgError{Code: "23505", ConstraintName: "ssh_public_keys_active_fingerprint_uq"}}
		}
		k := orderSelfKeyRow(d.w)
		k.Uuid = uuid.New()
		return orderRow{vals: orderSelfKeyVals(k)}
	}
	return d.orderDBTX.QueryRow(ctx, sql, args...)
}

type orderSelfTx struct {
	pgx.Tx
	d orderSelfDBTX
}

func (t orderSelfTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.d.Exec(ctx, sql, args...)
}
func (t orderSelfTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.d.Query(ctx, sql, args...)
}
func (t orderSelfTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.d.QueryRow(ctx, sql, args...)
}
func (orderSelfTx) Commit(context.Context) error   { return nil }
func (orderSelfTx) Rollback(context.Context) error { return nil }

type orderSelfPool struct{ d orderSelfDBTX }

func (p orderSelfPool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return orderSelfTx{d: p.d}, nil
}

// orderSelfEntity and orderSelfNaturalPerson stand in for the core services
// SelfHandler stitches; only the methods it calls are implemented.
type orderSelfEntity struct{ coreservice.EntityServicer }

func (orderSelfEntity) GetSelf(context.Context, coredb.Querier) (coreservice.Profile, error) {
	return coreservice.Profile{
		Kind: "natural_person",
		NaturalPerson: &coredb.GetNaturalPersonByEntityIDRow{
			GivenName:  pgtype.Text{String: "Ad", Valid: true},
			FamilyName: pgtype.Text{String: "Min", Valid: true},
		},
	}, nil
}

type orderSelfNaturalPerson struct {
	coreservice.NaturalPersonServicer
}

func (orderSelfNaturalPerson) UpdateByEntityUUID(context.Context, coredb.Querier, uuid.UUID, coreservice.UpdateNaturalPersonInput) error {
	return nil
}

// orderSelfLink is the linkAuthorizer fake for StartLink.
type orderSelfLink struct{}

func (orderSelfLink) LinkAuthorizeURL(_, _, _ string) (string, string, error) {
	return "https://idp.test/authorize", "state-token", nil
}

// --- router ----------------------------------------------------------------

type orderSelfOpts struct {
	unverified bool             // the authenticated caller's email is unverified
	state      config.BootState // OIDC boot state; zero value means confirmed
}

type orderSelfEnv struct {
	h        http.Handler
	w        orderSelfWorld
	consumed *sync.Map
}

// orderSelfUserContext installs the UserContext requireAuth would, for the
// opctx actor ordertest set: the admin actor is the seeded admin account, the
// stranger a different account, so the token-bound account id and the caller's
// own UserUUID line up with the seed data.
func orderSelfUserContext(w orderSelfWorld, verified bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if actor, ok := opctx.EffectiveActorEntityID(r.Context()); ok {
				uc := &auth.UserContext{EntityID: actor, UserAccountID: orderSelfStrangerAccountID,
					UserUUID: uuid.NewString(), Email: "actor@example.com"}
				if actor == orderAdminID {
					uc.UserAccountID, uc.UserUUID = w.admin.ID, w.admin.Uuid.String()
				}
				if verified {
					now := time.Now()
					uc.EmailVerifiedAt = &now
				}
				r = r.WithContext(auth.WithUserContext(r.Context(), uc))
			}
			next.ServeHTTP(rw, r)
		})
	}
}

func orderSelfRouter(opts orderSelfOpts) orderSelfEnv {
	w := orderSelfNewWorld()
	az := &orderAuthorizer{}
	dbtx := orderSelfDBTX{orderDBTX: orderDBTX{w: w.orderWorld}, w: w}
	q := orderSelfQuerier{orderQuerier: orderQuerier{w: w.orderWorld}, w: w}
	coreQ := orderCoreQuerier{}
	pool := orderSelfPool{d: dbtx}
	obs := &observer.ObserverGroup{}
	consumed := &sync.Map{}

	selfH := &SelfHandler{
		q: db.New(dbtx), coreQ: coredb.New(dbtx),
		coreSvcs: &coreservice.Services{Entity: orderSelfEntity{}, NaturalPerson: orderSelfNaturalPerson{}},
	}
	idH := &IdentitiesHandler{
		queries: db.New(dbtx), oauth: orderSelfLink{}, obs: obs,
		jwtSecret: stepUpTestSecret, consumed: consumed, stepUpRequired: true,
	}
	sshH := NewSSHKeysHandler(svc.NewSSHKeyService(pool, q, coreQ, az, obs), stepUpTestSecret, consumed, true)

	state := opts.state
	if state == "" {
		state = config.BootStateConfirmedOK
	}

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(auth.RequireOIDCConfirmed(func() config.BootState { return state }))
		r.Use(orderSelfUserContext(w, !opts.unverified))
		r.Use(coreAuthz.RequireAuth)
		r.Group(func(r chi.Router) { // read routes: no RequireVerifiedEmail
			RegisterSelfGetRoute(r, selfH)
			RegisterSelfIdentitiesReadRoute(r, idH)
			RegisterSelfSSHKeysReadRoute(r, sshH)
		})
		r.Group(func(r chi.Router) { // write routes
			r.Use(auth.RequireVerifiedEmail)
			RegisterSelfPutRoute(r, selfH)
			RegisterSelfIdentitiesWriteRoutes(r, idH)
			RegisterSelfSSHKeysWriteRoutes(r, sshH)
			RegisterUserAccountSSHKeyRoutes(r, sshH)
		})
	})
	return orderSelfEnv{h: r, w: w, consumed: consumed}
}

func orderSelfDo(h http.Handler, id ordertest.Identity, method, path, body, token string) *httptest.ResponseRecorder {
	req := orderRequest(method, path, body)
	if token != "" {
		req.Header.Set("X-Step-Up-Token", token)
	}
	req = req.WithContext(id(req.Context()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func orderSelfConsumed(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

// orderSelfActionCode returns action.code of an action-required envelope.
func orderSelfActionCode(t *testing.T, body []byte) (code, path string) {
	t.Helper()
	var env struct {
		Action struct {
			Code, Message, Path string
		} `json:"action"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal action envelope: %v; body=%s", err, body)
	}
	return env.Action.Code, env.Action.Path
}

func orderSelfAssertStepUpRequired(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	code, path := orderSelfActionCode(t, rec.Body.Bytes())
	if code != "users.step_up_required" || path != "/step-up" {
		t.Errorf("action = {%q %q}, want {users.step_up_required /step-up}; body=%s", code, path, rec.Body.String())
	}
}

// --- ordertest: operator SSH-key routes ------------------------------------

// TestOrderSelf_OperatorSSHKeyRoutes runs ordertest over the three operator
// routes. The account is masked: an authorized caller on a missing account
// gets 403, never 404 (D2). Register's malformed key is a 400 for the
// forbidden caller too (D10), and a duplicate key is a 409 only after
// authorization.
func TestOrderSelf_OperatorSSHKeyRoutes(t *testing.T) {
	env := orderSelfRouter(orderSelfOpts{})
	w := env.w
	acct := "/v1/user-accounts/" + w.account.Uuid.String() + "/ssh-keys"
	missing := "/v1/user-accounts/" + w.missing.String() + "/ssh-keys"
	key := "/" + w.keyUUID.String()

	good := []byte(`{"public_key":"` + orderSelfValidKey + `"}`)
	base := func(name, method, existing, missingPath string, goodBody, bad []byte, late ...ordertest.Late) ordertest.Route {
		return ordertest.Route{
			Name: name, Handler: env.h, Method: method,
			ExistingPath: existing, MissingPath: missingPath,
			GoodBody: goodBody, BadBody: bad,
			Authorized: ordertest.Actor(orderAdminID), Unauthorized: ordertest.Actor(orderStrangerID),
			AuthorizedMissingStatus: http.StatusForbidden,
			Late:                    late,
		}
	}
	routes := []ordertest.Route{
		base("GET /user-accounts/{uuid}/ssh-keys", http.MethodGet, acct, missing, nil, nil),
		base("POST /user-accounts/{uuid}/ssh-keys [malformed json]", http.MethodPost, acct, missing, good, []byte(`{"public_key":`)),
		base("POST /user-accounts/{uuid}/ssh-keys [malformed key]", http.MethodPost, acct, missing, good, []byte(`{"public_key":"ssh-ed25519 AAAA"}`)),
		base("POST /user-accounts/{uuid}/ssh-keys [public_key required]", http.MethodPost, acct, missing, good, []byte(`{"public_key":""}`)),
		base("POST /user-accounts/{uuid}/ssh-keys [duplicate]", http.MethodPost, acct, missing, good, []byte(`{"public_key":"x","bogus":1}`),
			ordertest.Late{Name: "duplicate key 409",
				Body: []byte(`{"public_key":"` + orderSelfValidKey + `","label":"` + orderSelfDupLabel + `"}`), WantStatus: http.StatusConflict}),
		base("DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid}", http.MethodDelete, acct+key, missing+key, nil, nil,
			// A key that is unknown for the account is masked as 403 after authorization (F5).
			ordertest.Late{Name: "unknown key masked 403", Path: acct + "/" + w.missing2.String(), WantStatus: http.StatusForbidden}),
	}
	ordertest.Run(t, routes...)
}

// TestOrderSelf_OperatorMalformedUUID covers the path-UUID slot ordertest
// cannot model: 401 first, then 400 even for a caller who would be denied.
func TestOrderSelf_OperatorMalformedUUID(t *testing.T) {
	env := orderSelfRouter(orderSelfOpts{})
	acct, key := env.w.account.Uuid.String(), env.w.keyUUID.String()
	const bad = "not-a-uuid"
	cases := []struct{ name, method, path, body string }{
		{"list", http.MethodGet, "/v1/user-accounts/" + bad + "/ssh-keys", ""},
		{"register", http.MethodPost, "/v1/user-accounts/" + bad + "/ssh-keys", `{"public_key":"` + orderSelfValidKey + `"}`},
		{"revoke uuid", http.MethodDelete, "/v1/user-accounts/" + bad + "/ssh-keys/" + key, ""},
		{"revoke key_uuid", http.MethodDelete, "/v1/user-accounts/" + acct + "/ssh-keys/" + bad, ""},
	}
	ids := []struct {
		name string
		id   ordertest.Identity
		want int
	}{
		{"anonymous", ordertest.Anonymous, http.StatusUnauthorized},
		{"stranger", ordertest.Actor(orderStrangerID), http.StatusBadRequest},
		{"authorized", ordertest.Actor(orderAdminID), http.StatusBadRequest},
	}
	for _, tc := range cases {
		for _, id := range ids {
			t.Run(tc.name+"/"+id.name, func(t *testing.T) {
				rec := orderSelfDo(env.h, id.id, tc.method, tc.path, tc.body, "")
				if rec.Code != id.want {
					t.Fatalf("status = %d, want %d; body=%s", rec.Code, id.want, rec.Body.String())
				}
			})
		}
	}
}

// --- table: self-service routes --------------------------------------------

type orderSelfBad struct {
	name     string
	path     string // overrides the case path when set
	body     string
	wantCode string // detail (or error) code; "" skips the check
}

type orderSelfCase struct {
	name, method, path string
	good               string
	bad                []orderSelfBad
	stepUp             bool // gated by the step-up check inside the handler
	okStatus           int  // status of a well-formed request with a valid token; 0 = not reachable here
	verifiedGate       bool // mounted behind RequireVerifiedEmail
	noAuthGood         bool // the good request is made with no token at all (read routes)
}

func orderSelfCases(w orderSelfWorld) []orderSelfCase {
	return []orderSelfCase{
		{name: "GET /self", method: http.MethodGet, path: "/v1/self", okStatus: http.StatusOK, noAuthGood: true},
		{name: "PUT /self", method: http.MethodPut, path: "/v1/self", good: `{"given_name":"Ada"}`,
			okStatus: http.StatusOK, verifiedGate: true, noAuthGood: true,
			bad: []orderSelfBad{
				{"blank given_name", "", `{"given_name":" "}`, "natural_persons.given_name_required"},
				{"blank family_name", "", `{"family_name":""}`, "natural_persons.family_name_required"},
				{"malformed json", "", `{"given_name":`, "invalid_json_body"},
				{"unknown field", "", `{"bogus":1}`, "invalid_json_body"},
			}},
		{name: "GET /self/identities", method: http.MethodGet, path: "/v1/self/identities", okStatus: http.StatusOK, noAuthGood: true},
		{name: "POST /self/identities/oidc/{provider}/start", method: http.MethodPost,
			path: "/v1/self/identities/oidc/google/start?return=/self/identities", stepUp: true, okStatus: http.StatusOK, verifiedGate: true,
			bad: []orderSelfBad{
				{"absolute return", "/v1/self/identities/oidc/google/start?return=http://evil.test/x", "", "users.return_path_invalid"},
				{"protocol-relative return", "/v1/self/identities/oidc/google/start?return=//evil.test", "", "users.return_path_invalid"},
				{"relative return", "/v1/self/identities/oidc/google/start?return=relative", "", "users.return_path_invalid"},
			}},
		{name: "DELETE /self/identities/{identity_uuid}", method: http.MethodDelete,
			path: "/v1/self/identities/" + uuid.NewString(), stepUp: true, verifiedGate: true,
			bad: []orderSelfBad{{"malformed uuid", "/v1/self/identities/not-a-uuid", "", "invalid_uuid"}}},
		{name: "POST /self/credential/password", method: http.MethodPost, path: "/v1/self/credential/password",
			good: `{"new_password":"aaaaaaaaaaaa","current_password":"` + orderSelfPassword + `"}`, stepUp: true, verifiedGate: true,
			bad: []orderSelfBad{
				{"malformed json", "", `{not json`, "invalid_json_body"},
				{"unknown field", "", `{"new_password":"aaaaaaaaaaaa","bogus":1}`, "invalid_json_body"},
				{"new_password required", "", `{}`, "users.new_password_required"},
				{"too short, wrong current_password", "", `{"new_password":"short","current_password":"wrong"}`, "users.password_too_short"},
			}},
		{name: "DELETE /self/credential/password", method: http.MethodDelete, path: "/v1/self/credential/password",
			stepUp: true, verifiedGate: true},
		{name: "POST /self/credential/step-up", method: http.MethodPost, path: "/v1/self/credential/step-up",
			okStatus: http.StatusNoContent, verifiedGate: true, noAuthGood: true},
		{name: "POST /self/credential/step-up/verify", method: http.MethodPost, path: "/v1/self/credential/step-up/verify",
			good: `{"code":"123456"}`, verifiedGate: true, noAuthGood: true,
			bad: []orderSelfBad{
				{"malformed json", "", `{"code":`, "invalid_json_body"},
				{"empty code", "", `{"code":""}`, ""},
			}},
		{name: "GET /self/ssh-keys", method: http.MethodGet, path: "/v1/self/ssh-keys", okStatus: http.StatusOK, noAuthGood: true},
		{name: "POST /self/ssh-keys", method: http.MethodPost, path: "/v1/self/ssh-keys",
			good: `{"public_key":"` + orderSelfValidKey + `"}`, stepUp: true, okStatus: http.StatusCreated, verifiedGate: true,
			bad: []orderSelfBad{
				{"malformed json", "", `{"public_key":`, "invalid_json_body"},
				{"public_key required", "", `{"public_key":""}`, "users.public_key_required"},
				{"malformed key", "", `{"public_key":"ssh-ed25519 AAAA"}`, "users.ssh_key_invalid"},
				{"label too long", "", `{"public_key":"` + orderSelfValidKey + `","label":"` + strings.Repeat("a", 500) + `"}`, "users.ssh_key_label_too_long"},
			}},
		{name: "DELETE /self/ssh-keys/{key_uuid}", method: http.MethodDelete, path: "/v1/self/ssh-keys/" + w.keyUUID.String(),
			stepUp: true, okStatus: http.StatusNoContent, verifiedGate: true,
			bad: []orderSelfBad{{"malformed uuid", "/v1/self/ssh-keys/not-a-uuid", "", "invalid_uuid"}}},
	}
}

func TestOrderSelf_SelfServiceRoutes(t *testing.T) {
	admin := ordertest.Actor(orderAdminID)
	for _, tc := range orderSelfCases(orderSelfNewWorld()) {
		t.Run(tc.name, func(t *testing.T) {
			env := orderSelfRouter(orderSelfOpts{})
			token := func() string { return issueTestStepUpToken(t, env.w.admin.ID) }
			path := func(b orderSelfBad) string {
				if b.path != "" {
					return b.path
				}
				return tc.path
			}

			// Anonymous: 401 for a well-formed and for a malformed request.
			t.Run("anonymous good=401", func(t *testing.T) {
				if rec := orderSelfDo(env.h, ordertest.Anonymous, tc.method, tc.path, tc.good, ""); rec.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
				}
			})
			for _, b := range tc.bad {
				t.Run("anonymous bad="+b.name+"=401", func(t *testing.T) {
					if rec := orderSelfDo(env.h, ordertest.Anonymous, tc.method, path(b), b.body, ""); rec.Code != http.StatusUnauthorized {
						t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
					}
				})

				// Malformed: 400 with a valid step-up token (not consumed) and without one (D9).
				t.Run("bad="+b.name+"/with token=400", func(t *testing.T) {
					rec := orderSelfDo(env.h, admin, tc.method, path(b), b.body, token())
					if rec.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
					}
					if b.wantCode != "" {
						if got := decodeErrorCode(t, rec.Body.Bytes()); got != b.wantCode {
							t.Errorf("code = %q, want %q; body=%s", got, b.wantCode, rec.Body.String())
						}
					}
					if n := orderSelfConsumed(env.consumed); n != 0 {
						t.Errorf("step-up store has %d entries, want 0 (a malformed request must not consume the token)", n)
					}
				})
				t.Run("bad="+b.name+"/no token=400", func(t *testing.T) {
					rec := orderSelfDo(env.h, admin, tc.method, path(b), b.body, "")
					if rec.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want 400 (not 409); body=%s", rec.Code, rec.Body.String())
					}
				})
			}

			if tc.stepUp {
				t.Run("good no token=409 step_up_required", func(t *testing.T) {
					orderSelfAssertStepUpRequired(t, orderSelfDo(env.h, admin, tc.method, tc.path, tc.good, ""))
					if n := orderSelfConsumed(env.consumed); n != 0 {
						t.Errorf("step-up store has %d entries, want 0", n)
					}
				})
			}
			if tc.okStatus != 0 {
				t.Run("good succeeds", func(t *testing.T) {
					tk := ""
					if tc.stepUp {
						tk = token()
					}
					rec := orderSelfDo(env.h, admin, tc.method, tc.path, tc.good, tk)
					if rec.Code != tc.okStatus {
						t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.okStatus, rec.Body.String())
					}
				})
			}
		})
	}
}

// TestOrderSelf_CredentialFailuresAfterShape pins that the credential 401s sit
// after shape and step-up: they need a well-formed body and a valid token.
func TestOrderSelf_CredentialFailuresAfterShape(t *testing.T) {
	admin := ordertest.Actor(orderAdminID)
	env := orderSelfRouter(orderSelfOpts{})
	tk := func() string { return issueTestStepUpToken(t, env.w.admin.ID) }

	t.Run("SetPassword missing current_password", func(t *testing.T) {
		rec := orderSelfDo(env.h, admin, http.MethodPost, "/v1/self/credential/password", `{"new_password":"aaaaaaaaaaaa"}`, tk())
		if rec.Code != http.StatusUnauthorized || decodeErrorCode(t, rec.Body.Bytes()) != "users.bad_credentials" {
			t.Fatalf("status = %d code = %q, want 401 users.bad_credentials; body=%s", rec.Code, decodeErrorCode(t, rec.Body.Bytes()), rec.Body.String())
		}
	})
	t.Run("SetPassword wrong current_password", func(t *testing.T) {
		rec := orderSelfDo(env.h, admin, http.MethodPost, "/v1/self/credential/password",
			`{"new_password":"aaaaaaaaaaaa","current_password":"wrong"}`, tk())
		if rec.Code != http.StatusUnauthorized || decodeErrorCode(t, rec.Body.Bytes()) != "users.bad_credentials" {
			t.Fatalf("status = %d, want 401 users.bad_credentials; body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("SetPassword wrong current_password without token stays 409", func(t *testing.T) {
		orderSelfAssertStepUpRequired(t, orderSelfDo(env.h, admin, http.MethodPost, "/v1/self/credential/password",
			`{"new_password":"aaaaaaaaaaaa","current_password":"wrong"}`, ""))
	})
	t.Run("StepUpVerify wrong code", func(t *testing.T) {
		rec := orderSelfDo(env.h, admin, http.MethodPost, "/v1/self/credential/step-up/verify", `{"code":"123456"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestOrderSelf_StrangerOnOperatorSSHKeyRoutes: a stranger is 403 on every
// operator route against an existing account, and a malformed body from the
// stranger is still 400 (D10), before authorization.
func TestOrderSelf_StrangerOnOperatorSSHKeyRoutes(t *testing.T) {
	env := orderSelfRouter(orderSelfOpts{})
	stranger := ordertest.Actor(orderStrangerID)
	acct := "/v1/user-accounts/" + env.w.account.Uuid.String() + "/ssh-keys"
	good := `{"public_key":"` + orderSelfValidKey + `"}`
	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"list", http.MethodGet, acct, "", http.StatusForbidden},
		{"register well-formed", http.MethodPost, acct, good, http.StatusForbidden},
		{"register malformed key", http.MethodPost, acct, `{"public_key":"ssh-ed25519 AAAA"}`, http.StatusBadRequest},
		{"revoke", http.MethodDelete, acct + "/" + env.w.keyUUID.String(), "", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := orderSelfDo(env.h, stranger, tc.method, tc.path, tc.body, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// --- D11 gate positions ----------------------------------------------------

// TestOrderSelf_VerifiedEmailGatePosition documents the D11 exception: the
// requireVerifiedEmail gate depends only on the caller's own account state,
// reveals nothing about a target or about request-shape rules, and runs
// before the handler. So an unverified authenticated caller with a malformed
// body gets 403 action-required users.email_unverified, not 400. (Documented
// exception to "shape before authorization": see D11 in the conformance plan
// notes and the manifest order requireOIDCConfirmed, requireAuth,
// requireVerifiedEmail.) Anonymous still gets 401 first, and the read routes
// (no gate) answer an unverified caller normally.
func TestOrderSelf_VerifiedEmailGatePosition(t *testing.T) {
	env := orderSelfRouter(orderSelfOpts{unverified: true})
	admin := ordertest.Actor(orderAdminID)
	acctKeys := "/v1/user-accounts/" + env.w.account.Uuid.String() + "/ssh-keys"

	gated := []struct{ name, method, path, malformed string }{
		{"PUT /self", http.MethodPut, "/v1/self", `{"given_name":`},
		{"POST oidc start", http.MethodPost, "/v1/self/identities/oidc/google/start?return=http://evil.test/x", ""},
		{"DELETE identity", http.MethodDelete, "/v1/self/identities/not-a-uuid", ""},
		{"POST password", http.MethodPost, "/v1/self/credential/password", `{not json`},
		{"DELETE password", http.MethodDelete, "/v1/self/credential/password", ""},
		{"POST step-up", http.MethodPost, "/v1/self/credential/step-up", ""},
		{"POST step-up verify", http.MethodPost, "/v1/self/credential/step-up/verify", `{"code":`},
		{"POST self ssh-keys", http.MethodPost, "/v1/self/ssh-keys", `{"public_key":`},
		{"DELETE self ssh-key", http.MethodDelete, "/v1/self/ssh-keys/not-a-uuid", ""},
		{"GET operator ssh-keys", http.MethodGet, acctKeys, ""},
		{"POST operator ssh-keys", http.MethodPost, acctKeys, `{"public_key":`},
		{"DELETE operator ssh-key", http.MethodDelete, acctKeys + "/not-a-uuid", ""},
	}
	for _, tc := range gated {
		t.Run(tc.name, func(t *testing.T) {
			rec := orderSelfDo(env.h, admin, tc.method, tc.path, tc.malformed, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if code, path := orderSelfActionCode(t, rec.Body.Bytes()); code != "users.email_unverified" || path != "/verify-email" {
				t.Errorf("action = {%q %q}, want {users.email_unverified /verify-email}; body=%s", code, path, rec.Body.String())
			}
			if rec := orderSelfDo(env.h, ordertest.Anonymous, tc.method, tc.path, tc.malformed, ""); rec.Code != http.StatusUnauthorized {
				t.Errorf("anonymous status = %d, want 401", rec.Code)
			}
		})
	}

	for _, p := range []string{"/v1/self", "/v1/self/identities", "/v1/self/ssh-keys"} {
		t.Run("read route "+p+" ungated", func(t *testing.T) {
			if rec := orderSelfDo(env.h, admin, http.MethodGet, p, "", ""); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestOrderSelf_OIDCConfirmedGatePosition documents the other D11 gate:
// requireOIDCConfirmed is outermost in the manifest, so with onboarding
// unconfirmed every /v1 self route answers 503 users.oidc_not_confirmed even
// to an anonymous caller (503 before 401) and before any body check. It
// depends only on service state. (Documented exception, D11.)
func TestOrderSelf_OIDCConfirmedGatePosition(t *testing.T) {
	env := orderSelfRouter(orderSelfOpts{state: config.BootStateInitFailed})
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/self", ""},
		{http.MethodPut, "/v1/self", `{"given_name":`},
		{http.MethodGet, "/v1/self/identities", ""},
		{http.MethodPost, "/v1/self/credential/password", `{not json`},
		{http.MethodGet, "/v1/self/ssh-keys", ""},
		{http.MethodPost, "/v1/self/ssh-keys", `{"public_key":`},
	}
	ids := []struct {
		name string
		id   ordertest.Identity
	}{{"anonymous", ordertest.Anonymous}, {"authenticated", ordertest.Actor(orderAdminID)}}
	for _, rt := range routes {
		for _, id := range ids {
			t.Run(rt.method+" "+rt.path+"/"+id.name, func(t *testing.T) {
				rec := orderSelfDo(env.h, id.id, rt.method, rt.path, rt.body, "")
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
				}
				if code, _ := orderSelfActionCode(t, rec.Body.Bytes()); code != "users.oidc_not_confirmed" {
					t.Errorf("action code = %q, want users.oidc_not_confirmed; body=%s", code, rec.Body.String())
				}
			})
		}
	}

	// Confirmed again: authentication answers first.
	conf := orderSelfRouter(orderSelfOpts{})
	if rec := orderSelfDo(conf.h, ordertest.Anonymous, http.MethodGet, "/v1/self", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("confirmed anonymous status = %d, want 401", rec.Code)
	}
}
