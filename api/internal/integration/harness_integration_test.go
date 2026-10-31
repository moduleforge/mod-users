//go:build integration

// Package integration holds the HTTP-level integration suites of the identity
// core: the real services, handlers, authentication middleware and token
// carrier composed over a throwaway Postgres with mod-core, mod-authz,
// mod-users and mod-audit migrations applied, driven through
// httptest exactly as a client would.
//
// Run with a throwaway Postgres (never the shared users-module-postgres):
//
//	name="mod-users-integ-$(openssl rand -hex 4)"
//	docker run -d --rm --name "$name" -e POSTGRES_USER=users -e POSTGRES_PASSWORD=users \
//	  -e POSTGRES_DB=postgres -p 127.0.0.1::5432 postgres:16
//	port="$(docker port "$name" 5432/tcp | head -1 | sed 's/.*://')"
//	until docker exec "$name" pg_isready -U users; do sleep 1; done
//	make -C model compose
//	(cd api && AUTHZ_DEV_PG_HOST=127.0.0.1 AUTHZ_DEV_PG_PORT="$port" \
//	   go test -tags=integration -p 1 -count=1 ./internal/integration/...)
//	docker rm -f "$name"
//
// The environment variables and the refusal of an unconfirmed shared Postgres
// are the same as for internal/authz (see its header). The suite drops and
// recreates its own database (users_integ_http). mod-audit's audit_log table
// is part of the composed migrations (make -C model compose); the real
// mod-audit observer runs in the composed observer group so a vocabulary or principal
// mismatch fails the operation exactly as it would in production.
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	auditservice "github.com/moduleforge/audit-api/service"
	auditdb "github.com/moduleforge/audit-model/db"
	authzapi "github.com/moduleforge/authz-api/authz"
	authzhttpapi "github.com/moduleforge/authz-api/httpapi"
	authzservice "github.com/moduleforge/authz-api/service"
	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/fieldcrypto"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/auth"
	localAuthz "github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/handlers"
	authhandlers "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	"github.com/moduleforge/mod-users/api/internal/handlers/me"
	"github.com/moduleforge/mod-users/api/internal/handlers/parties"
	"github.com/moduleforge/mod-users/api/internal/handlers/principals"
	"github.com/moduleforge/mod-users/api/internal/handlers/session"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

const (
	integDB     = "users_integ_http"
	integIssuer = "mod-users-integ"
)

// ---------------------------------------------------------------------------
// recording observer
// ---------------------------------------------------------------------------

// observation is one recorded Observe call.
type observation struct {
	Op       string
	Resource string
	Target   int64
	Auth     opctx.AuthContext
	HasAuth  bool
	Before   any
	After    any
}

// recorder is a MutationObserver that records every in-transaction
// observation. Observations of a transaction that later rolls back stay in the
// list, so a test asserting "nothing was written" must assert on the database.
type recorder struct {
	mu   sync.Mutex
	list []observation
	// committed holds the after-commit observations, which the group dispatches
	// only once a transaction has committed. A rollback leaves it untouched.
	committed []observation
}

func (r *recorder) Observe(ctx context.Context, _ pgx.Tx, op, resource string, target *int64, before, after any) error {
	o := observation{Op: op, Resource: resource, Before: before, After: after}
	if target != nil {
		o.Target = *target
	}
	o.Auth, o.HasAuth = opctx.Auth(ctx)
	r.mu.Lock()
	r.list = append(r.list, o)
	r.mu.Unlock()
	return nil
}

func (r *recorder) ObserveAfterCommit(ctx context.Context, op, resource string, target *int64, after any) {
	o := observation{Op: op, Resource: resource, After: after}
	if target != nil {
		o.Target = *target
	}
	o.Auth, o.HasAuth = opctx.Auth(ctx)
	r.mu.Lock()
	r.committed = append(r.committed, o)
	r.mu.Unlock()
}

// afterCommit returns a copy of the after-commit observations.
func (r *recorder) afterCommit() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observation(nil), r.committed...)
}

// afterCommitCount counts the after-commit observations of one op and resource.
func (r *recorder) afterCommitCount(op, resource string) int {
	n := 0
	for _, o := range r.afterCommit() {
		if o.Op == op && o.Resource == resource {
			n++
		}
	}
	return n
}

