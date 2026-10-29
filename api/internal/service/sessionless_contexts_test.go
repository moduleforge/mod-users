package service

// This file pins the sessionless AuthContexts mod-users builds for real users.
//
// The authorization engine (internal/authz, func gated) treats an AuthContext
// that is non-anonymous and carries neither a session nor a credential as a
// trusted system actor: the assurance gate and the owning-party gate are
// skipped for it. mod-users builds exactly such contexts for real users in its
// unauthenticated flows (sign-up, password reset, OIDC just-in-time creation,
// bootstrap), so those contexts must only ever be used for observations and
// the unauthorized creation writes of those flows and must never reach
// Authorize, AuthorizeType or AuthorizeTypeIn. Until mod-core grows an
// explicit system-actor marker, "sessionless means trusted" is inferred from
// absent fields; these tests are the guard that keeps a real user's sessionless
// context away from the engine.
//
// Three pins:
//
//  1. TestSessionlessHelpers_AreSessionless: every service-level helper that
//     returns a sessionless context does return one (so the other pins are
//     about the right contexts).
//  2. TestSessionlessContexts_NeverReachAuthorize: the flows that consume
//     those contexts, run against a failing recording authorizer, never call
//     it with a sessionless context.
//  3. TestSessionlessContexts_SourceAudit: a static audit of every
//     non-test source file in the api module. It enumerates the sessionless
//     context builders and their callers (so a new one fails the test until it
//     is reviewed and added here) and asserts that none of them, nor anything
//     they transitively call by name, calls an Authorize entry point.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// isSessionless reports whether ac is the shape internal/authz.gated treats as
// a trusted system actor: authenticated-looking (a principal and an acting
// profile, not anonymous) but carrying neither a session nor a credential.
func isSessionless(ac opctx.AuthContext) bool {
	return !ac.Anonymous && ac.SessionID == 0 && ac.CredentialID == 0 &&
		ac.PrincipalID > 0 && ac.ProfileID > 0 && ac.ImpersonatorPrincipalID == 0
}

func requireSessionless(t *testing.T, name string, ctx context.Context) {
	t.Helper()
	ac, ok := opctx.Auth(ctx)
	if !ok {
		t.Fatalf("%s: context carries no AuthContext", name)
	}
	if !isSessionless(ac) {
		t.Errorf("%s: AuthContext = %+v, want a non-anonymous context with SessionID == 0 and CredentialID == 0", name, ac)
	}
}

// actingDB is a usersdb.DBTX whose only query, the fallback acting profile
// lookup, returns profileID.
type actingDB struct {
	usersdb.DBTX
	profileID int64
}

func (d actingDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return profileRow{profileID: d.profileID}
}

type profileRow struct{ profileID int64 }

func (r profileRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.profileID
	return nil
}

func TestSessionlessHelpers_AreSessionless(t *testing.T) {
	t.Parallel()
	base := context.Background()

	ucx, ok, err := UnauthenticatedActingContext(base, actingDB{profileID: 9}, 5)
	if err != nil || !ok {
		t.Fatalf("UnauthenticatedActingContext: ok=%v err=%v", ok, err)
	}

	for name, ctx := range map[string]context.Context{
		"withActingProfile":            withActingProfile(base, 5, 9),
		"CreatedUser.ActingContext":    CreatedUser{User: usersdb.User{EntityID: 5}, PersonalProfileID: 9}.ActingContext(base),
		"UnauthenticatedActingContext": ucx,
	} {
		requireSessionless(t, name, ctx)
	}

	// A managed user has no personal profile: ActingContext adds nothing, so
	// it cannot manufacture a sessionless actor.
	managed := CreatedUser{User: usersdb.User{EntityID: 5}}.ActingContext(base)
	if _, ok := opctx.Auth(managed); ok {
		t.Error("CreatedUser.ActingContext for a managed user must return ctx unchanged")
	}
}

