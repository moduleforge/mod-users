//go:build integration

package integration

// Consumer-requested additions (decisions U23 and U24): LinkOIDCInTx and
// ReadPrincipalSummaries.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// linkOIDC links (issuer, subject) to the principal through LinkOIDCInTx, in
// its own transaction, acting as the principal's own profile (what a
// provisioning caller such as app-mfmanager supplies).
func (s *stack) linkOIDC(t *testing.T, principalID int64, provider, issuer, subject, email string) (svc.LinkedCredential, error) {
	t.Helper()
	var linked svc.LinkedCredential
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		ctx, ok, err := svc.UnauthenticatedActingContext(context.Background(), tx, principalID)
		if err != nil || !ok {
			t.Fatalf("acting context: ok=%v err=%v", ok, err)
		}
		linked, err = s.creds.LinkOIDCInTx(ctx, tx, svc.LinkOIDCInput{
			PrincipalID: principalID, ProviderID: provider, Issuer: issuer, Subject: subject,
			EmailAtLink: email, Label: provider,
		})
		return err
	})
	return linked, err
}

func TestLinkOIDCInTx_CreatesACredentialThatLogsInOnTheKnownPath(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone})
	subject := uniq("owner-sub-")

	// The email recorded with the link is somebody else's: it must not matter.
	stranger := env.register(t)
	linked, err := env.linkOIDC(t, uid, provider, idpIssuer, subject, stranger.Email)
	if err != nil {
		t.Fatalf("LinkOIDCInTx: %v", err)
	}
	if n := env.count(t, "credential", `SELECT count(*) FROM mod_users.credentials WHERE id = $1 AND principal_id = $2 AND kind = 'oidc' AND revoked_at IS NULL`,
		linked.CredentialID, uid); n != 1 {
		t.Fatalf("linked credential rows = %d, want 1", n)
	}

	// The identity now signs in on the known (issuer, subject) path as the
	// linked principal, even though the provider creates no accounts.
	env.idpReturns(t, auth.ExternalIdentity{Issuer: idpIssuer, Subject: subject, Email: "unrelated@example.test", EmailVerified: true}, auth.StatePayload{})
	tk := env.signIn(t, provider)
	if _, p := env.probe(t, "/v1/probe", tk.access()); p.PrincipalID != uid {
		t.Fatalf("known-path sign-in principal = %d, want %d", p.PrincipalID, uid)
	}
	if n := env.count(t, "stranger", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'oidc'`,
		env.principalID(t, stranger.Email)); n != 0 {
		t.Errorf("the email's owner gained an oidc credential: LinkOIDCInTx linked by email")
	}
}

func TestLinkOIDCInTx_ASecondActiveLinkOfThePairIsAConflict(t *testing.T) {
	a := env.register(t)
	b := env.register(t)
	provider := uniq("prov")
	subject := uniq("pair-sub-")

	if _, err := env.linkOIDC(t, env.principalID(t, a.Email), provider, idpIssuer, subject, a.Email); err != nil {
		t.Fatalf("first link: %v", err)
	}
	// The same pair for another principal, and for the same principal again.
	for name, owner := range map[string]account{"another principal": b, "the same principal": a} {
		_, err := env.linkOIDC(t, env.principalID(t, owner.Email), provider, idpIssuer, subject, "")
		if err == nil || !errorsIs(err, svc.ErrOIDCIdentityInUse) {
			t.Fatalf("%s: err = %v, want ErrOIDCIdentityInUse", name, err)
		}
		// Rendered by the shared writer it is 409 users.oidc_identity_in_use.
		rec := httptest.NewRecorder()
		apiresp.WriteError(rec, httptest.NewRequest(http.MethodPost, "/", nil), err)
		r := &resp{status: rec.Code, raw: rec.Body.Bytes()}
		r.wantError(t, http.StatusConflict, "conflict", "users.oidc_identity_in_use")
	}
	if n := env.count(t, "single active link", `SELECT count(*) FROM mod_users.cred_oidc o JOIN mod_users.credentials c ON c.id = o.credential_id
		WHERE o.issuer = $1 AND o.subject = $2 AND c.revoked_at IS NULL`, idpIssuer, subject); n != 1 {
		t.Errorf("active links of the pair = %d, want 1", n)
	}

	// A revoked link frees the pair.
	var credID int64
	if err := env.pool.QueryRow(context.Background(),
		`SELECT credential_id FROM mod_users.cred_oidc WHERE issuer = $1 AND subject = $2`, idpIssuer, subject).Scan(&credID); err != nil {
		t.Fatal(err)
	}
	env.exec(t, "revoke link", `UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'user' WHERE id = $1`, credID)
	if _, err := env.linkOIDC(t, env.principalID(t, b.Email), provider, idpIssuer, subject, ""); err != nil {
		t.Errorf("link after the first was revoked: %v", err)
	}
}

func TestLinkOIDCInTx_RefusesPrincipalsThatCannotHoldOIDCCredentials(t *testing.T) {
	g := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).wantStatus(t, http.StatusCreated)
	var guest tokens
	g.into(t, &guest)
	_, p := env.probe(t, "/v1/probe", guest.access())
	if _, err := env.linkOIDC(t, p.PrincipalID, uniq("prov"), idpIssuer, uniq("guest-sub-"), ""); err == nil {
		t.Error("a guest was linked to an oidc credential")
	}
}

func TestReadPrincipalSummaries_SeesTheCallersTransactionOnASingleConnection(t *testing.T) {
	dsn := env.pool.Config().ConnString()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()

	committed := []int64{env.principalID(t, env.register(t).Email), env.principalID(t, env.register(t).Email)}
	sa := env.seedServiceAccount(t)
	committed = append(committed, sa.ID)

	// A deadline turns a second connection request (a deadlock on a pool of
	// one) into a failure instead of a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := one.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// A row written earlier in the same transaction (not committed, so no other
	// connection can see it).
	var userID int64
	if err := tx.QueryRow(ctx, `INSERT INTO entities (fundamental_type_id) SELECT id FROM types WHERE slug = 'user' RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO principals (entity_id) VALUES ($1)`,
		`INSERT INTO mod_users.users (entity_id, is_guest) VALUES ($1, false)`,
		`INSERT INTO mod_users.user_identifiers (user_id, kind, value_canon, verified_at, is_primary) VALUES ($1, 'email', 'in-tx@example.test', now(), true)`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	ids := append(append([]int64(nil), committed...), userID)
	got, err := svc.ReadPrincipalSummaries(ctx, tx, ids)
	if err != nil {
		t.Fatalf("ReadPrincipalSummaries on the open transaction: %v", err)
	}
	in, ok := got[userID]
	if !ok || in.PrimaryEmail != "in-tx@example.test" || in.DisplayName != "in-tx@example.test" || in.Kind != svc.PrincipalKindUser {
		t.Fatalf("summary of the in-transaction user = %+v (present: %v)", in, ok)
	}
	// The pool-based directory cannot see that row.
	if _, visible := mustSummaries(t, ids)[userID]; visible {
		t.Fatal("the directory saw an uncommitted row")
	}
	_ = tx.Rollback(ctx)

	// For committed ids the result equals DirectoryService.PrincipalSummaries.
	tx2, err := one.Begin(ctx)
	if err != nil {
		t.Fatalf("begin on the single connection after rollback: %v", err)
	}
	defer func() { _ = tx2.Rollback(context.Background()) }()
	viaTx, err := svc.ReadPrincipalSummaries(ctx, tx2, committed)
	if err != nil {
		t.Fatal(err)
	}
	viaDir := mustSummaries(t, committed)
	if len(viaTx) != len(committed) || !reflect.DeepEqual(viaTx, viaDir) {
		t.Fatalf("summaries differ:\n tx  %+v\n dir %+v", viaTx, viaDir)
	}
	if viaTx[sa.ID].Kind != svc.PrincipalKindServiceAccount || viaTx[sa.ID].DisplayName == "" {
		t.Errorf("service account summary = %+v", viaTx[sa.ID])
	}
	// Unknown ids are absent.
	none, err := svc.ReadPrincipalSummaries(ctx, tx2, []int64{1 << 50})
	if err != nil || len(none) != 0 {
		t.Errorf("unknown id: %v %v", none, err)
	}
}

func mustSummaries(t *testing.T, ids []int64) map[int64]svc.PrincipalSummary {
	t.Helper()
	m, err := env.dir.PrincipalSummaries(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
