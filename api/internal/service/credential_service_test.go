package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

type fakeAuthorizer struct {
	err    error
	called []string
}

func (a *fakeAuthorizer) Authorize(_ context.Context, op string, target *int64) error {
	if target != nil {
		a.called = append(a.called, op)
	}
	return a.err
}

func actorCtx(profileID int64) context.Context {
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, ProfileID: profileID})
}

func newTestCredentialService(q *fakeUsersQ, cq *fakeCoreQ, az *fakeAuthorizer, rec *recordingObserver) *CredentialService {
	s := NewCredentialService(q, cq, az, observer.NewObserverGroup(rec), func(p string) (string, error) { return "hash:" + p, nil })
	s.newQ = func(pgx.Tx) usersdb.Querier { return q }
	s.newCoreQ = func(pgx.Tx) coredb.Querier { return cq }
	return s
}

func credRow(id, principal int64, kind string) usersdb.Credential {
	return usersdb.Credential{ID: id, Uuid: uuid.New(), PrincipalID: principal, Kind: kind}
}

func TestLinkOIDCInTx(t *testing.T) {
	t.Parallel()

	newQ := func() *fakeUsersQ {
		return &fakeUsersQ{
			createCredential: func(a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
				if a.Kind != KindOIDC {
					t.Errorf("kind = %q, want oidc", a.Kind)
				}
				if !a.CreatedByProfileID.Valid || a.CreatedByProfileID.Int64 != 77 {
					t.Errorf("created_by = %+v, want the acting profile 77", a.CreatedByProfileID)
				}
				return credRow(9, a.PrincipalID, a.Kind), nil
			},
		}
	}

	t.Run("creates credential and observes create", func(t *testing.T) {
		t.Parallel()
		q := newQ()
		var got usersdb.CreateOIDCCredentialParams
		q.createOIDC = func(a usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error) {
			got = a
			return usersdb.CredOidc{CredentialID: a.CredentialID}, nil
		}
		rec := &recordingObserver{}
		s := newTestCredentialService(q, nil, nil, rec)

		res, err := s.LinkOIDCInTx(actorCtx(77), nil, LinkOIDCInput{
			PrincipalID: 5, ProviderID: "authelia", Issuer: "https://idp", Subject: "sub-1",
		})
		if err != nil {
			t.Fatalf("LinkOIDCInTx: %v", err)
		}
		if res.CredentialID != 9 || res.CredentialUUID == uuid.Nil {
			t.Errorf("result = %+v", res)
		}
		if got.EmailAtLink.Valid {
			t.Error("an empty EmailAtLink must store NULL")
		}
		want := []observation{{Op: "create", Resource: "credential", Target: 5, Actor: 77}}
		if len(rec.seen) != 1 || rec.seen[0] != want[0] {
			t.Errorf("observations = %+v, want %+v", rec.seen, want)
		}
	})

	t.Run("stores email at link", func(t *testing.T) {
		t.Parallel()
		q := newQ()
		var got usersdb.CreateOIDCCredentialParams
		q.createOIDC = func(a usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error) {
			got = a
			return usersdb.CredOidc{}, nil
		}
		s := newTestCredentialService(q, nil, nil, &recordingObserver{})
		if _, err := s.LinkOIDCInTx(actorCtx(77), nil, LinkOIDCInput{
			PrincipalID: 5, ProviderID: "p", Issuer: "i", Subject: "s", EmailAtLink: "a@b.example",
		}); err != nil {
			t.Fatal(err)
		}
		if got.EmailAtLink != (pgtype.Text{String: "a@b.example", Valid: true}) {
			t.Errorf("EmailAtLink = %+v", got.EmailAtLink)
		}
	})

	t.Run("active identity clash maps to ErrOIDCIdentityInUse", func(t *testing.T) {
		t.Parallel()
		q := newQ()
		q.createOIDC = func(usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error) {
			return usersdb.CredOidc{}, &pgconn.PgError{Code: "23505", ConstraintName: OIDCActiveIssuerSubjectIndex}
		}
		rec := &recordingObserver{}
		s := newTestCredentialService(q, nil, nil, rec)
		_, err := s.LinkOIDCInTx(actorCtx(77), nil, LinkOIDCInput{PrincipalID: 5, ProviderID: "p", Issuer: "i", Subject: "s"})
		if !errors.Is(err, ErrOIDCIdentityInUse) {
			t.Fatalf("got %v, want ErrOIDCIdentityInUse", err)
		}
		if len(rec.seen) != 0 {
			t.Errorf("a failed link must not observe: %+v", rec.seen)
		}
	})

	t.Run("an MF102 refusal is an internal error", func(t *testing.T) {
		t.Parallel()
		q := newQ()
		q.createCredential = func(usersdb.CreateCredentialParams) (usersdb.Credential, error) {
			return usersdb.Credential{}, &pgconn.PgError{Code: CredentialInvariantSQLState}
		}
		s := newTestCredentialService(q, nil, nil, &recordingObserver{})
		_, err := s.LinkOIDCInTx(actorCtx(77), nil, LinkOIDCInput{PrincipalID: 5, ProviderID: "p", Issuer: "i", Subject: "s"})
		if err == nil || !IsCredentialInvariantError(err) {
			t.Fatalf("got %v, want the wrapped MF102", err)
		}
		for _, sentinel := range []error{apiresp.ErrConflict, apiresp.ErrInvalidInput, apiresp.ErrForbidden} {
			if errors.Is(err, sentinel) {
				t.Errorf("MF102 must not map to %v", sentinel)
			}
		}
	})

	t.Run("requires provider, issuer and subject", func(t *testing.T) {
		t.Parallel()
		s := newTestCredentialService(newQ(), nil, nil, &recordingObserver{})
		for _, in := range []LinkOIDCInput{
			{PrincipalID: 5, Issuer: "i", Subject: "s"},
			{PrincipalID: 5, ProviderID: "p", Subject: "s"},
			{PrincipalID: 5, ProviderID: "p", Issuer: "i"},
		} {
			if _, err := s.LinkOIDCInTx(actorCtx(77), nil, in); !errors.Is(err, apiresp.ErrInvalidInput) {
				t.Errorf("%+v: got %v, want invalid input", in, err)
			}
		}
	})
}