// authorizeProbe is a failing authorizer that records every call it receives
// through any of the three engine entry points, along with whether the context
// it was handed is a sessionless one.
type authorizeProbe struct {
	mu    sync.Mutex
	calls []string
}

func (p *authorizeProbe) record(ctx context.Context, entry, op string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	desc := entry + "(" + op + ")"
	if ac, ok := opctx.Auth(ctx); ok && isSessionless(ac) {
		desc += " with a sessionless context"
	}
	p.calls = append(p.calls, desc)
	return apiresp.ErrForbidden
}

func (p *authorizeProbe) Authorize(ctx context.Context, op string, _ *int64) error {
	return p.record(ctx, "Authorize", op)
}

func (p *authorizeProbe) AuthorizeType(ctx context.Context, op string, _ int64) error {
	return p.record(ctx, "AuthorizeType", op)
}

func (p *authorizeProbe) AuthorizeTypeIn(ctx context.Context, op string, _ int64, _ int64) error {
	return p.record(ctx, "AuthorizeTypeIn", op)
}

// ctxCapturingPersons records the context the natural-person write receives.
type ctxCapturingPersons struct {
	fakeNaturalPersons
	got []context.Context
}

func (f *ctxCapturingPersons) CreateInTx(ctx context.Context, tx pgx.Tx, in coreservice.CreateNaturalPersonInput) (coredb.CreateNaturalPersonRow, uuid.UUID, int64, error) {
	f.got = append(f.got, ctx)
	return f.fakeNaturalPersons.CreateInTx(ctx, tx, in)
}

func TestSessionlessContexts_NeverReachAuthorize(t *testing.T) {
	t.Parallel()

	newFlow := func() (*UserService, *authorizeProbe, *ctxCapturingPersons, *fakeUsersQ) {
		log := &creationLog{}
		q := creationUsersQ(log)
		q.getUser = func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil }
		q.getPrimaryEmail = func(int64) (usersdb.UserIdentifier, error) { return usersdb.UserIdentifier{}, pgx.ErrNoRows }
		q.createIdentifier = func(a usersdb.CreateIdentifierParams) (usersdb.UserIdentifier, error) {
			return identifierRow(11, a.UserID, a.ValueCanon, a.IsPrimary, a.VerifiedAt != nil), nil
		}
		q.createOIDC = func(a usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error) {
			return usersdb.CredOidc{CredentialID: a.CredentialID}, nil
		}
		probe := &authorizeProbe{}
		np := &ctxCapturingPersons{fakeNaturalPersons: fakeNaturalPersons{log: log}}
		// newTestUserService takes the concrete *fakeAuthorizer, so build the
		// services here with the probe instead.
		obs := &recordingObserver{}
		s := newTestUserService(q, newCreationCoreQ(log), np, nil, obs)
		s.az = probe
		s.creds.az = probe
		return s, probe, np, q
	}

	t.Run("sign-up (bring-your-own user, identifier, oidc link)", func(t *testing.T) {
		t.Parallel()
		s, probe, np, _ := newFlow()
		created, err := s.CreateBYOUserInTx(context.Background(), nil, CreateBYOUserInput{GivenName: "Ada", FamilyName: "Lovelace"})
		if err != nil {
			t.Fatalf("CreateBYOUserInTx: %v", err)
		}
		if len(np.got) != 1 {
			t.Fatalf("person writes = %d, want 1", len(np.got))
		}
		requireSessionless(t, "context handed to the person write", np.got[0])

		actCtx := created.ActingContext(context.Background())
		requireSessionless(t, "CreatedUser.ActingContext", actCtx)
		if _, err := s.AddIdentifierInTx(actCtx, nil, AddIdentifierInput{
			UserID: created.User.EntityID, Kind: IdentifierKindEmail, Value: "ada@example.com", Primary: true,
		}); err != nil {
			t.Fatalf("AddIdentifierInTx: %v", err)
		}
		if _, err := s.creds.LinkOIDCInTx(actCtx, nil, LinkOIDCInput{
			PrincipalID: created.User.EntityID, ProviderID: "p", Issuer: "i", Subject: "s",
		}); err != nil {
			t.Fatalf("LinkOIDCInTx: %v", err)
		}
		if len(probe.calls) != 0 {
			t.Errorf("a sessionless flow reached the authorizer: %v", probe.calls)
		}
	})

	t.Run("guest sign-in", func(t *testing.T) {
		t.Parallel()
		s, probe, _, _ := newFlow()
		if _, err := s.CreateGuestUserInTx(context.Background(), nil, CreateGuestUserInput{DeviceID: "dev-1"}); err != nil {
			t.Fatalf("CreateGuestUserInTx: %v", err)
		}
		if len(probe.calls) != 0 {
			t.Errorf("a sessionless flow reached the authorizer: %v", probe.calls)
		}
	})

	// The probe must be able to see a violation, or the assertions above
	// would pass vacuously.
	t.Run("the probe records a call made with a sessionless context", func(t *testing.T) {
		t.Parallel()
		probe := &authorizeProbe{}
		err := probe.Authorize(withActingProfile(context.Background(), 5, 9), "update", nil)
		if !errors.Is(err, apiresp.ErrForbidden) || len(probe.calls) != 1 ||
			!strings.Contains(probe.calls[0], "sessionless") {
			t.Errorf("err=%v calls=%v", err, probe.calls)
		}
	})
}