// reset clears both lists.
func (r *recorder) reset() {
	r.mu.Lock()
	r.list = nil
	r.committed = nil
	r.mu.Unlock()
}

// all returns a copy of the list.
func (r *recorder) all() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observation(nil), r.list...)
}

// pairs returns the "op resource" pairs in order.
func (r *recorder) pairs() []string {
	var out []string
	for _, o := range r.all() {
		out = append(out, o.Op+" "+o.Resource)
	}
	return out
}

// ---------------------------------------------------------------------------
// mailbox
// ---------------------------------------------------------------------------

// mailbox is the email.Sender of the stack: it keeps every message and lets a
// test wait for the code mailed to an address.
type mailbox struct {
	mu   sync.Mutex
	msgs []mail
}

type mail struct {
	To, Subject, Body string
}

func (m *mailbox) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	m.msgs = append(m.msgs, mail{To: to, Subject: subject, Body: body})
	m.mu.Unlock()
	return nil
}

func (m *mailbox) count(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.msgs {
		if x.To == to {
			n++
		}
	}
	return n
}

var codeRE = regexp.MustCompile(`code is: (\S+)`)

// codeAfter waits for the (seen+1)th message to `to` and returns the code in it.
func (m *mailbox) codeAfter(t *testing.T, to string, seen int) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		var got []mail
		for _, x := range m.msgs {
			if x.To == to {
				got = append(got, x)
			}
		}
		m.mu.Unlock()
		if len(got) > seen {
			if sub := codeRE.FindStringSubmatch(got[len(got)-1].Body); sub != nil {
				return sub[1]
			}
			t.Fatalf("mail to %s carries no code: %q", to, got[len(got)-1].Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no mail to %s arrived (seen %d)", to, seen)
	return ""
}

// ---------------------------------------------------------------------------
// stack
// ---------------------------------------------------------------------------

// stack is the composed module under test.
type stack struct {
	pool        *pgxpool.Pool
	cfg         *config.Config
	signer      *auth.Signer
	signKey     ed25519.PrivateKey
	handler     http.Handler
	obs         *recorder
	group       *observer.ObserverGroup
	mail        *mailbox
	oauth       *auth.OAuth
	az          *localAuthz.Authorizer
	authn       *auth.Authenticator
	users       *svc.UserService
	profiles    *svc.ProfileService
	partyPolicy *svc.PartyPolicyService
	personal    *svc.PersonalPartyService
	gate        *svc.CollaboratorGate
	creds       *svc.CredentialService
	sessions    *svc.SessionService
	imps        *svc.ImpersonationService
	chal        *svc.ChallengeService
	dir         *svc.DirectoryService
	boot        *svc.BootstrapService
	onboarding  *handlers.OIDCConfigHandler
	q           *db.Queries
	coreQ       *coredb.Queries
	opReg       *authzapi.OperationRegistry

	// auditApplied is true when mod-audit's audit_log table is part of the
	// database and its observer is in the group.
	auditApplied bool
}

var env *stack

func TestMain(m *testing.M) {
	if err := checkPrereqs(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: skipping HTTP integration suite -- %v\n", err)
		os.Exit(0)
	}
	host, port := resolveHost(), resolvePort()
	if err := resetDB(host, port); err != nil {
		fmt.Fprintf(os.Stderr, "integration: DB reset failed: %v\n", err)
		os.Exit(1)
	}
	dsn := fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", host, port, integDB)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: open pool: %v\n", err)
		os.Exit(1)
	}
	if err := checkClockSkew(context.Background(), pool); err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		pool.Close()
		os.Exit(1)
	}
	s, err := newStack(context.Background(), pool, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: compose stack: %v\n", err)
		pool.Close()
		os.Exit(1)
	}
	env = s
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// maxClockSkew is the largest difference between the database clock and this
// process's clock the suite tolerates. The module stamps times with the
// database clock (auth_time, expiries) and compares them with its own, so a
// database container whose clock drifted (for example after the host slept)
// makes freshness and expiry assertions fail for reasons that have nothing to
// do with the code under test.
const maxClockSkew = 10 * time.Second

