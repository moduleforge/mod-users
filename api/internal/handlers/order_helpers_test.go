package handlers

// Shared harness for the HTTP error-precedence order tests of this package
// (apiresp/ordertest). This is the only file that declares shared order
// helpers; every identifier here is prefixed "order". Later order tests in
// this package add helpers only under their own prefixes.
//
// The harness wires the REAL UserAccountService, UserAccountsHandler,
// AssumeHandler, AppsHandler and RegisterAccountRoutes, with fakes only at the
// bottom: an authorizer that models the owner/admin versus stranger split, a
// db.Querier and coredb.Querier (embedding the interfaces; only the methods
// the service reads are implemented), a SQL-dispatching DBTX (the apps handler
// holds concrete *db.Queries and *coredb.Queries, and the service transactions
// run sqlc code against pgx.Tx), a fake txhelper.DB, a types.Resolver and an
// observer group.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/auth"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

const (
	// orderAdminID is the entity id of the owner/admin actor the authorizer
	// allows. orderStrangerID is an authenticated actor it always denies.
	orderAdminID    int64 = 1
	orderStrangerID int64 = 2
	// orderHolderID is the account-holder entity of the seeded user account.
	orderHolderID int64 = 100
	// orderAppEntityID is the entities.id (== apps.id) of the seeded app.
	orderAppEntityID int64 = 300
	// orderNaturalPersonTypeID is the types.id the service resolves for
	// natural_person.
	orderNaturalPersonTypeID int64 = 3

	// orderTakenEmail is an email another account already owns: writes of it
	// fail with a unique violation on user_accounts_email_key.
	orderTakenEmail = "taken@example.com"
)

// orderWorld is the seed data shared by the fakes and the tests.
type orderWorld struct {
	account  db.UserAccount // the existing account (holder orderHolderID)
	admin    db.UserAccount // the admin actor's own account (holder orderAdminID)
	appUUID  uuid.UUID      // the existing app
	missing  uuid.UUID      // a well-formed uuid that names nothing
	missing2 uuid.UUID      // a second one, for routes with two path ids
}

func orderNewWorld() orderWorld {
	return orderWorld{
		account: db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: orderHolderID,
			Email: pgtype.Text{String: "existing@example.com", Valid: true}},
		admin: db.UserAccount{ID: 8, Uuid: uuid.New(), AccountHolder: orderAdminID,
			Email: pgtype.Text{String: "admin@example.com", Valid: true}},
		appUUID:  uuid.New(),
		missing:  uuid.New(),
		missing2: uuid.New(),
	}
}

// --- authorizer ------------------------------------------------------------

// orderAuthzCall is one recorded Authorize call.
type orderAuthzCall struct {
	op        string
	hasTarget bool
	target    int64
}

// orderAuthorizer allows the admin actor and denies everyone else. Per the
// target-0 rule it denies the AuthorizeResolve-style miss probe (target 0)
// with a plain forbidden error for every actor, never erroring loudly. It
// records every call and implements AuthorizeType for the type-level routes.
type orderAuthorizer struct {
	mu    sync.Mutex
	calls []orderAuthzCall
}

func (a *orderAuthorizer) Authorize(ctx context.Context, op string, target *int64) error {
	c := orderAuthzCall{op: op}
	if target != nil {
		c.hasTarget, c.target = true, *target
	}
	a.mu.Lock()
	a.calls = append(a.calls, c)
	a.mu.Unlock()

	if c.hasTarget && c.target == 0 {
		return apiresp.ErrForbidden
	}
	return orderAllowAdmin(ctx)
}

func (a *orderAuthorizer) AuthorizeType(ctx context.Context, _ string, typeID int64) error {
	if typeID <= 0 {
		return apiresp.ErrForbidden
	}
	return orderAllowAdmin(ctx)
}

func orderAllowAdmin(ctx context.Context) error {
	actor, ok := opctx.EffectiveActorEntityID(ctx)
	if !ok {
		return apiresp.ErrUnauthenticated
	}
	if actor != orderAdminID {
		return apiresp.ErrForbidden
	}
	return nil
}

func (a *orderAuthorizer) reset() {
	a.mu.Lock()
	a.calls = nil
	a.mu.Unlock()
}

// callsWith returns the recorded calls for op whose target is exactly target.
func (a *orderAuthorizer) callsWith(op string, target int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.calls {
		if c.op == op && c.hasTarget && c.target == target {
			n++
		}
	}
	return n
}

var (
	_ coreAuthz.Authorizer = (*orderAuthorizer)(nil)
	_ interface {
		AuthorizeType(context.Context, string, int64) error
	} = (*orderAuthorizer)(nil)
)

// --- queriers --------------------------------------------------------------

// orderQuerier is a db.Querier fake: only the reads the user-account service
// and the grant closures use are implemented; anything else panics through
// the nil embed.
type orderQuerier struct {
	db.Querier
	w orderWorld
}

