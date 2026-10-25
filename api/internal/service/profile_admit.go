package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// AdmitInput is the input of AdmitInTx.
type AdmitInput struct {
	PartyID      int64
	HolderUserID int64
	// InvitedByProfileID is recorded as invited_by_profile_id on a profile this
	// call creates; 0 when none.
	InvitedByProfileID int64
}

// lockOpenProfileSQL reads and locks the holder's open (not ended) profile in a
// party. profiles_one_open_per_holder_party allows at most one.
const lockOpenProfileSQL = `SELECT ` + profileColumns + `
  FROM public.profiles WHERE holder_id = $1 AND party_id = $2 AND status <> 'ended' FOR UPDATE`

// admitAction is what AdmitInTx does with the holder's open profile.
type admitAction int

const (
	admitCreate   admitAction = iota // no open profile: create an active one
	admitReturn                      // active: return as is
	admitActivate                    // invited: activate
	admitRefuse                      // suspended: refuse
)

// admitActionFor maps the status of the holder's open profile (empty when there
// is none) to the admission action.
func admitActionFor(status string) admitAction {
	switch status {
	case ProfileStatusActive:
		return admitReturn
	case ProfileStatusInvited:
		return admitActivate
	case ProfileStatusSuspended:
		return admitRefuse
	default:
		return admitCreate
	}
}

// validateAdmit checks the shape of in.
func validateAdmit(in AdmitInput) error {
	if in.PartyID <= 0 {
		return invalidField("party", "users.party_required", "a party is required")
	}
	if in.HolderUserID <= 0 {
		return invalidField("holder", "users.holder_required", "a holder is required")
	}
	if in.InvitedByProfileID < 0 {
		return invalidField("invited_by", "users.invited_by_invalid", "invited_by_profile_id is invalid")
	}
	return nil
}

// AdmitInTx admits a user into a party in the caller's transaction and returns
// the holder's active profile there (decision U22; used by mod-billing to seat
// a holder who accepted an invitation or took a seat themselves).
//
// This is a trusted operation: it consults no grant. The caller has authorized
// the admission: the holder consented, and the party consented through an
// earlier authorized invitation. Unlike InviteInTx it neither authorizes the
// acting profile nor leaves an `invited` row that needs a second acceptance.
// ctx must still carry an AuthContext with an acting profile (apiresp.
// ErrUnauthenticated otherwise), because the observations need one; on
// mod-billing's paths that is the accepting holder's profile or the
// `billing.paddle_sync` system actor's.
//
// The holder's open (not ended) profile in the party decides the outcome:
//
//   - `active`: returned as is; nothing is written and no observer runs.
//   - `invited`: becomes `active` through the same status-change path as an
//     accept (`update` `profile`; lifecycle observers see invited to active).
//   - `suspended`: ErrProfileSuspended; nothing is written.
//   - none (or only ended ones): a new `active` profile, source `manual`, owned
//     by the party, invited_by_profile_id = InvitedByProfileID (NULL when 0),
//     created by the acting profile (`create` `profile`; lifecycle observers see
//     a change from "" to `active`).
//
// Refusals: a 400 field error for a malformed input; apiresp.ErrNotFound for an
// unknown or archived party or an unknown holder; ErrGuestMustUpgrade for a
// guest holder; ErrHolderNotAdmissible for a managed user of another party or a
// principal that is not a user. A database refusal of the holder rules (MF103)
// maps to the same errors.
//
// No policy-mismatch check runs: admission is not an outside-collaborator
// invite. A holder whose credentials do not meet the party's authentication
// policy gets an active profile that `profile_availability` reports as locked
// until the holder adds a qualifying credential.
//
// Concurrency: calls are serialized per holder by locking the holder's
// `mod_users.users` row and re-reading the open profile under that lock, so two
// concurrent admissions of one holder into one party yield one profile and no
// error, without relying on (and so without aborting the transaction through)
// the profiles_one_open_per_holder_party unique index.
func (s *ProfileService) AdmitInTx(ctx context.Context, tx pgx.Tx, in AdmitInput) (Profile, error) {
	if err := validateAdmit(in); err != nil {
		return Profile{}, err
	}
	if _, err := adminCaller(ctx); err != nil {
		return Profile{}, err
	}
	if err := s.requireParty(ctx, s.coreQuerier(tx), in.PartyID); err != nil {
		return Profile{}, err
	}

	holder, err := s.lockHolder(ctx, tx, in.HolderUserID)
	if err != nil {
		return Profile{}, err
	}
	if err := checkAdmissible(holder, in.PartyID); err != nil {
		return Profile{}, err
	}

	open, err := scanProfile(tx.QueryRow(ctx, lockOpenProfileSQL, holder.EntityID, in.PartyID))
	hasOpen := true
	if errors.Is(err, apiresp.ErrNotFound) {
		hasOpen, err = false, nil
	}
	if err != nil {
		return Profile{}, err
	}
	status := ""
	if hasOpen {
		status = open.Status
	}

	switch admitActionFor(status) {
	case admitReturn:
		return s.existingProfile(ctx, tx, open)
	case admitActivate:
		updated, err := s.transitionLocked(ctx, tx, open, ProfileStatusActive, "")
		if err != nil {
			return Profile{}, err
		}
		return s.existingProfile(ctx, tx, updated)
	case admitRefuse:
		return Profile{}, ErrProfileSuspended
	default:
		return s.createInTx(ctx, tx, newProfile{
			HolderID: holder.EntityID, Holder: holder, PartyID: in.PartyID,
			Status: ProfileStatusActive, Source: ProfileSourceManual, InvitedByProfileID: in.InvitedByProfileID,
		})
	}
}

// lockHolder locks and returns the users row of holderID. A principal that is
// not a user is ErrHolderNotAdmissible; an unknown id is apiresp.ErrNotFound.
func (s *ProfileService) lockHolder(ctx context.Context, tx pgx.Tx, holderID int64) (usersdb.User, error) {
	user, err := s.usersQ(tx).GetUserByEntityIDForUpdate(ctx, holderID)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return usersdb.User{}, fmt.Errorf("users: lock holder: %w", err)
	}
	if _, perr := s.coreQuerier(tx).GetPrincipalByEntityID(ctx, holderID); perr == nil {
		return usersdb.User{}, ErrHolderNotAdmissible
	} else if !errors.Is(perr, pgx.ErrNoRows) {
		return usersdb.User{}, fmt.Errorf("users: look up holder: %w", perr)
	}
	return usersdb.User{}, apiresp.ErrNotFound
}

// existingProfile renders a profile row the call did not create.
func (s *ProfileService) existingProfile(ctx context.Context, tx pgx.Tx, p coredb.Profile) (Profile, error) {
	ids, err := entityUUIDs(ctx, tx, []int64{p.EntityID})
	if err != nil {
		return Profile{}, err
	}
	return toProfile(p, ids[p.EntityID]), nil
}