func TestIssueAPIKeyInTx(t *testing.T) {
	t.Parallel()

	t.Run("denied before any lookup", func(t *testing.T) {
		t.Parallel()
		az := &fakeAuthorizer{err: apiresp.ErrForbidden}
		s := newTestCredentialService(&fakeUsersQ{}, &fakeCoreQ{}, az, &recordingObserver{})
		_, err := s.IssueAPIKeyInTx(actorCtx(1), nil, IssueAPIKeyInput{PrincipalID: 3})
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("got %v, want forbidden", err)
		}
		if len(az.called) != 1 || az.called[0] != "credentials" {
			t.Errorf("authorized ops = %v, want [credentials]", az.called)
		}
	})

	t.Run("no authorizer fails closed", func(t *testing.T) {
		t.Parallel()
		s := NewCredentialService(&fakeUsersQ{}, &fakeCoreQ{}, nil, nil, nil)
		if _, err := s.IssueAPIKeyInTx(actorCtx(1), nil, IssueAPIKeyInput{PrincipalID: 3}); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("got %v, want forbidden", err)
		}
	})

	t.Run("only service accounts", func(t *testing.T) {
		t.Parallel()
		cq := &fakeCoreQ{getServiceAccount: func(int64) (coredb.ServiceAccount, error) { return coredb.ServiceAccount{}, pgx.ErrNoRows }}
		s := newTestCredentialService(&fakeUsersQ{}, cq, &fakeAuthorizer{}, &recordingObserver{})
		if _, err := s.IssueAPIKeyInTx(actorCtx(1), nil, IssueAPIKeyInput{PrincipalID: 3}); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Fatalf("got %v, want invalid input", err)
		}
	})

	t.Run("issues key whose secret verifies against the stored hash", func(t *testing.T) {
		t.Parallel()
		cq := &fakeCoreQ{getServiceAccount: func(int64) (coredb.ServiceAccount, error) { return coredb.ServiceAccount{}, nil }}
		var stored string
		q := &fakeUsersQ{
			createCredential: func(a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
				if a.Kind != KindAPIKey {
					t.Errorf("kind = %q", a.Kind)
				}
				return credRow(4, a.PrincipalID, a.Kind), nil
			},
		}
		rec := &recordingObserver{}
		s := newTestCredentialService(q, cq, &fakeAuthorizer{}, rec)
		s.newQ = func(pgx.Tx) usersdb.Querier {
			return &apiKeyStore{fakeUsersQ: q, onCreate: func(a usersdb.CreateAPIKeyCredentialParams) { stored = a.SecretHash }}
		}

		key, err := s.IssueAPIKeyInTx(actorCtx(1), nil, IssueAPIKeyInput{PrincipalID: 3, Label: "ci"})
		if err != nil {
			t.Fatalf("IssueAPIKeyInTx: %v", err)
		}
		if key.CredentialID != 4 || key.ClientID == "" || key.ClientSecret == "" {
			t.Fatalf("key = %+v", key)
		}
		if stored == key.ClientSecret {
			t.Fatal("the secret must be stored hashed")
		}
		if !TokenMatchesHash(key.ClientSecret, stored) {
			t.Error("stored hash does not verify the issued secret")
		}
		if len(rec.seen) != 1 || rec.seen[0].Op != "create" || rec.seen[0].Resource != "credential" || rec.seen[0].Target != 3 {
			t.Errorf("observations = %+v", rec.seen)
		}
	})

	t.Run("expiry must be in the future", func(t *testing.T) {
		t.Parallel()
		cq := &fakeCoreQ{getServiceAccount: func(int64) (coredb.ServiceAccount, error) { return coredb.ServiceAccount{}, nil }}
		s := newTestCredentialService(&fakeUsersQ{}, cq, &fakeAuthorizer{}, &recordingObserver{})
		past := time.Now().Add(-time.Hour)
		if _, err := s.IssueAPIKeyInTx(actorCtx(1), nil, IssueAPIKeyInput{PrincipalID: 3, ExpiresAt: &past}); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Fatalf("got %v, want invalid input", err)
		}
	})
}

