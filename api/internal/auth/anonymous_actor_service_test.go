package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	usersdb "github.com/moduleforge/mod-users/model/db"
)

// errUnexpected is a stand-in for an opaque, non-ErrNoRows database error.
var errUnexpected = errors.New("boom: connection reset")

func TestNewAnonymousActor(t *testing.T) {
	const resolvedEntityID int64 = 777

	tests := []struct {
		name          string
		lookup        lookupSystemActorBySlugFn
		hasGrants     hasAnyGrantsFn
		wantEntityID  int64
		wantErr       bool
		wantErrSubstr []string
	}{
		{
			name: "row present, no grants: succeeds",
			lookup: func(_ context.Context, slug string) (usersdb.GetSystemActorBySlugRow, error) {
				if slug != anonymousActorSlug {
					t.Errorf("lookup slug = %q, want %q", slug, anonymousActorSlug)
				}
				return usersdb.GetSystemActorBySlugRow{EntityID: resolvedEntityID, Slug: slug}, nil
			},
			hasGrants: func(_ context.Context, entityID int64) (bool, error) {
				if entityID != resolvedEntityID {
					t.Errorf("hasGrants entityID = %d, want %d", entityID, resolvedEntityID)
				}
				return false, nil
			},
			wantEntityID: resolvedEntityID,
			wantErr:      false,
		},
		{
			name: "row missing: error mentions slug and migration",
			lookup: func(_ context.Context, _ string) (usersdb.GetSystemActorBySlugRow, error) {
				return usersdb.GetSystemActorBySlugRow{}, pgx.ErrNoRows
			},
			hasGrants: func(_ context.Context, _ int64) (bool, error) {
				t.Error("hasGrants must not be called when the slug lookup fails")
				return false, nil
			},
			wantErr:       true,
			wantErrSubstr: []string{anonymousActorSlug, "0100_baseline.sql"},
		},
		{
			name: "lookup returns unexpected error: wrapped",
			lookup: func(_ context.Context, _ string) (usersdb.GetSystemActorBySlugRow, error) {
				return usersdb.GetSystemActorBySlugRow{}, errUnexpected
			},
			hasGrants: func(_ context.Context, _ int64) (bool, error) {
				t.Error("hasGrants must not be called when the slug lookup fails")
				return false, nil
			},
			wantErr:       true,
			wantErrSubstr: []string{"boom: connection reset"},
		},
		{
			name: "grants exist: refuses to start",
			lookup: func(_ context.Context, _ string) (usersdb.GetSystemActorBySlugRow, error) {
				return usersdb.GetSystemActorBySlugRow{EntityID: resolvedEntityID, Slug: anonymousActorSlug}, nil
			},
			hasGrants: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
			wantErr:       true,
			wantErrSubstr: []string{"grant"},
		},
		{
			name: "grants-existence query errors: fails closed",
			lookup: func(_ context.Context, _ string) (usersdb.GetSystemActorBySlugRow, error) {
				return usersdb.GetSystemActorBySlugRow{EntityID: resolvedEntityID, Slug: anonymousActorSlug}, nil
			},
			hasGrants: func(_ context.Context, _ int64) (bool, error) {
				return false, errUnexpected
			},
			wantErr:       true,
			wantErrSubstr: []string{"boom: connection reset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := newAnonymousActor(context.Background(), tt.lookup, tt.hasGrants)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("newAnonymousActor: expected error, got nil (actor=%+v)", got)
				}
				if got != nil {
					t.Errorf("newAnonymousActor: expected nil *AnonymousActor on error, got %+v", got)
				}
				for _, substr := range tt.wantErrSubstr {
					if !strings.Contains(err.Error(), substr) {
						t.Errorf("newAnonymousActor error = %q, want substring %q", err.Error(), substr)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("newAnonymousActor: unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("newAnonymousActor: expected non-nil *AnonymousActor")
			}
			if got.EntityID() != tt.wantEntityID {
				t.Errorf("EntityID() = %d, want %d", got.EntityID(), tt.wantEntityID)
			}
		})
	}
}
