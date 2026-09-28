package service

// Unit tests for SSHKeyResolver.ResolveActor. Shares stubCoreQuerier and
// stubSSHQuerier, and the genEd25519Line/activeEntity/archivedEntity
// fixtures, with ssh_keys_test.go (same package).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
)

// genEd25519Key generates a fresh ssh.PublicKey for ResolveActor's key
// parameter (a parsed key, not an authorized_keys line).
func genEd25519Key(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap ed25519 key: %v", err)
	}
	return sshPub
}

func newTestSSHKeyResolver(q *stubSSHQuerier, coreQ *stubCoreQuerier) *SSHKeyResolver {
	return &SSHKeyResolver{q: q, coreQ: coreQ}
}

func TestSSHKeyResolver_ResolveActor_NilKeyReturnsErrUnknownSSHKey(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier()
	coreQ := &stubCoreQuerier{}
	r := newTestSSHKeyResolver(q, coreQ)

	actorID, err := r.ResolveActor(context.Background(), nil)

	if !errors.Is(err, ErrUnknownSSHKey) {
		t.Errorf("error: got %v, want ErrUnknownSSHKey", err)
	}
	if actorID != 0 {
		t.Errorf("actorID: got %d, want 0", actorID)
	}
	if q.writeCalled {
		t.Error("a nil key must not reach any write path")
	}
}

func TestSSHKeyResolver_ResolveActor_NoRowsReturnsErrUnknownSSHKey(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier()
	q.resolveErr = pgx.ErrNoRows
	coreQ := &stubCoreQuerier{}
	r := newTestSSHKeyResolver(q, coreQ)

	_, err := r.ResolveActor(context.Background(), genEd25519Key(t))

	if !errors.Is(err, ErrUnknownSSHKey) {
		t.Errorf("error: got %v, want ErrUnknownSSHKey", err)
	}
	if q.writeCalled {
		t.Error("an unresolved key must not reach any write path")
	}
}

func TestSSHKeyResolver_ResolveActor_ArchivedHolderReturnsErrUnknownSSHKey(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier()
	q.resolveAccountHolder = 100
	coreQ := &stubCoreQuerier{entity: archivedEntity(100)}
	r := newTestSSHKeyResolver(q, coreQ)

	_, err := r.ResolveActor(context.Background(), genEd25519Key(t))

	if !errors.Is(err, ErrUnknownSSHKey) {
		t.Errorf("error: got %v, want ErrUnknownSSHKey", err)
	}
	if q.writeCalled {
		t.Error("an archived holder must not reach any write path")
	}
}

func TestSSHKeyResolver_ResolveActor_MissingHolderEntityReturnsErrUnknownSSHKey(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier()
	q.resolveAccountHolder = 100
	coreQ := &stubCoreQuerier{entityErr: pgx.ErrNoRows}
	r := newTestSSHKeyResolver(q, coreQ)

	_, err := r.ResolveActor(context.Background(), genEd25519Key(t))

	if !errors.Is(err, ErrUnknownSSHKey) {
		t.Errorf("error: got %v, want ErrUnknownSSHKey", err)
	}
}

func TestSSHKeyResolver_ResolveActor_DBErrorDoesNotSatisfyErrUnknownSSHKey(t *testing.T) {
	t.Parallel()

	dbErr := errors.New("connection reset")

	t.Run("resolve query error", func(t *testing.T) {
		t.Parallel()
		q := newStubSSHQuerier()
		q.resolveErr = dbErr
		coreQ := &stubCoreQuerier{}
		r := newTestSSHKeyResolver(q, coreQ)

		_, err := r.ResolveActor(context.Background(), genEd25519Key(t))

		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if errors.Is(err, ErrUnknownSSHKey) {
			t.Errorf("a transient DB error must not satisfy errors.Is(err, ErrUnknownSSHKey): %v", err)
		}
		if !errors.Is(err, dbErr) {
			t.Errorf("error does not wrap the underlying DB error: %v", err)
		}
	})

	t.Run("entity lookup error", func(t *testing.T) {
		t.Parallel()
		q := newStubSSHQuerier()
		q.resolveAccountHolder = 100
		coreQ := &stubCoreQuerier{entityErr: dbErr}
		r := newTestSSHKeyResolver(q, coreQ)

		_, err := r.ResolveActor(context.Background(), genEd25519Key(t))

		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if errors.Is(err, ErrUnknownSSHKey) {
			t.Errorf("a transient DB error must not satisfy errors.Is(err, ErrUnknownSSHKey): %v", err)
		}
		if !errors.Is(err, dbErr) {
			t.Errorf("error does not wrap the underlying DB error: %v", err)
		}
	})
}

func TestSSHKeyResolver_ResolveActor_Success(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier()
	q.resolveAccountHolder = 100
	coreQ := &stubCoreQuerier{entity: activeEntity(100)}
	r := newTestSSHKeyResolver(q, coreQ)

	actorID, err := r.ResolveActor(context.Background(), genEd25519Key(t))

	if err != nil {
		t.Fatalf("ResolveActor returned error: %v", err)
	}
	if actorID != 100 {
		t.Errorf("actorID: got %d, want 100", actorID)
	}
	if q.writeCalled {
		t.Error("ResolveActor must never call a write method")
	}
}