// apiKeyStore intercepts CreateAPIKeyCredential on top of fakeUsersQ.
type apiKeyStore struct {
	*fakeUsersQ
	onCreate func(usersdb.CreateAPIKeyCredentialParams)
}

func (a *apiKeyStore) CreateAPIKeyCredential(_ context.Context, p usersdb.CreateAPIKeyCredentialParams) (usersdb.CredApiKey, error) {
	a.onCreate(p)
	return usersdb.CredApiKey{CredentialID: p.CredentialID}, nil
}

func TestRevokeInTx(t *testing.T) {
	t.Parallel()

	t.Run("revokes credential and its sessions with the right ops", func(t *testing.T) {
		t.Parallel()
		var reason string
		q := &fakeUsersQ{
			getCredentialByID: func(id int64) (usersdb.Credential, error) { return credRow(id, 5, KindSSHKey), nil },
			revokeCredential: func(a usersdb.RevokeCredentialParams) (usersdb.Credential, error) {
				reason = a.RevokeReason.String
				c := credRow(a.ID, 5, KindSSHKey)
				c.RevokeReason = a.RevokeReason
				return c, nil
			},
			revokeSessions: func(int64) ([]usersdb.Session, error) {
				return []usersdb.Session{{ID: 1, Uuid: uuid.New(), PrincipalID: 5}, {ID: 2, Uuid: uuid.New(), PrincipalID: 5}}, nil
			},
		}
		rec := &recordingObserver{}
		s := newTestCredentialService(q, nil, nil, rec)

		if err := s.RevokeInTx(actorCtx(77), nil, 8, RevokeReasonAdmin); err != nil {
			t.Fatalf("RevokeInTx: %v", err)
		}
		if reason != "admin" {
			t.Errorf("reason = %q", reason)
		}
		want := []observation{
			{Op: "revoke", Resource: "credential", Target: 5, Actor: 77},
			{Op: "revoke", Resource: "session", Target: 5, Actor: 77},
			{Op: "revoke", Resource: "session", Target: 5, Actor: 77},
		}
		if len(rec.seen) != len(want) {
			t.Fatalf("observations = %+v", rec.seen)
		}
		for i := range want {
			if rec.seen[i] != want[i] {
				t.Errorf("observation %d = %+v, want %+v", i, rec.seen[i], want[i])
			}
		}
	})

	t.Run("already revoked is a no-op", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		q := &fakeUsersQ{getCredentialByID: func(id int64) (usersdb.Credential, error) {
			c := credRow(id, 5, KindSSHKey)
			c.RevokedAt = &now
			return c, nil
		}}
		rec := &recordingObserver{}
		s := newTestCredentialService(q, nil, nil, rec)
		if err := s.RevokeInTx(actorCtx(77), nil, 8, RevokeReasonUser); err != nil {
			t.Fatal(err)
		}
		if len(q.revokedCredentials) != 0 || len(rec.seen) != 0 {
			t.Errorf("no-op wrote: revoked=%v obs=%+v", q.revokedCredentials, rec.seen)
		}
	})

	t.Run("unknown credential and bad reason", func(t *testing.T) {
		t.Parallel()
		q := &fakeUsersQ{getCredentialByID: func(int64) (usersdb.Credential, error) { return usersdb.Credential{}, pgx.ErrNoRows }}
		s := newTestCredentialService(q, nil, nil, &recordingObserver{})
		if err := s.RevokeInTx(actorCtx(77), nil, 8, RevokeReasonUser); !errors.Is(err, apiresp.ErrNotFound) {
			t.Errorf("unknown credential: got %v, want not found", err)
		}
		if err := s.RevokeInTx(actorCtx(77), nil, 8, "assume"); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("bad reason: got %v, want invalid input", err)
		}
	})
}

