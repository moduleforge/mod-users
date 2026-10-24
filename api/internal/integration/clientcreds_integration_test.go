//go:build integration

package integration

// Client credentials: the cid token and the per-request credential liveness.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/opctx"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// serviceAccount is a seeded service account with an issued API key.
type serviceAccount struct {
	ID         int64
	UUID       string
	ProfileID  int64
	ProfileUID string
	Key        svc.IssuedAPIKey
}

// seedServiceAccount inserts a service account owned by a fresh party with its
// personal profile and issues it an API key through the credential service, as
// an operator.
func (s *stack) seedServiceAccount(t *testing.T) serviceAccount {
	t.Helper()
	owner := s.seedParty(t, "Service Org "+uniq("p"))
	var sa serviceAccount
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		sa.ID = s.entityOf(t, tx, "service_account", &owner)
		ctx := context.Background()
		if _, err := tx.Exec(ctx, `INSERT INTO principals (entity_id) VALUES ($1)`, sa.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_accounts (entity_id, label) VALUES ($1, $2)`, sa.ID, uniq("svc")); err != nil {
			return err
		}
		sa.ProfileID = s.entityOf(t, tx, "profile", &owner)
		_, err := tx.Exec(ctx,
			`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at) VALUES ($1, $2, NULL, 'active', now())`,
			sa.ProfileID, sa.ID)
		return err
	})
	if err != nil {
		t.Fatalf("seed service account: %v", err)
	}
	sa.UUID = s.entityUUID(t, sa.ID)
	sa.ProfileUID = s.entityUUID(t, sa.ProfileID)

	op := s.register(t)
	opTok, _ := s.makeOperator(t, op)
	ctx := s.authCtx(t, opTok.access())
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		sa.Key, err = s.creds.IssueAPIKeyInTx(ctx, tx, svc.IssueAPIKeyInput{PrincipalID: sa.ID, Label: "ci"})
		return err
	})
	if err != nil {
		t.Fatalf("issue api key: %v", err)
	}
	return sa
}

// authCtx returns a context carrying the AuthContext the middleware would
// install for the access token.
func (s *stack) authCtx(t *testing.T, token string) context.Context {
	t.Helper()
	status, p := s.probe(t, "/v1/probe", token)
	if status != http.StatusOK {
		t.Fatalf("probe for an auth context = %d", status)
	}
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: p.PrincipalID, ProfileID: p.ProfileID, SessionID: p.SessionID,
		CredentialID: p.CredentialID, ACR: p.ACR, AuthTime: p.AuthTime,
	})
}

func clientCredentials(sa serviceAccount) map[string]string {
	return map[string]string{"grant_type": "client_credentials", "client_id": sa.Key.ClientID, "client_secret": sa.Key.ClientSecret}
}

func TestClientCredentials_IssueAndAuthenticate(t *testing.T) {
	sa := env.seedServiceAccount(t)

	r := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK)
	var tk tokens
	r.into(t, &tk)
	if tk.AccessToken == nil || tk.RefreshToken != nil || tk.Session != nil {
		t.Fatalf("client-credentials response = %s, want an access token with no refresh token and no session", r.raw)
	}
	// expires_in counts down from the token's exp; allow a second or two of drift.
	if want := int64(env.cfg.Auth.ClientCredentialsTTL / time.Second); tk.ExpiresIn < want-2 || tk.ExpiresIn > want {
		t.Errorf("expires_in = %d, want about %d", tk.ExpiresIn, want)
	}

	c := claimsOf(t, tk.access())
	if c["cid"] != sa.Key.CredentialUUID.String() || c["sub"] != sa.UUID || c["prof"] != sa.ProfileUID {
		t.Errorf("claims = %v, want cid %s sub %s prof %s", c, sa.Key.CredentialUUID, sa.UUID, sa.ProfileUID)
	}
	if _, ok := c["sid"]; ok {
		t.Error("a client-credentials token carries sid")
	}
	if amr, _ := c["amr"].([]any); len(amr) != 1 || amr[0] != "swk" {
		t.Errorf("amr = %v, want [swk]", c["amr"])
	}
	if c["aud"] != "mod-users" && !audContains(c["aud"], "mod-users") {
		t.Errorf("aud = %v, want the default audience", c["aud"])
	}
	if d := time.Duration(c["exp"].(float64)-c["iat"].(float64)) * time.Second; d != env.cfg.Auth.ClientCredentialsTTL {
		t.Errorf("exp - iat = %v, want %v", d, env.cfg.Auth.ClientCredentialsTTL)
	}

	// The middleware installs a service-account identity with a credential and
	// no session.
	status, p := env.probe(t, "/v1/probe", tk.access())
	if status != http.StatusOK {
		t.Fatalf("probe = %d", status)
	}
	if p.Kind != "service_account" || p.PrincipalID != sa.ID || p.ProfileID != sa.ProfileID || p.CredentialID == 0 || p.SessionID != 0 {
		t.Errorf("AuthContext = %+v, want service account %d acting as %d via a credential, no session", p, sa.ID, sa.ProfileID)
	}
	if p.ACR < 1 {
		t.Errorf("acr = %d", p.ACR)
	}

	// Session-only routes refuse a client-credentials caller.
	env.do(t, http.MethodGet, "/v1/me", tk.access(), nil).wantStatus(t, http.StatusForbidden)
	env.do(t, http.MethodGet, "/v1/session", tk.access(), nil).wantStatus(t, http.StatusForbidden)
	env.do(t, http.MethodPost, "/v1/session/fork", tk.access(), nil).wantStatus(t, http.StatusForbidden)
}

func TestClientCredentials_BadCredentialsAreIndistinguishable(t *testing.T) {
	sa := env.seedServiceAccount(t)
	wrongSecret := clientCredentials(sa)
	wrongSecret["client_secret"] = "mfs_" + "0000000000000000000000000000000000000000000000000000000000000000"
	unknownID := clientCredentials(sa)
	unknownID["client_id"] = "mfk_00000000000000000000000000000000"

	a := env.do(t, http.MethodPost, "/v1/auth/token", "", wrongSecret)
	b := env.do(t, http.MethodPost, "/v1/auth/token", "", unknownID)
	a.wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	b.wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	if string(a.raw) != string(b.raw) {
		t.Errorf("responses differ:\n%s\n%s", a.raw, b.raw)
	}
	// Missing fields and unsupported grants are 400s.
	env.do(t, http.MethodPost, "/v1/auth/token", "", map[string]string{"grant_type": "client_credentials"}).wantStatus(t, http.StatusBadRequest)
	env.do(t, http.MethodPost, "/v1/auth/token", "", map[string]string{"grant_type": "password"}).wantStatus(t, http.StatusBadRequest)
	env.do(t, http.MethodPost, "/v1/auth/token", "", map[string]string{}).wantStatus(t, http.StatusBadRequest)
}

func TestClientCredentials_RevokedCredentialIsRefusedOnTheNextRequest(t *testing.T) {
	sa := env.seedServiceAccount(t)
	tk := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK).tok(t)
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Fatalf("probe before revocation = %d", status)
	}

	// credential_liveness reads the credential row on every request, so a
	// token minted before the revocation fails after it.
	env.exec(t, "revoke api key", `UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'admin' WHERE id = $1`, sa.Key.CredentialID)
	env.do(t, http.MethodGet, "/v1/probe", tk.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	// A new token cannot be minted from the revoked key.
	env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
}

func TestClientCredentials_ExpiredCredentialIsRefused(t *testing.T) {
	sa := env.seedServiceAccount(t)
	tk := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK).tok(t)
	env.exec(t, "expire api key", `UPDATE mod_users.credentials SET expires_at = now() - interval '1 second' WHERE id = $1`, sa.Key.CredentialID)
	env.do(t, http.MethodGet, "/v1/probe", tk.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
}

func TestClientCredentials_DisabledServiceAccountIsRefused(t *testing.T) {
	sa := env.seedServiceAccount(t)
	tk := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK).tok(t)

	admin := env.register(t)
	opTok, _ := env.makeOperator(t, admin)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/principals/"+sa.UUID+"/disable", opTok.access(), map[string]string{"reason": "rotated"}).wantStatus(t, http.StatusOK)

	if r := env.do(t, http.MethodGet, "/v1/probe", tk.access(), nil); r.status != http.StatusUnauthorized {
		t.Fatalf("token of a disabled service account = %d %s", r.status, r.raw)
	}
	if r := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)); r.status != http.StatusUnauthorized {
		t.Fatalf("client credentials of a disabled service account = %d %s", r.status, r.raw)
	}
	env.do(t, http.MethodPost, "/v1/principals/"+sa.UUID+"/enable", opTok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK)
}

func TestClientCredentials_UnregisteredAppAudienceIsRefused(t *testing.T) {
	sa := env.seedServiceAccount(t)
	body := clientCredentials(sa)
	body["app"] = "no-such-app"
	if r := env.do(t, http.MethodPost, "/v1/auth/token", "", body); r.status == http.StatusOK {
		t.Fatalf("a token was issued for an unregistered app audience: %s", r.raw)
	}
}

func TestClientCredentials_CannotLinkOrStepUpAnOIDCCredential(t *testing.T) {
	sa := env.seedServiceAccount(t)
	tk := env.do(t, http.MethodPost, "/v1/auth/token", "", clientCredentials(sa)).wantStatus(t, http.StatusOK).tok(t)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone})

	// A service principal has no session to link to or step up.
	env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start", tk.access(), nil).wantStatus(t, http.StatusForbidden)
	env.do(t, http.MethodPost, "/v1/session/step-up/oidc/"+provider+"/start", tk.access(), nil).wantStatus(t, http.StatusForbidden)

	// A forged link callback that names the service account as the principal
	// of a real user's session is refused, and nothing is linked to it.
	a := env.register(t)
	id := idpIdentity(uniq("sa")+"@example.test", true)
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, a.tk.Session.UUID, sa.ID))
	env.wantRedirectError(t, provider, "authentication_failed")
	if n := env.count(t, "oidc on the service account", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'oidc'`, sa.ID); n != 0 {
		t.Errorf("the service account gained an oidc credential")
	}
}
