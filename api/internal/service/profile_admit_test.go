package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// lockingUsersQ answers GetUserByEntityIDForUpdate, the holder lock of AdmitInTx.
type lockingUsersQ struct {
	usersdb.Querier
	user func(int64) (usersdb.User, error)
}

func (f *lockingUsersQ) GetUserByEntityIDForUpdate(_ context.Context, id int64) (usersdb.User, error) {
	return f.user(id)
}

func newAdmitTestService(uq *lockingUsersQ, cq *profileCoreQ) *ProfileService {
	s := NewProfileService(nil, uq, cq, &opAuthorizer{}, nil, &UserService{}, nil, nil)
	s.newQ = func(pgx.Tx) usersdb.Querier { return uq }
	s.newCoreQ = func(pgx.Tx) coredb.Querier { return cq }
	return s
}

func TestAdmitActionFor(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]admitAction{
		"":                     admitCreate,
		ProfileStatusActive:    admitReturn,
		ProfileStatusInvited:   admitActivate,
		ProfileStatusSuspended: admitRefuse,
	} {
		if got := admitActionFor(status); got != want {
			t.Errorf("admitActionFor(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestValidateAdmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   AdmitInput
		ok   bool
	}{
		{"valid", AdmitInput{PartyID: 5, HolderUserID: 6}, true},
		{"valid with inviter", AdmitInput{PartyID: 5, HolderUserID: 6, InvitedByProfileID: 7}, true},
		{"no party", AdmitInput{HolderUserID: 6}, false},
		{"no holder", AdmitInput{PartyID: 5}, false},
		{"negative inviter", AdmitInput{PartyID: 5, HolderUserID: 6, InvitedByProfileID: -1}, false},
	}
	for _, tc := range tests {
		err := validateAdmit(tc.in)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestAdmitInTx_RefusalsBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	notFound := func(int64) (usersdb.User, error) { return usersdb.User{}, pgx.ErrNoRows }
	managedOf := func(party int64) usersdb.User {
		return usersdb.User{EntityID: 6, ManagingPartyID: pgtype.Int8{Int64: party, Valid: true}}
	}
	tests := []struct {
		name string
		user func(int64) (usersdb.User, error)
		cq   *profileCoreQ
		want error
	}{
		{"guest", func(int64) (usersdb.User, error) { return usersdb.User{EntityID: 6, IsGuest: true}, nil },
			&profileCoreQ{}, ErrGuestMustUpgrade},
		{"managed user of another party", func(int64) (usersdb.User, error) { return managedOf(9), nil },
			&profileCoreQ{}, ErrHolderNotAdmissible},
		{"principal that is not a user", notFound,
			&profileCoreQ{principal: func(int64) (coredb.Principal, error) { return coredb.Principal{}, nil }}, ErrHolderNotAdmissible},
		{"unknown holder", notFound,
			&profileCoreQ{principal: func(int64) (coredb.Principal, error) { return coredb.Principal{}, pgx.ErrNoRows }}, apiresp.ErrNotFound},
		{"unknown party", func(int64) (usersdb.User, error) { return usersdb.User{EntityID: 6}, nil },
			&profileCoreQ{entityByID: func(int64) (coredb.GetEntityByIDRow, error) { return coredb.GetEntityByIDRow{}, pgx.ErrNoRows }},
			apiresp.ErrNotFound},
	}
	for _, tc := range tests {
		// A nil transaction panics on any read or write past the refusal.
		s := newAdmitTestService(&lockingUsersQ{user: tc.user}, tc.cq)
		_, err := s.AdmitInTx(authCtx(sessionAdmin), nil, AdmitInput{PartyID: 5, HolderUserID: 6})
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestAdmitInTx_RequiresAnActingProfileAndValidInput(t *testing.T) {
	t.Parallel()
	s := newAdmitTestService(&lockingUsersQ{}, &profileCoreQ{})
	if _, err := s.AdmitInTx(context.Background(), nil, AdmitInput{PartyID: 5}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("no holder = %v, want invalid input before authentication", err)
	}
	if _, err := s.AdmitInTx(context.Background(), nil, AdmitInput{PartyID: 5, HolderUserID: 6}); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Errorf("no AuthContext = %v, want unauthenticated", err)
	}
}