func TestRemoveInTx_LastInteractiveCredential(t *testing.T) {
	t.Parallel()

	build := func(kind string, class string, remaining int64) (*CredentialService, *fakeUsersQ) {
		q := &fakeUsersQ{
			getUser:           func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil },
			getCredentialByID: func(id int64) (usersdb.Credential, error) { return credRow(id, 5, kind), nil },
			getCredentialKind: func(k string) (usersdb.CredentialKind, error) {
				return usersdb.CredentialKind{Kind: k, Class: class}, nil
			},
			countInteractive: func(int64) (int64, error) { return remaining, nil },
			revokeCredential: func(a usersdb.RevokeCredentialParams) (usersdb.Credential, error) {
				return credRow(a.ID, 5, kind), nil
			},
			revokeSessions: func(int64) ([]usersdb.Session, error) { return nil, nil },
		}
		return newTestCredentialService(q, nil, nil, &recordingObserver{}), q
	}

	t.Run("refuses the last interactive credential", func(t *testing.T) {
		t.Parallel()
		s, q := build(KindPassword, "interactive", 1)
		if err := s.RemoveInTx(actorCtx(77), nil, 5, 8, RevokeReasonUser); !errors.Is(err, ErrLastCredential) {
			t.Fatalf("got %v, want ErrLastCredential", err)
		}
		if len(q.revokedCredentials) != 0 {
			t.Error("a refused removal revoked the credential")
		}
		if len(q.lockedUsers) != 1 || q.lockedUsers[0] != 5 {
			t.Errorf("user row lock = %v, want [5]", q.lockedUsers)
		}
	})

	t.Run("allows removal when another remains", func(t *testing.T) {
		t.Parallel()
		s, q := build(KindPassword, "interactive", 2)
		if err := s.RemoveInTx(actorCtx(77), nil, 5, 8, RevokeReasonUser); err != nil {
			t.Fatal(err)
		}
		if len(q.revokedCredentials) != 1 {
			t.Errorf("revoked = %v", q.revokedCredentials)
		}
	})

	t.Run("non-interactive credentials are not counted", func(t *testing.T) {
		t.Parallel()
		s, q := build(KindSSHKey, "non_interactive", 0)
		if err := s.RemoveInTx(actorCtx(77), nil, 5, 8, RevokeReasonUser); err != nil {
			t.Fatal(err)
		}
		if len(q.revokedCredentials) != 1 {
			t.Errorf("revoked = %v", q.revokedCredentials)
		}
	})

	t.Run("another principal's credential is masked as not found", func(t *testing.T) {
		t.Parallel()
		s, _ := build(KindPassword, "interactive", 5)
		if err := s.RemoveInTx(actorCtx(77), nil, 6, 8, RevokeReasonUser); !errors.Is(err, apiresp.ErrNotFound) {
			t.Fatalf("got %v, want not found", err)
		}
	})
}

func TestCreatePasswordInTx_Validation(t *testing.T) {
	t.Parallel()
	s := newTestCredentialService(&fakeUsersQ{}, nil, nil, &recordingObserver{})
	for name, pw := range map[string]string{"short": "short", "long": string(make([]byte, maxPasswordBytes+1))} {
		if _, err := s.CreatePasswordInTx(actorCtx(1), nil, CreatePasswordInput{PrincipalID: 1, Password: pw}); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s: got %v, want invalid input", name, err)
		}
	}
}

func TestCreateSSHKeyInTx_Validation(t *testing.T) {
	t.Parallel()
	s := newTestCredentialService(&fakeUsersQ{}, nil, nil, &recordingObserver{})
	_, err := s.CreateSSHKeyInTx(actorCtx(1), nil, CreateSSHKeyInput{PrincipalID: 1, PublicKey: "not a key"})
	if !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Fatalf("got %v, want invalid input", err)
	}
	if fe := ValidateSSHKeyShape("not a key", nil); len(fe) != 1 || fe[0].Code != "users.ssh_key_invalid" {
		t.Errorf("ValidateSSHKeyShape = %+v", fe)
	}
}
