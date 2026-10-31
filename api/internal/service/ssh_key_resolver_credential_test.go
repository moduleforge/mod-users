package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/ssh"

	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

func newSSHKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

type fakeSSHQ struct {
	usersdb.Querier
	row          func(fingerprint string) (usersdb.GetActiveSSHKeyByFingerprintRow, error)
	availability string
	writes       int
}

func (f *fakeSSHQ) GetActiveSSHKeyByFingerprint(_ context.Context, fp string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
	return f.row(fp)
}

func (f *fakeSSHQ) CredentialProfileAvailability(context.Context, usersdb.CredentialProfileAvailabilityParams) (string, error) {
	return f.availability, nil
}

func (f *fakeSSHQ) GetCredentialKind(context.Context, string) (usersdb.CredentialKind, error) {
	return usersdb.CredentialKind{Kind: "ssh_key", BaseAcr: 1}, nil
}

func activeKeyRow(key ssh.PublicKey, principal int64) usersdb.GetActiveSSHKeyByFingerprintRow {
	return usersdb.GetActiveSSHKeyByFingerprintRow{
		Credential:        usersdb.Credential{ID: 9, Uuid: uuid.New(), PrincipalID: principal, Kind: KindSSHKey},
		KeyType:           key.Type(),
		PublicKey:         sshkey.Canonical(key),
		FingerprintSha256: sshkey.Fingerprint(key),
	}
}

func TestSSHKeyResolver_ResolveCredential(t *testing.T) {
	t.Parallel()
	key := newSSHKey(t)
	liveEntity := func(int64) (coredb.GetEntityByIDRow, error) {
		return coredb.GetEntityByIDRow{ID: 5, Uuid: uuid.New()}, nil
	}
	livePrincipal := func(id int64) (coredb.Principal, error) { return coredb.Principal{EntityID: id}, nil }

	t.Run("resolves an active key", func(t *testing.T) {
		t.Parallel()
		q := &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) { return activeKeyRow(key, 5), nil }}
		r := NewSSHKeyResolver(q, &fakeCoreQ{getEntity: liveEntity, getPrincipal: livePrincipal})
		cred, err := r.ResolveCredential(context.Background(), key)
		if err != nil {
			t.Fatalf("ResolveCredential: %v", err)
		}
		if cred.CredentialID != 9 || cred.PrincipalID != 5 || cred.PrincipalUUID == uuid.Nil || cred.ConfinedToPartyID != 0 {
			t.Errorf("credential = %+v", cred)
		}
	})

	t.Run("every miss is the same sentinel", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		archived := now
		cases := map[string]struct {
			key ssh.PublicKey
			q   *fakeSSHQ
			cq  *fakeCoreQ
		}{
			"nil key": {nil, &fakeSSHQ{}, &fakeCoreQ{}},
			"unregistered": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
				return usersdb.GetActiveSSHKeyByFingerprintRow{}, pgx.ErrNoRows
			}}, &fakeCoreQ{}},
			"stored text mismatch": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
				row := activeKeyRow(key, 5)
				row.PublicKey = "ssh-ed25519 AAAA"
				return row, nil
			}}, &fakeCoreQ{}},
			"revoked": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
				row := activeKeyRow(key, 5)
				row.Credential.RevokedAt = &now
				return row, nil
			}}, &fakeCoreQ{}},
			"expired": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
				row := activeKeyRow(key, 5)
				past := now.Add(-time.Minute)
				row.Credential.ExpiresAt = &past
				return row, nil
			}}, &fakeCoreQ{}},
			"archived principal": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) { return activeKeyRow(key, 5), nil }},
				&fakeCoreQ{getEntity: func(int64) (coredb.GetEntityByIDRow, error) {
					return coredb.GetEntityByIDRow{ArchivedAt: &archived}, nil
				}}},
			"disabled principal": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) { return activeKeyRow(key, 5), nil }},
				&fakeCoreQ{getEntity: liveEntity, getPrincipal: func(id int64) (coredb.Principal, error) {
					return coredb.Principal{EntityID: id, DisabledAt: &archived}, nil
				}}},
			"missing entity": {key, &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) { return activeKeyRow(key, 5), nil }},
				&fakeCoreQ{getEntity: func(int64) (coredb.GetEntityByIDRow, error) { return coredb.GetEntityByIDRow{}, pgx.ErrNoRows }}},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				r := NewSSHKeyResolver(tc.q, tc.cq)
				if _, err := r.ResolveCredential(context.Background(), tc.key); !errors.Is(err, ErrUnknownSSHKey) {
					t.Errorf("got %v, want ErrUnknownSSHKey", err)
				}
			})
		}
	})

	t.Run("a database error is not ErrUnknownSSHKey", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("connection reset")
		q := &fakeSSHQ{row: func(string) (usersdb.GetActiveSSHKeyByFingerprintRow, error) {
			return usersdb.GetActiveSSHKeyByFingerprintRow{}, dbErr
		}}
		_, err := NewSSHKeyResolver(q, &fakeCoreQ{}).ResolveCredential(context.Background(), key)
		if err == nil || errors.Is(err, ErrUnknownSSHKey) || !errors.Is(err, dbErr) {
			t.Fatalf("got %v, want a wrapped transient error", err)
		}
	})
}

