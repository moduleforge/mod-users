package config

// Unit tests (no database) for fetchOrGeneratePersistedSigningKey, the
// unreachable-database path of bootstrapSigningKeyFromDB, and a drift guard
// between the bootstrap DDL and model/migrations/sql/0100_baseline.sql.
// Package config (not config_test) because the symbols are unexported.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	db "github.com/moduleforge/mod-users/model/db"
)

// fakeSigningKeyQuerier simulates mod_users.auth_signing_keys. stored is the
// persisted DER (nil = no row). raceWinner, when set, is what a concurrent
// caller inserts just before this caller's insert, so the insert affects no
// row.
type fakeSigningKeyQuerier struct {
	stored     []byte
	getErr     error
	insertErr  error
	raceWinner []byte

	gets, inserts int
}

func (f *fakeSigningKeyQuerier) GetSigningKey(context.Context) (db.AuthSigningKey, error) {
	f.gets++
	if f.getErr != nil {
		return db.AuthSigningKey{}, f.getErr
	}
	if f.stored == nil {
		return db.AuthSigningKey{}, pgx.ErrNoRows
	}
	return db.AuthSigningKey{ID: 1, PrivateKey: f.stored}, nil
}

func (f *fakeSigningKeyQuerier) InsertSigningKeyIfAbsent(_ context.Context, der []byte) (int64, error) {
	f.inserts++
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	if f.raceWinner != nil {
		f.stored = f.raceWinner
		return 0, nil
	}
	if f.stored != nil {
		return 0, nil
	}
	f.stored = der
	return 1, nil
}

func newDERKey(t *testing.T) (ed25519.PrivateKey, []byte) {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return k, der
}

func TestFetchOrGeneratePersistedSigningKey(t *testing.T) {
	ctx := context.Background()

	t.Run("existing key is returned unchanged", func(t *testing.T) {
		want, der := newDERKey(t)
		q := &fakeSigningKeyQuerier{stored: der}
		got, err := fetchOrGeneratePersistedSigningKey(ctx, q)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !got.Equal(want) || q.inserts != 0 {
			t.Errorf("key mismatch or unexpected insert (%d)", q.inserts)
		}
	})

	t.Run("absent key is generated, persisted and read back", func(t *testing.T) {
		q := &fakeSigningKeyQuerier{}
		got, err := fetchOrGeneratePersistedSigningKey(ctx, q)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if q.inserts != 1 || q.stored == nil {
			t.Fatalf("expected one insert, got %d", q.inserts)
		}
		persisted, err := parsePersistedSigningKey(q.stored)
		if err != nil || !persisted.Equal(got) {
			t.Errorf("returned key is not the persisted key (err %v)", err)
		}
	})

	t.Run("lost race converges on the winner", func(t *testing.T) {
		winner, der := newDERKey(t)
		q := &fakeSigningKeyQuerier{raceWinner: der}
		got, err := fetchOrGeneratePersistedSigningKey(ctx, q)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !got.Equal(winner) {
			t.Error("expected the race winner's key")
		}
	})

	t.Run("corrupt persisted key fails loudly", func(t *testing.T) {
		q := &fakeSigningKeyQuerier{stored: []byte("not a key, but long enough to pass the length check")}
		if _, err := fetchOrGeneratePersistedSigningKey(ctx, q); err == nil {
			t.Fatal("expected an error")
		}
		if q.inserts != 0 {
			t.Error("a corrupt key must never be regenerated")
		}
	})

	t.Run("read error never generates", func(t *testing.T) {
		q := &fakeSigningKeyQuerier{getErr: errors.New("boom")}
		if _, err := fetchOrGeneratePersistedSigningKey(ctx, q); err == nil {
			t.Fatal("expected an error")
		}
		if q.inserts != 0 {
			t.Error("must not insert after a read error")
		}
	})

	t.Run("insert error is returned", func(t *testing.T) {
		q := &fakeSigningKeyQuerier{insertErr: errors.New("boom")}
		if _, err := fetchOrGeneratePersistedSigningKey(ctx, q); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestBootstrapSigningKeyFromDB_UnreachableDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := bootstrapSigningKeyFromDB(ctx, "postgres://u:p@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err == nil || !strings.Contains(err.Error(), "signingkey: connect") {
		t.Fatalf("err = %v, want a wrapped connect error", err)
	}
}

func TestBootstrapSigningKeyDDL_MatchesBaselineMigration(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "model", "migrations", "sql", "0100_baseline.sql")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for name, ddl := range map[string]string{
		"schema": bootstrapSigningKeySchemaDDL,
		"table":  bootstrapSigningKeyTableDDL,
	} {
		if !strings.Contains(string(content), ddl) {
			t.Errorf("%s does not contain the bootstrap %s DDL verbatim:\n%s", path, name, ddl)
		}
	}
}