func checkClockSkew(ctx context.Context, pool *pgxpool.Pool) error {
	var dbNow time.Time
	before := time.Now()
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return fmt.Errorf("read database clock: %w", err)
	}
	skew := dbNow.Sub(before)
	if skew < 0 {
		skew = -skew
	}
	if skew > maxClockSkew {
		return fmt.Errorf("the database clock differs from this machine's by %v (limit %v); resync the Postgres container's clock and rerun", skew.Round(time.Second), maxClockSkew)
	}
	return nil
}

func checkPrereqs() error {
	if os.Getenv("AUTHZ_DEV_PG_HOST") == "" {
		out, err := exec.Command("docker", "inspect", "--format={{.State.Running}}", "users-module-postgres").Output()
		if err != nil {
			return fmt.Errorf("docker inspect: %w", err)
		}
		if strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("container users-module-postgres is not running")
		}
	} else if resolvePort() == "5432" && os.Getenv("AUTHZ_DEV_PG_CONFIRM_SHARED") != "1" {
		return fmt.Errorf("refusing to run: AUTHZ_DEV_PG_HOST is set but AUTHZ_DEV_PG_PORT is unset or 5432 (likely a shared Postgres); " +
			"set AUTHZ_DEV_PG_PORT to a throwaway container's port, or AUTHZ_DEV_PG_CONFIRM_SHARED=1 to confirm using the shared instance")
	}
	if _, err := exec.LookPath("goose"); err != nil {
		return fmt.Errorf("goose not in PATH: %w", err)
	}
	if info, err := os.Stat(migrationsDir()); err != nil || !info.IsDir() {
		return fmt.Errorf("composed migrations dir %s not found -- run `make -C model compose` first", migrationsDir())
	}
	return nil
}

func resolveHost() string {
	if h := os.Getenv("AUTHZ_DEV_PG_HOST"); h != "" {
		return h
	}
	out, err := exec.Command("docker", "inspect", "--format",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", "users-module-postgres").Output()
	if err == nil {
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	return "localhost"
}

func resolvePort() string {
	if p := os.Getenv("AUTHZ_DEV_PG_PORT"); p != "" {
		return p
	}
	return "5432"
}

// repoRoot is the mod-users checkout, resolved from this file's location.
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func migrationsDir() string {
	if d := os.Getenv("AUTHZ_INTEG_MIGRATIONS_DIR"); d != "" {
		return d
	}
	return filepath.Join(repoRoot(), "model", "schema", "migrations")
}

// resetDB drops and recreates the suite database and applies the composed
// migrations directly (mod-audit's audit_log migration is part of it).
func resetDB(host, port string) error {
	ctx := context.Background()
	adminURL := fmt.Sprintf("postgres://users:users@%s:%s/postgres?sslmode=disable", host, port)
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + integDB, "CREATE DATABASE " + integDB} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}

	dir := migrationsDir()
	dsn := fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", host, port, integDB)
	out, err := exec.Command("goose", "-dir", dir, "postgres", dsn, "up").CombinedOutput() //nolint:gosec // fixed args, resolved paths
	if err != nil {
		return fmt.Errorf("goose up: %w\n%s", err, out)
	}
	return nil
}

// testConfig is the configuration the stack runs with: the production
// defaults, an ephemeral signing key and no OIDC provider from the
// environment.
func testConfig(key ed25519.PrivateKey) *config.Config {
	return &config.Config{
		LocalAuth: config.LocalAuthConfig{
			JWTSigningKey: key,
			LocalIssuer:   integIssuer,
			EmailCodeTTL:  5 * time.Minute,
		},
		Auth: config.AuthConfig{
			FrontendReturnURL:     "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL:  "http://api.test",
			AccessTokenTTL:        config.DefaultAccessTokenTTL,
			SessionMaxAge:         config.DefaultSessionMaxAge,
			SessionIdleTimeout:    config.DefaultSessionIdleTimeout,
			ClientCredentialsTTL:  config.DefaultClientCredentialsTTL,
			ImpersonationTTL:      config.DefaultImpersonationTTL,
			ImpersonationMax:      config.DefaultImpersonationMax,
			SelfServiceMaxAuthAge: config.DefaultSelfServiceMaxAuthAge,
			DefaultAudience:       config.DefaultAudience,
		},
		Server:    config.ServerConfig{GUIBaseURL: "http://gui.test"},
		Providers: config.ProviderRegistry{},
	}
}