func TestSSHKeyResolver_AuthContextForOwner(t *testing.T) {
	t.Parallel()
	profile := func(int64) (coredb.Profile, error) { return coredb.Profile{EntityID: 55, HolderID: 5}, nil }

	t.Run("personal profile context", func(t *testing.T) {
		t.Parallel()
		fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		r := NewSSHKeyResolver(&fakeSSHQ{availability: "available"}, &fakeCoreQ{getPersonal: profile})
		r.now = func() time.Time { return fixed }
		owner := int64(99)
		ac, err := r.AuthContextForOwner(context.Background(), SSHCredential{CredentialID: 9, PrincipalID: 5}, &owner)
		if err != nil {
			t.Fatal(err)
		}
		if ac.PrincipalID != 5 || ac.ProfileID != 55 || ac.CredentialID != 9 || ac.SessionID != 0 || ac.ACR != 1 || !ac.AuthTime.Equal(fixed) {
			t.Errorf("AuthContext = %+v", ac)
		}
	})

	t.Run("an active party profile is used when available, refused with the party uuid otherwise", func(t *testing.T) {
		t.Parallel()
		party := uuid.New()
		cq := &fakeCoreQ{
			getPersonal: profile,
			getEntity:   func(int64) (coredb.GetEntityByIDRow, error) { return coredb.GetEntityByIDRow{Uuid: party}, nil },
			listByHolder: func(int64) ([]coredb.Profile, error) {
				return []coredb.Profile{
					{EntityID: 55, HolderID: 5, Status: "active"},
					{EntityID: 66, HolderID: 5, PartyID: pgtype.Int8{Int64: 99, Valid: true}, Status: "suspended"},
					{EntityID: 77, HolderID: 5, PartyID: pgtype.Int8{Int64: 98, Valid: true}, Status: "active"},
				}, nil
			},
		}
		cred := SSHCredential{CredentialID: 9, PrincipalID: 5}
		owner := int64(98)
		ac, err := NewSSHKeyResolver(&fakeSSHQ{availability: "available"}, cq).AuthContextForOwner(context.Background(), cred, &owner)
		if err != nil || ac.ProfileID != 77 {
			t.Fatalf("profile = %d, %v; want 77", ac.ProfileID, err)
		}
		_, err = NewSSHKeyResolver(&fakeSSHQ{availability: "credential_not_authorized"}, cq).AuthContextForOwner(context.Background(), cred, &owner)
		var na *SSHKeyNotAuthorizedError
		if !errors.As(err, &na) || na.PartyUUID != party {
			t.Fatalf("err = %v, want not authorized naming %s", err, party)
		}
		// A suspended party profile falls to the personal profile.
		suspended := int64(99)
		ac, err = NewSSHKeyResolver(&fakeSSHQ{availability: "available"}, cq).AuthContextForOwner(context.Background(), cred, &suspended)
		if err != nil || ac.ProfileID != 55 {
			t.Fatalf("suspended: profile = %d, %v; want personal 55", ac.ProfileID, err)
		}
	})

	t.Run("a confined credential is not authorized and names the party", func(t *testing.T) {
		t.Parallel()
		party := uuid.New()
		cq := &fakeCoreQ{getEntity: func(int64) (coredb.GetEntityByIDRow, error) { return coredb.GetEntityByIDRow{Uuid: party}, nil }}
		r := NewSSHKeyResolver(&fakeSSHQ{}, cq)
		_, err := r.AuthContextForOwner(context.Background(), SSHCredential{CredentialID: 9, PrincipalID: 5, ConfinedToPartyID: 7}, nil)
		if !errors.Is(err, ErrSSHKeyNotAuthorized) {
			t.Fatalf("got %v, want ErrSSHKeyNotAuthorized", err)
		}
		var na *SSHKeyNotAuthorizedError
		if !errors.As(err, &na) || na.PartyUUID != party {
			t.Errorf("error = %#v, want the party uuid %s", err, party)
		}
	})

	t.Run("an unavailable personal profile is not authorized", func(t *testing.T) {
		t.Parallel()
		r := NewSSHKeyResolver(&fakeSSHQ{availability: "inactive"}, &fakeCoreQ{getPersonal: profile})
		if _, err := r.AuthContextForOwner(context.Background(), SSHCredential{CredentialID: 9, PrincipalID: 5}, nil); !errors.Is(err, ErrSSHKeyNotAuthorized) {
			t.Fatalf("got %v, want ErrSSHKeyNotAuthorized", err)
		}
	})
}
