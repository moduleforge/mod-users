package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/moduleforge/users-module/model/db"
)

// stubIdentityQuerier is a minimal in-memory implementation of identityQuerier.
type stubIdentityQuerier struct {
	oidcCount    int64
	oidcCountErr error
	authLocal    *db.AuthLocal // nil → ErrNoRows
	authLocalErr error
}

func (s *stubIdentityQuerier) CountOIDCIdentitiesByUserAccount(_ context.Context, _ int64) (int64, error) {
	if s.oidcCountErr != nil {
		return 0, s.oidcCountErr
	}
	return s.oidcCount, nil
}

func (s *stubIdentityQuerier) GetAuthLocal(_ context.Context, _ int64) (db.AuthLocal, error) {
	if s.authLocalErr != nil {
		return db.AuthLocal{}, s.authLocalErr
	}
	if s.authLocal == nil {
		return db.AuthLocal{}, pgx.ErrNoRows
	}
	return *s.authLocal, nil
}

func localRow() *db.AuthLocal {
	return &db.AuthLocal{
		UserAccountID:     1,
		PasswordHash:      "$hash",
		PasswordUpdatedAt: pgtype.Timestamptz{Valid: true},
		CreatedAt:         pgtype.Timestamptz{Valid: true},
	}
}

func TestIdentityCounts(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		q            *stubIdentityQuerier
		wantOIDC     int64
		wantHasLocal bool
		wantErr      bool
	}{
		{
			name:         "no identities",
			q:            &stubIdentityQuerier{oidcCount: 0, authLocal: nil},
			wantOIDC:     0,
			wantHasLocal: false,
		},
		{
			name:         "oidc only",
			q:            &stubIdentityQuerier{oidcCount: 2, authLocal: nil},
			wantOIDC:     2,
			wantHasLocal: false,
		},
		{
			name:         "local only",
			q:            &stubIdentityQuerier{oidcCount: 0, authLocal: localRow()},
			wantOIDC:     0,
			wantHasLocal: true,
		},
		{
			name:         "both",
			q:            &stubIdentityQuerier{oidcCount: 3, authLocal: localRow()},
			wantOIDC:     3,
			wantHasLocal: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oidc, hasLocal, err := IdentityCounts(ctx, tc.q, 1)
			if (err != nil) != tc.wantErr {
				t.Fatalf("IdentityCounts() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if oidc != tc.wantOIDC {
				t.Errorf("oidc = %d, want %d", oidc, tc.wantOIDC)
			}
			if hasLocal != tc.wantHasLocal {
				t.Errorf("hasLocal = %v, want %v", hasLocal, tc.wantHasLocal)
			}
		})
	}
}
