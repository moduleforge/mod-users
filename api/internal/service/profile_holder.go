package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// This file holds the holder's own profile actions behind
// POST /v1/me/profiles/{uuid}/accept, /decline and /leave.
//
// Authority. Like every /v1/me method they authorize on "session principal =
// profile holder", not on grants, from any interactive session of the holder:
// the holder is read from the AuthContext, never from an argument. A profile
// the caller does not hold is apiresp.ErrNotFound (masking). An impersonating
// administrator is refused (403), as for every self-service write. These
// actions are not behind the freshness gate: accepting an invitation or
// leaving a party is not a change to the holder's sign-in methods, and the
// administrator's counterpart (suspend, resume, end) is gated instead.
//
// Leaving is behind the party gate. Accept and decline are open to a session
// the gate locks out (the invitation is how the holder reaches the party), but
// leaving runs the destructive departure cleanup, so LeaveProfile refuses
// unless profile_gate(session, profile) is `available`, evaluated in the leave
// transaction on the locked profile. profile_gate is profile_availability
// without the profile's status check, so the gate decides for a suspended
// profile as well as for an active one (profile_availability reports a
// suspended profile as `inactive` before the gate); a locked-out session
// cannot leave a suspended profile and the departure cleanup does not run.

// holderProfile resolves the profile the caller holds, for a holder action.
func (s *ProfileService) holderProfile(ctx context.Context, profileUUID uuid.UUID) (opctx.AuthContext, coredb.Profile, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return opctx.AuthContext{}, coredb.Profile{}, err
	}
	p, err := s.partyProfileByUUID(ctx, profileUUID)
	if err != nil {
		return opctx.AuthContext{}, coredb.Profile{}, err
	}
	if p.HolderID != ac.PrincipalID {
		return opctx.AuthContext{}, coredb.Profile{}, apiresp.ErrNotFound
	}
	return ac, p, nil
}

// holderTransition locks the profile, requires its current status to be one of
// from (ErrProfileTransition otherwise) and moves it to to. Ending runs the
// departure cleanup of EndInTx with reason. guard, when non-nil, runs in the
// transaction on the locked profile after the status check and before the
// change; its error aborts the transition.
func (s *ProfileService) holderTransition(ctx context.Context, p coredb.Profile, from []string, to, reason string, guard func(tx pgx.Tx, locked coredb.Profile) error) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		locked, err := lockProfile(ctx, tx, p.EntityID)
		if err != nil {
			return err
		}
		ok := false
		for _, f := range from {
			ok = ok || locked.Status == f
		}
		if !ok {
			return ErrProfileTransition
		}
		if guard != nil {
			if err := guard(tx, locked); err != nil {
				return err
			}
		}
		if to == ProfileStatusEnded {
			return s.endLocked(ctx, tx, locked, reason)
		}
		_, err = s.transitionLocked(ctx, tx, locked, to, "")
		return err
	})
}

// AcceptInvitation accepts an invitation (POST /v1/me/profiles/{uuid}/accept):
// `invited` to `active`. It returns the profile as the caller's session sees
// it, so a profile the party's policy locks for this session carries its lock
// reason and remedy. A profile that is not invited is ErrProfileTransition.
func (s *ProfileService) AcceptInvitation(ctx context.Context, profileUUID uuid.UUID) (ProfileView, error) {
	ac, p, err := s.holderProfile(ctx, profileUUID)
	if err != nil {
		return ProfileView{}, err
	}
	if err := s.holderTransition(ctx, p, []string{ProfileStatusInvited}, ProfileStatusActive, "", nil); err != nil {
		return ProfileView{}, err
	}
	if s.sessions == nil {
		return ProfileView{}, errors.New("users: no session service configured for profile views")
	}
	views, err := s.sessions.Profiles(ctx, ac.SessionID)
	if err != nil {
		return ProfileView{}, err
	}
	view, ok := findViewByID(views, p.EntityID)
	if !ok {
		return ProfileView{}, apiresp.ErrNotFound
	}
	return view, nil
}

// DeclineInvitation declines an invitation (POST
// /v1/me/profiles/{uuid}/decline): `invited` to `ended` with end reason
// "declined", running the departure cleanup. A profile that is not invited is
// ErrProfileTransition.
func (s *ProfileService) DeclineInvitation(ctx context.Context, profileUUID uuid.UUID) error {
	_, p, err := s.holderProfile(ctx, profileUUID)
	if err != nil {
		return err
	}
	return s.holderTransition(ctx, p, []string{ProfileStatusInvited}, ProfileStatusEnded, EndReasonDeclined, nil)
}

// LeaveProfile leaves a party (POST /v1/me/profiles/{uuid}/leave): `active` or
// `suspended` to `ended` with end reason "left", running the departure cleanup.
// A profile that is invited (decline it instead) or already ended is
// ErrProfileTransition.
//
// Unlike accept and decline it is gated: the party gate must be met by the
// caller's session, for a suspended profile as for an active one
// (ProfileUnavailable, 401, with the lock reason otherwise),
// and the last active operator administrator cannot leave
// (ErrLastOperatorAdmin).
func (s *ProfileService) LeaveProfile(ctx context.Context, profileUUID uuid.UUID) error {
	ac, p, err := s.holderProfile(ctx, profileUUID)
	if err != nil {
		return err
	}
	guard := func(tx pgx.Tx, locked coredb.Profile) error {
		// The gate is evaluated whatever the profile's status (active or
		// suspended): ProfileAvailability reports a suspended profile as
		// inactive before the party gate, so it cannot decide here.
		avail, err := s.usersQ(tx).ProfileGate(ctx, usersdb.ProfileGateParams{SessionID: ac.SessionID, ProfileID: locked.EntityID})
		if err != nil {
			return fmt.Errorf("users: profile gate: %w", err)
		}
		if avail != availabilityAvailable {
			return ProfileUnavailable(avail)
		}
		return requireNotLastOperatorAdmin(ctx, tx, locked)
	}
	return s.holderTransition(ctx, p, []string{ProfileStatusActive, ProfileStatusSuspended}, ProfileStatusEnded, EndReasonLeft, guard)
}