// ---------------------------------------------------------------------------
// static audit
// ---------------------------------------------------------------------------

// sessionlessContextRoots is the exact set of non-test functions that build a
// sessionless AuthContext or call a helper that does (withActingProfile,
// CreatedUser.ActingContext, UnauthenticatedActingContext). It is a closed list
// on purpose: a new entry must be reviewed to confirm its context never
// reaches Authorize, AuthorizeType or AuthorizeTypeIn, and then added here.
var sessionlessContextRoots = []string{
	"internal/handlers/auth/oidc_flows.go:OIDCHandler.createBYOUser",
	"internal/handlers/auth/oidc_flows.go:OIDCHandler.createManagedUser",
	"internal/handlers/auth/oidc_flows.go:OIDCHandler.sessionActingContext",
	"internal/handlers/auth/register.go:Handler.Register",
	"internal/handlers/auth/reset.go:Handler.replacePassword",
	"internal/service/bootstrap_service.go:BootstrapService.bootstrapInTx",
	"internal/service/helpers.go:withActingProfile",
	"internal/service/session_service.go:UnauthenticatedActingContext",
	"internal/service/user_service.go:CreatedUser.ActingContext",
	"internal/service/user_service.go:UserService.CreateBYOUserInTx",
	"internal/service/user_service.go:UserService.CreateGuestUserInTx",
}

// sessionlessReceivers are the functions the roots hand a sessionless context
// to. Each, and everything it calls (matched by bare function or method name,
// an over-approximation), must be free of Authorize entry-point calls. The
// roots themselves are checked the same way but are not walked transitively:
// bootstrapInTx and Register also start a real session, whose own gates
// legitimately authorize under the session's context.
var sessionlessReceivers = []string{
	"internal/service/bootstrap_service.go:BootstrapService.createOrganization",
	"internal/service/bootstrap_service.go:BootstrapService.createWildcardManageGrant",
	"internal/service/credential_service.go:CredentialService.CreateAnonInTx",
	"internal/service/credential_service.go:CredentialService.CreatePasswordInTx",
	"internal/service/credential_service.go:CredentialService.EnsureEmailOTPInTx",
	"internal/service/credential_service.go:CredentialService.LinkOIDCInTx",
	"internal/service/credential_service.go:CredentialService.SupersedePasswordInTx",
	"internal/service/profile_service.go:ProfileService.createInTx",
	"internal/service/user_service.go:UserService.AddIdentifierInTx",
	"internal/service/user_service.go:UserService.createUserAndProfile",
}

var authorizeEntryPoints = map[string]bool{"Authorize": true, "AuthorizeType": true, "AuthorizeTypeIn": true}