// probe is the body of GET /v1/probe: the AuthContext and Identity the
// authentication middleware installed.
type probe struct {
	Authenticated bool      `json:"authenticated"`
	Anonymous     bool      `json:"anonymous"`
	PrincipalID   int64     `json:"principal_id"`
	ProfileID     int64     `json:"profile_id"`
	SessionID     int64     `json:"session_id"`
	CredentialID  int64     `json:"credential_id"`
	ImpersonatorP int64     `json:"impersonator_principal_id"`
	ACR           int16     `json:"acr"`
	AuthTime      time.Time `json:"auth_time"`
	IdentityACR   int16     `json:"identity_acr"`
	Kind          string    `json:"kind"`
}

func newStack(ctx context.Context, pool *pgxpool.Pool, auditApplied bool) (*stack, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	cfg := testConfig(key)

	queries := db.New(pool)
	coreQueries := coredb.New(pool)

	signer, err := auth.NewSigner(cfg.LocalAuth)
	if err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	verifier, err := auth.NewLocalVerifier(ctx, cfg.LocalAuth)
	if err != nil {
		return nil, fmt.Errorf("verifier: %w", err)
	}
	authn := auth.NewAuthenticator(pool, verifier, cfg.Auth)

	oauth, err := auth.NewOAuth(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("oauth: %w", err)
	}

	cipher, err := fieldcrypto.NewFromEnvOrGenerate(ctx, coreQueries)
	if err != nil {
		return nil, fmt.Errorf("field cipher: %w", err)
	}
	typeResolver, err := types.New(ctx, coreQueries)
	if err != nil {
		return nil, fmt.Errorf("type resolver: %w", err)
	}
	opReg, err := authzapi.NewOperationRegistry(ctx, authzdb.New(pool))
	if err != nil {
		return nil, fmt.Errorf("operation registry: %w", err)
	}
	slugs := []string{
		"natural_person", "organization", "user", "profile", "service_account",
		"legal_entity", "tag", "authz_actor_group", "authz_target_group",
	}
	// The production generator (the owning-party row filter around the
	// grant-table body), exactly as the dev server installs it.
	accessGen, err := localAuthz.AccessFuncGenerator()
	if err != nil {
		return nil, fmt.Errorf("access-function generator: %w", err)
	}
	if err := setup.ApplyFuncs(ctx, pool, accessGen, slugs); err != nil {
		return nil, fmt.Errorf("apply access functions: %w", err)
	}
	az := localAuthz.New(authzdb.New(pool), opReg, pool)
	authn.SetAppGate(az)

	rec := &recorder{}
	members := []observer.MutationObserver{rec}
	if auditApplied {
		members = append(members, auditservice.New(func(tx pgx.Tx) *auditdb.Queries { return auditdb.New(tx) }))
	}
	group := observer.NewObserverGroup(members...)

	coreSvcs := coreservice.New(coreQueries, pool, az, group, cipher, entity.NewResolver(), typeResolver)

	mb := &mailbox{}
	chal := svc.NewChallengeService(queries, mb)
	creds := svc.NewCredentialService(queries, coreQueries, az, group, auth.HashPassword)
	users := svc.NewUserService(queries, coreQueries, az, group, coreSvcs.NaturalPerson, creds)
	sessions := svc.NewSessionService(pool, queries, coreQueries, az, group, signer, chal, svc.SessionConfigFrom(cfg))
	users.WithSelfService(pool, sessions, chal, opReg, cfg.Auth.SelfServiceMaxAuthAge)
	imps := svc.NewImpersonationService(pool, queries, az, group, sessions)
	profiles := svc.NewProfileService(pool, queries, coreQueries, az, group, users, creds, sessions)
	dir := svc.NewDirectoryService(pool)

	authH := authhandlers.New(pool, queries, coreQueries, users, creds, chal, sessions, oauth, mb, cfg.Server.GUIBaseURL)
	oidcH := authhandlers.NewOIDCHandler(pool, queries, coreQueries, oauth, auth.NewOIDCResolver(queries),
		users, creds, chal, sessions, group, cfg)

	requireAuth := auth.NewRequireAuth(authn)
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) { authhandlers.RegisterRoutes(r, authH, oidcH) })
	r.Route("/v1/session", func(r chi.Router) {
		r.Use(requireAuth)
		session.RegisterSessionRoutes(r, session.NewSessionHandler(sessions))
		r.Post("/step-up/oidc/{provider}/start", oidcH.StepUpStart)
	})
	r.Route("/v1/impersonations", func(r chi.Router) {
		r.Use(requireAuth)
		handlers.RegisterImpersonationRoutes(r, handlers.NewImpersonationHandler(imps))
	})
	r.Route("/v1/me", func(r chi.Router) {
		r.Use(requireAuth)
		me.RegisterMeRoutes(r, me.NewMeHandler(users, profiles))
		r.Post("/credentials/oidc/{provider}/start", oidcH.LinkStart)
	})
	r.Route("/v1/principals", func(r chi.Router) {
		r.Use(requireAuth)
		principals.RegisterPrincipalRoutes(r, principals.NewPrincipalsHandler(users))
	})
	partyPolicy := svc.NewPartyPolicyService(pool, coreQueries, az, group, users)
	personal := svc.NewPersonalPartyService(group)
	partiesH := parties.NewPartiesHandler(profiles, partyPolicy)
	r.Route("/v1/parties", func(r chi.Router) {
		r.Use(requireAuth)
		parties.RegisterPartyRoutes(r, partiesH)
	})
	r.Route("/v1/profiles", func(r chi.Router) {
		r.Use(requireAuth)
		parties.RegisterProfileRoutes(r, partiesH)
	})
	// mod-authz's routes with the outside-collaborator gate wired as the
	// trailing argument of NewFromArgs, exactly as the dev server does.
	gate := svc.NewCollaboratorGate()
	authzSvcs := authzservice.NewFromArgs(pool, authzdb.New(pool), coreQueries, az, group, typeResolver, opReg, gate)
	r.Route("/v1/authz", func(r chi.Router) {
		r.Use(requireAuth)
		authzhttpapi.RegisterRoutes(r, authzSvcs)
	})
	// The first-run bootstrap and the OIDC configuration routes, wired as the
	// dev server does: public, the OIDC config handler owning the setup token
	// (active while the bootstrap or the OIDC confirmation is pending) and the
	// operator-administrator check as its AdminChecker.
	boot := svc.NewBootstrapService(pool, az, group, users, creds, sessions, profiles, coreSvcs.Organization)
	onboarding := handlers.NewOIDCConfigHandler(handlers.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  cfg.Providers,
		TokenDisplay: config.TokenDisplayStderr,
		AdminChecker: handlers.NewOperatorAdminChecker(authn, boot),
		Platform:     coreQueries,
	})
	if err := onboarding.RefreshState(ctx); err != nil {
		return nil, fmt.Errorf("oidc config state: %w", err)
	}
	providersH := handlers.NewProvidersHandler(handlers.ProvidersDeps{
		Queries: queries, EnvRegistry: cfg.Providers, OAuth: oauth,
		RedirectBase: cfg.Auth.OAuthRedirectBaseURL, Parties: coreQueries, Confirmer: onboarding,
	})
	r.Route("/v1/oidc-config", func(r chi.Router) { handlers.RegisterOIDCConfigRoutes(r, onboarding, providersH) })
	r.Route("/v1/bootstrap", func(r chi.Router) {
		handlers.RegisterBootstrapRoutes(r, handlers.NewBootstrapHandler(boot, onboarding))
	})
	// /v1/probe reports what the middleware installed. /v1/probe-anon is the
	// same behind ResolveOrAnonymous.
	anon, err := auth.NewAnonymousActor(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("anonymous actor: %w", err)
	}
	r.With(requireAuth).Get("/v1/probe", probeHandler)
	r.With(auth.NewResolveOrAnonymous(authn, anon)).Get("/v1/probe-anon", probeHandler)
	r.With(requireAuth, auth.NewRequireVerifiedEmail()).Get("/v1/probe-verified", probeHandler)

	return &stack{
		pool: pool, cfg: cfg, signer: signer, signKey: key, handler: r, obs: rec, group: group,
		mail: mb, oauth: oauth, az: az, authn: authn, users: users, profiles: profiles, partyPolicy: partyPolicy, personal: personal, gate: gate, creds: creds,
		sessions: sessions, imps: imps, chal: chal, dir: dir, boot: boot, onboarding: onboarding, q: queries, coreQ: coreQueries, opReg: opReg,
		auditApplied: auditApplied,
	}, nil
}