func (q orderQuerier) GetUserAccountByUUID(_ context.Context, id uuid.UUID) (db.UserAccount, error) {
	switch id {
	case q.w.account.Uuid:
		return q.w.account, nil
	case q.w.admin.Uuid:
		return q.w.admin, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

func (q orderQuerier) GetUserAccountByAccountHolder(_ context.Context, holder int64) (db.UserAccount, error) {
	if holder == q.w.admin.AccountHolder {
		return q.w.admin, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

func (q orderQuerier) SearchUserAccounts(context.Context, db.SearchUserAccountsParams) ([]db.UserAccount, error) {
	return []db.UserAccount{q.w.account}, nil
}

// orderCoreQuerier is a coredb.Querier fake with the entity and natural-person
// reads the service and the grant closures use.
type orderCoreQuerier struct{ coredb.Querier }

func (orderCoreQuerier) GetNaturalPersonByEntityID(_ context.Context, id int64) (coredb.GetNaturalPersonByEntityIDRow, error) {
	return coredb.GetNaturalPersonByEntityIDRow{
		EntityID:   id,
		GivenName:  pgtype.Text{String: "Ex", Valid: true},
		FamilyName: pgtype.Text{String: "Isting", Valid: true},
	}, nil
}

func (orderCoreQuerier) GetEntityByID(_ context.Context, id int64) (coredb.GetEntityByIDRow, error) {
	return coredb.GetEntityByIDRow{ID: id, Uuid: uuid.New()}, nil
}

// --- DBTX, tx, pool --------------------------------------------------------

// orderRow is a pgx.Row that copies vals into Scan destinations by position
// (a nil val leaves the destination at its zero value).
type orderRow struct {
	err  error
	vals []any
}

func (r orderRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if i >= len(r.vals) || r.vals[i] == nil {
			continue
		}
		reflect.ValueOf(d).Elem().Set(reflect.ValueOf(r.vals[i]))
	}
	return nil
}

// orderNoRows is a pgx.Rows with no rows.
type orderNoRows struct{ pgx.Rows }

func (orderNoRows) Next() bool { return false }
func (orderNoRows) Close()     {}
func (orderNoRows) Err() error { return nil }

// orderDBTX implements both db.DBTX and coredb.DBTX. It dispatches on the sqlc
// query name that heads every generated statement ("-- name: X :one").
type orderDBTX struct{ w orderWorld }

func orderQueryName(sql string) string {
	rest := strings.TrimPrefix(sql, "-- name: ")
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		return rest[:i]
	}
	return rest
}

func orderEmailArg(v any) string {
	if t, ok := v.(pgtype.Text); ok {
		return t.String
	}
	return ""
}

func orderUniqueEmailViolation() error {
	return &pgconn.PgError{Code: "23505", ConstraintName: "user_accounts_email_key"}
}

func orderAccountVals(ua db.UserAccount) []any {
	return []any{ua.ID, ua.Uuid, ua.AccountHolder, ua.Email, ua.EmailVerifiedAt, ua.DefaultAppID, ua.CreatedAt, ua.UpdatedAt}
}

func (d orderDBTX) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	switch orderQueryName(sql) {
	case "UpdateUserAccount":
		if len(args) > 1 && orderEmailArg(args[1]) == orderTakenEmail {
			return pgconn.CommandTag{}, orderUniqueEmailViolation()
		}
	case "AssignUserAccountToApp", "RemoveUserAccountFromApp", "SetAppUserAccountRoles",
		"ArchiveEntity", "DeleteAnonTokensByUserAccountID":
	default:
		return pgconn.CommandTag{}, fmt.Errorf("orderDBTX: unexpected Exec %q", orderQueryName(sql))
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (d orderDBTX) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	switch orderQueryName(sql) {
	case "ListAppUserAccounts":
		return orderNoRows{}, nil
	}
	return nil, fmt.Errorf("orderDBTX: unexpected Query %q", orderQueryName(sql))
}

func (d orderDBTX) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	name := orderQueryName(sql)
	switch name {
	case "GetAppByUUID":
		if len(args) == 1 && args[0] == d.w.appUUID {
			return orderRow{vals: []any{orderAppEntityID}}
		}
		return orderRow{err: pgx.ErrNoRows}
	case "GetUserAccountByUUID":
		if len(args) == 1 {
			switch args[0] {
			case d.w.account.Uuid:
				return orderRow{vals: orderAccountVals(d.w.account)}
			case d.w.admin.Uuid:
				return orderRow{vals: orderAccountVals(d.w.admin)}
			}
		}
		return orderRow{err: pgx.ErrNoRows}
	case "GetUserAccountByID":
		return orderRow{vals: orderAccountVals(d.w.account)}
	case "CreateUserAccount":
		if len(args) > 1 && orderEmailArg(args[1]) == orderTakenEmail {
			return orderRow{err: orderUniqueEmailViolation()}
		}
		ua := db.UserAccount{ID: 9, Uuid: uuid.New(), AccountHolder: orderHolderID + 1}
		if len(args) > 1 {
			ua.Email, _ = args[1].(pgtype.Text)
		}
		return orderRow{vals: orderAccountVals(ua)}
	case "GetNaturalPersonByEntityID":
		// No natural_persons row: the service skips the name write.
		return orderRow{err: pgx.ErrNoRows}
	case "GetEntityByID":
		return orderRow{vals: []any{orderHolderID}}
	}
	return orderRow{err: fmt.Errorf("orderDBTX: unexpected QueryRow %q", name)}
}

// orderTx is a pgx.Tx over orderDBTX. Unused methods panic through the nil
// embed.
type orderTx struct {
	pgx.Tx
	d orderDBTX
}

func (t orderTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.d.Exec(ctx, sql, args...)
}
func (t orderTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.d.Query(ctx, sql, args...)
}
func (t orderTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.d.QueryRow(ctx, sql, args...)
}
func (orderTx) Commit(context.Context) error   { return nil }
func (orderTx) Rollback(context.Context) error { return nil }

// orderPool is the fake txhelper.DB.
type orderPool struct{ d orderDBTX }

func (p orderPool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return orderTx{d: p.d}, nil
}

// orderNaturalPersons is a coreservice.NaturalPersonServicer fake; only
// CreateInTx (the create path) is implemented.
type orderNaturalPersons struct {
	coreservice.NaturalPersonServicer
}

func (orderNaturalPersons) CreateInTx(_ context.Context, _ pgx.Tx, in coreservice.CreateNaturalPersonInput) (coredb.CreateNaturalPersonRow, uuid.UUID, int64, error) {
	id := orderHolderID + 1
	return coredb.CreateNaturalPersonRow{
		EntityID:   id,
		GivenName:  pgtype.Text{String: in.GivenName, Valid: true},
		FamilyName: pgtype.Text{String: in.FamilyName, Valid: true},
	}, uuid.New(), id, nil
}

// --- context shim and router -----------------------------------------------

// orderUserContext installs a verified auth.UserContext matching the opctx
// effective actor, because mod-users handlers call auth.MustFromContext and
// the verified-email gate reads the context, while ordertest only installs an
// opctx identity. With no actor it does nothing, so authentication still
// answers 401.
func orderUserContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actor, ok := opctx.EffectiveActorEntityID(r.Context()); ok {
			verified := time.Now()
			uc := &auth.UserContext{
				EntityID:        actor,
				UserUUID:        uuid.NewString(),
				Email:           "actor@example.com",
				EmailVerifiedAt: &verified,
			}
			r = r.WithContext(auth.WithUserContext(r.Context(), uc))
		}
		next.ServeHTTP(w, r)
	})
}