type auditedFunc struct {
	key       string
	calls     map[string]bool // bare callee names
	literals  int             // sessionless opctx.AuthContext literals
	authorize []string        // Authorize entry points called directly
}

func TestSessionlessContexts_SourceAudit(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..") // the api module
	fset := token.NewFileSet()
	var funcs []*auditedFunc
	byName := map[string][]*auditedFunc{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				name = receiverName(fd.Recv.List[0].Type) + "." + name
			}
			af := &auditedFunc{key: rel + ":" + name, calls: map[string]bool{}}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					callee := calleeName(n.Fun)
					if callee == "" {
						break
					}
					af.calls[callee] = true
					if authorizeEntryPoints[callee] {
						af.authorize = append(af.authorize, callee)
					}
				case *ast.CompositeLit:
					if isSessionlessAuthContextLiteral(n) {
						af.literals++
					}
				}
				return true
			})
			funcs = append(funcs, af)
			bare := fd.Name.Name
			byName[bare] = append(byName[bare], af)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	byKey := map[string]*auditedFunc{}
	for _, f := range funcs {
		byKey[f.key] = f
	}

	// Pin the set of roots exactly.
	constructors := map[string]bool{"withActingProfile": true, "ActingContext": true, "UnauthenticatedActingContext": true}
	var found []string
	for _, f := range funcs {
		hit := f.literals > 0
		for c := range f.calls {
			if constructors[c] {
				hit = true
			}
		}
		if hit {
			found = append(found, f.key)
		}
	}
	sort.Strings(found)
	want := append([]string(nil), sessionlessContextRoots...)
	sort.Strings(want)
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the set of functions that build or call a sessionless-context helper changed.\n"+
			"Confirm the new context never reaches Authorize, AuthorizeType or AuthorizeTypeIn "+
			"(see the file comment), then update sessionlessContextRoots.\n got: %v\nwant: %v", found, want)
	}

	// Roots: no direct Authorize entry-point call.
	for _, key := range sessionlessContextRoots {
		if f := byKey[key]; len(f.authorize) > 0 {
			t.Errorf("%s builds or holds a sessionless context and calls %v", key, f.authorize)
		}
	}

	// Receivers: no Authorize entry point anywhere in the by-name closure.
	for _, key := range sessionlessReceivers {
		start, ok := byKey[key]
		if !ok {
			t.Errorf("sessionless receiver %s no longer exists; update sessionlessReceivers", key)
			continue
		}
		seen := map[*auditedFunc]bool{}
		var walk func(f *auditedFunc, path []string)
		walk = func(f *auditedFunc, path []string) {
			if seen[f] {
				return
			}
			seen[f] = true
			path = append(path, f.key)
			if len(f.authorize) > 0 {
				t.Errorf("%s receives a sessionless context but reaches %v via %s", key, f.authorize, strings.Join(path, " -> "))
			}
			for callee := range f.calls {
				for _, next := range byName[callee] {
					// Same package only: a bare name shared with another
					// package's method is not an edge of this call graph.
					if filepath.Dir(next.key) == filepath.Dir(f.key) {
						walk(next, path)
					}
				}
			}
		}
		walk(start, nil)
	}
}

// isSessionlessAuthContextLiteral reports whether lit is a non-empty
// opctx.AuthContext literal naming neither SessionID, CredentialID nor
// Anonymous: a context that carries no session, no credential and is not the
// anonymous actor.
func isSessionlessAuthContextLiteral(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "AuthContext" || len(lit.Elts) == 0 {
		return false
	}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return false // positional literal: not one of the audited shapes
		}
		if id, ok := kv.Key.(*ast.Ident); ok && (id.Name == "SessionID" || id.Name == "CredentialID" || id.Name == "Anonymous") {
			return false
		}
	}
	return true
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func receiverName(t ast.Expr) string {
	switch e := t.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return receiverName(e.X)
	}
	return "?"
}