func probeHandler(w http.ResponseWriter, r *http.Request) {
	p := probe{}
	if ac, ok := opctx.Auth(r.Context()); ok {
		p.Authenticated = !ac.Anonymous
		p.Anonymous = ac.Anonymous
		p.PrincipalID, p.ProfileID, p.SessionID, p.CredentialID = ac.PrincipalID, ac.ProfileID, ac.SessionID, ac.CredentialID
		p.ImpersonatorP, p.ACR, p.AuthTime = ac.ImpersonatorPrincipalID, ac.ACR, ac.AuthTime
	}
	if id, ok := auth.IdentityFromContext(r.Context()); ok {
		p.IdentityACR, p.Kind = id.ACR, id.PrincipalKind
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// resp is one recorded HTTP response.
type resp struct {
	status int
	header http.Header
	raw    []byte
}

// do issues a request against the stack. body may be nil, a string (sent
// verbatim) or a value (marshalled as JSON); token, when non-empty, is the
// bearer token.
func (s *stack) do(t *testing.T, method, path, token string, body any) *resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return &resp{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
}

// obj decodes the body as a JSON object.
func (r *resp) obj(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.raw, &m); err != nil {
		t.Fatalf("decode %q: %v", r.raw, err)
	}
	return m
}

// into decodes the body into v.
func (r *resp) into(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.raw, v); err != nil {
		t.Fatalf("decode %q: %v", r.raw, err)
	}
}

