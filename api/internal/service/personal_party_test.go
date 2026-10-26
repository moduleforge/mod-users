package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

type personalCoreQ struct {
	coredb.Querier
	profile coredb.Profile
	pErr    error
	person  coredb.GetEntityByIDRow
	eErr    error
}

func (f *personalCoreQ) GetProfileByEntityID(context.Context, int64) (coredb.Profile, error) {
	return f.profile, f.pErr
}

func (f *personalCoreQ) GetEntityByID(context.Context, int64) (coredb.GetEntityByIDRow, error) {
	return f.person, f.eErr
}

type personalUsersQ struct {
	usersdb.Querier
	user usersdb.User
	err  error
}

func (f *personalUsersQ) GetUserByEntityIDForUpdate(context.Context, int64) (usersdb.User, error) {
	return f.user, f.err
}

func personalCtx(principal, profile int64) context.Context {
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: principal, ProfileID: profile})
}

func TestEnsurePersonalParty_Preconditions(t *testing.T) {
	t.Parallel()
	const userID, profileID = int64(10), int64(11)
	personalProfile := coredb.Profile{EntityID: profileID, HolderID: userID}

	cases := []struct {
		name string
		ctx  context.Context
		core *personalCoreQ
		user *personalUsersQ
		want error
	}{
		{"no auth context", context.Background(), &personalCoreQ{}, &personalUsersQ{}, apiresp.ErrUnauthenticated},
		{"unknown profile", personalCtx(userID, profileID), &personalCoreQ{pErr: pgx.ErrNoRows}, &personalUsersQ{}, ErrPersonalProfileRequired},
		{"party profile", personalCtx(userID, profileID),
			&personalCoreQ{profile: coredb.Profile{EntityID: profileID, HolderID: userID, PartyID: pgtype.Int8{Int64: 5, Valid: true}}},
			&personalUsersQ{}, ErrPersonalProfileRequired},
		{"profile held by another principal", personalCtx(99, profileID), &personalCoreQ{profile: personalProfile}, &personalUsersQ{}, ErrPersonalProfileRequired},
		{"principal is not a user", personalCtx(userID, profileID), &personalCoreQ{profile: personalProfile}, &personalUsersQ{err: pgx.ErrNoRows}, ErrPersonalProfileRequired},
		{"guest", personalCtx(userID, profileID), &personalCoreQ{profile: personalProfile},
			&personalUsersQ{user: usersdb.User{EntityID: userID, IsGuest: true}}, ErrGuestMustUpgrade},
		{"managed user", personalCtx(userID, profileID), &personalCoreQ{profile: personalProfile},
			&personalUsersQ{user: usersdb.User{EntityID: userID, ManagingPartyID: pgtype.Int8{Int64: 5, Valid: true}}}, ErrPersonalProfileRequired},
		{"linked person owned by someone else", personalCtx(userID, profileID),
			&personalCoreQ{profile: personalProfile, person: coredb.GetEntityByIDRow{ID: 20, OwnerID: pgtype.Int8{Int64: 77, Valid: true}}},
			&personalUsersQ{user: usersdb.User{EntityID: userID, PersonID: pgtype.Int8{Int64: 20, Valid: true}}}, ErrPersonalPartyNotOwned},
		{"linked person without an owner", personalCtx(userID, profileID),
			&personalCoreQ{profile: personalProfile, person: coredb.GetEntityByIDRow{ID: 20}},
			&personalUsersQ{user: usersdb.User{EntityID: userID, PersonID: pgtype.Int8{Int64: 20, Valid: true}}}, ErrPersonalPartyNotOwned},
		{"linked person missing", personalCtx(userID, profileID),
			&personalCoreQ{profile: personalProfile, eErr: pgx.ErrNoRows},
			&personalUsersQ{user: usersdb.User{EntityID: userID, PersonID: pgtype.Int8{Int64: 20, Valid: true}}}, ErrPersonalPartyNotOwned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewPersonalPartyService(nil)
			s.newCoreQ = func(pgx.Tx) coredb.Querier { return tc.core }
			s.newQ = func(pgx.Tx) usersdb.Querier { return tc.user }
			// Every precondition refuses before a grant query runs, so the
			// nil authzdb querier is never reached.
			if _, err := s.EnsurePersonalParty(tc.ctx, nil); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPersonalPartyErrorsRenderTheirStatuses(t *testing.T) {
	t.Parallel()
	var ae *AuthError
	if !errors.As(ErrPersonalProfileRequired, &ae) || ae.Status != http.StatusForbidden || ae.Code != "users.personal_profile_required" {
		t.Fatalf("ErrPersonalProfileRequired = %+v, want 403 users.personal_profile_required", ae)
	}
	if !errors.Is(ErrPersonalProfileRequired, apiresp.ErrForbidden) {
		t.Error("ErrPersonalProfileRequired must classify as forbidden")
	}
	if !errors.Is(ErrPersonalPartyNotOwned, apiresp.ErrConflict) {
		t.Error("ErrPersonalPartyNotOwned must classify as a conflict (409)")
	}
}