// orderGrantClosure shapes a GrantAdminFn like the composition root's
// grantAdmin/revokeAdmin (api/cmd/server/main.go): nil-target manage first,
// then the account and holder lookups, with the wildcard-grant write itself
// replaced by a no-op.
func orderGrantClosure(az coreAuthz.Authorizer, q db.Querier, coreQ coredb.Querier) GrantAdminFn {
	return func(ctx context.Context, userAccountUUID uuid.UUID) error {
		if err := az.Authorize(ctx, "manage", nil); err != nil {
			return err
		}
		ua, err := q.GetUserAccountByUUID(ctx, userAccountUUID)
		if err != nil {
			return coreservice.ErrNotFound
		}
		if _, err := coreQ.GetEntityByID(ctx, ua.AccountHolder); err != nil {
			return errors.New("order grant closure: resolve entity")
		}
		return nil
	}
}

// orderAccountRouter wires the real RegisterAccountRoutes under /v1, behind
// the orderUserContext shim, a core RequireAuth (standing in for mod-users'
// token-verifying requireAuth, which ordertest cannot drive) and
// auth.RequireVerifiedEmail, the way the manifest composes them. It installs
// no token verification, because ordertest sets the identity directly. It
// also returns the recording authorizer and the seed data.
func orderAccountRouter() (http.Handler, *orderAuthorizer, orderWorld) {
	w := orderNewWorld()
	az := &orderAuthorizer{}
	dbtx := orderDBTX{w: w}
	q := orderQuerier{w: w}
	coreQ := orderCoreQuerier{}
	pool := orderPool{d: dbtx}
	obs := &observer.ObserverGroup{}
	typeRes := types.NewFromMap(map[string]int64{"natural_person": orderNaturalPersonTypeID})

	service := svc.NewUserAccountService(pool, q, coreQ, az, obs, orderNaturalPersons{}, typeRes, nil)
	uh := NewUserAccountsHandler(service, orderGrantClosure(az, q, coreQ), orderGrantClosure(az, q, coreQ))
	ah := NewAssumeHandler(service, "order-test-secret-order-test-secret", "order-test")
	apph := NewAppsHandler(pool, db.New(dbtx), az, obs, nil, coredb.New(dbtx))

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(orderUserContext)
		r.Use(coreAuthz.RequireAuth)
		r.Use(auth.RequireVerifiedEmail)
		RegisterAccountRoutes(r, uh, ah, apph)
	})
	return r, az, w
}

// orderRequest builds a request with an optional JSON body.
func orderRequest(method, path, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, path, nil)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}