// wantStatus fails the test unless the status is want.
func (r *resp) wantStatus(t *testing.T, want int) *resp {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body=%s", r.status, want, r.raw)
	}
	return r
}

// errCode returns error.code and the first detail code of an error envelope.
func (r *resp) errCode(t *testing.T) (code, detail string) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.raw, &e); err != nil {
		t.Fatalf("decode error body %q: %v", r.raw, err)
	}
	if len(e.Error.Details) > 0 {
		detail = e.Error.Details[0].Code
	}
	return e.Error.Code, detail
}

// wantError fails unless the response has the status and the error code (the
// detail code too when detail is non-empty).
func (r *resp) wantError(t *testing.T, status int, code, detail string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body=%s", r.status, status, r.raw)
	}
	gotCode, gotDetail := r.errCode(t)
	if gotCode != code {
		t.Fatalf("error code = %q, want %q; body=%s", gotCode, code, r.raw)
	}
	if detail != "" && gotDetail != detail {
		t.Fatalf("error detail = %q, want %q; body=%s", gotDetail, detail, r.raw)
	}
}

// tokens is the token response of a sign-in.
type tokens struct {
	AccessToken       *string `json:"access_token"`
	TokenType         string  `json:"token_type"`
	ExpiresIn         int64   `json:"expires_in"`
	RefreshToken      *string `json:"refresh_token"`
	SelectionRequired bool    `json:"selection_required"`
	DeviceToken       string  `json:"device_token"`
	Session           *struct {
		UUID          string   `json:"uuid"`
		ACR           int16    `json:"acr"`
		AMR           []string `json:"amr"`
		AuthTime      string   `json:"auth_time"`
		Impersonating bool     `json:"impersonating"`
	} `json:"session"`
	Profile  *profileView  `json:"profile"`
	Profiles []profileView `json:"profiles"`
}

type profileView struct {
	UUID        string `json:"uuid"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
	Party       *struct {
		UUID        string `json:"uuid"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	} `json:"party"`
	Lock *struct {
		Reason string `json:"reason"`
	} `json:"lock"`
}

// tok decodes the token response and requires an access token.
func (r *resp) tok(t *testing.T) tokens {
	t.Helper()
	var tk tokens
	r.into(t, &tk)
	if tk.AccessToken == nil || *tk.AccessToken == "" {
		t.Fatalf("no access token in %s", r.raw)
	}
	return tk
}

func (tk tokens) access() string  { return *tk.AccessToken }
func (tk tokens) refresh() string { return *tk.RefreshToken }

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

var tagCounter struct {
	sync.Mutex
	n int
}

// uniq returns a unique suffix.
func uniq(prefix string) string {
	tagCounter.Lock()
	defer tagCounter.Unlock()
	tagCounter.n++
	return fmt.Sprintf("%s%d", prefix, tagCounter.n)
}

const testPassword = "correct-horse-battery"

// account is a registered user.
type account struct {
	Email    string
	Password string
	tk       tokens
}

// register creates a user through POST /v1/auth/register and returns it with
// its session.
func (s *stack) register(t *testing.T) account {
	t.Helper()
	email := uniq("user") + "@example.test"
	r := s.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": email, "password": testPassword, "given_name": "Test", "family_name": "User",
	}).wantStatus(t, http.StatusCreated)
	return account{Email: email, Password: testPassword, tk: r.tok(t)}
}

// login signs the account in with its password.
func (s *stack) login(t *testing.T, a account) tokens {
	t.Helper()
	return s.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{
		"email": a.Email, "password": a.Password,
	}).wantStatus(t, http.StatusOK).tok(t)
}

// principalID returns the principal entity id of an email identifier.
func (s *stack) principalID(t *testing.T, email string) int64 {
	t.Helper()
	var id int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT user_id FROM mod_users.user_identifiers WHERE kind = 'email' AND value_canon = $1`,
		strings.ToLower(email)).Scan(&id); err != nil {
		t.Fatalf("principal of %s: %v", email, err)
	}
	return id
}

// personalProfileID returns the personal profile entity id of a user.
func (s *stack) personalProfileID(t *testing.T, principalID int64) int64 {
	t.Helper()
	var id int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT entity_id FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, principalID).Scan(&id); err != nil {
		t.Fatalf("personal profile of %d: %v", principalID, err)
	}
	return id
}

// sessionRow reads a session by uuid.
func (s *stack) sessionRow(t *testing.T, sessionUUID string) db.Session {
	t.Helper()
	row, err := s.q.GetSessionByUUID(context.Background(), mustUUID(t, sessionUUID))
	if err != nil {
		t.Fatalf("get session %s: %v", sessionUUID, err)
	}
	return row
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}

// exec runs SQL, failing the test on error.
func (s *stack) exec(t *testing.T, what, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// scanInt64 runs a one-row, one-column query.
func (s *stack) scanInt64(t *testing.T, what, sql string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return v
}

// probe calls GET path and decodes the probe body.
func (s *stack) probe(t *testing.T, path, token string) (int, probe) {
	t.Helper()
	r := s.do(t, http.MethodGet, path, token, nil)
	var p probe
	if r.status == http.StatusOK {
		r.into(t, &p)
	}
	return r.status, p
}

// wantAction fails unless the response is the shared apiresp action-required
// envelope with the status and action code.
func (r *resp) wantAction(t *testing.T, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body=%s", r.status, status, r.raw)
	}
	var env struct {
		Action struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Path    string `json:"path"`
		} `json:"action"`
		Error *struct{} `json:"error"`
	}
	if err := json.Unmarshal(r.raw, &env); err != nil {
		t.Fatalf("decode action body %q: %v", r.raw, err)
	}
	if env.Error != nil {
		t.Fatalf("action response also carries an error envelope: %s", r.raw)
	}
	if env.Action.Code != code || env.Action.Message == "" || !strings.HasPrefix(env.Action.Path, "/") {
		t.Fatalf("action = %+v, want code %q with a message and an application-relative path; body=%s", env.Action, code, r.raw)
	}
}
