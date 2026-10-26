package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// CollaboratorGate decides whether a profile outside a party may be added to
// the party's actor group or named as the actor of a grant over the party's
// content (ADR "Outside collaborators"). mod-authz declares the interface it
// consumes (authzservice.CollaboratorGate) and takes no dependency on this
// module; *CollaboratorGate satisfies it structurally.
//
// It holds no state: every call reads and writes through the caller's
// transaction.
type CollaboratorGate struct {
	newQ     func(pgx.Tx) usersdb.Querier
	newCoreQ func(pgx.Tx) coredb.Querier
}

// NewCollaboratorGate constructs a CollaboratorGate.
func NewCollaboratorGate() *CollaboratorGate { return &CollaboratorGate{} }

func (g *CollaboratorGate) usersQ(tx pgx.Tx) usersdb.Querier {
	if g.newQ != nil {
		return g.newQ(tx)
	}
	return usersdb.New(tx)
}

func (g *CollaboratorGate) coreQuerier(tx pgx.Tx) coredb.Querier {
	if g.newCoreQ != nil {
		return g.newCoreQ(tx)
	}
	return coredb.New(tx)
}

// CheckOutsideCollaborator admits memberProfileID, a profile mod-authz is about
// to add to a group or name as a grant actor under owningPartyID, inside tx.
//
// A profile of the owning party passes with no write. Any other profile,
// including a personal one, is outside: the owning party's authentication
// policy is checked against the credentials of the profile's holder
// (mod_users.holder_policy_mismatch). With no mismatch, or when the profile is
// already confirmed for the party, it passes. On a mismatch without
// confirmation it returns the 409 users.policy_mismatch_confirmation_required
// (an apiresp conflict, which mod-authz returns unchanged); with confirmation
// it records, idempotently, profile_policy_confirmations(memberProfileID,
// owningPartyID, <acting profile>). The confirmation exempts the profile from
// the party's credential rule only, never from its min_acr.
//
// The caller (mod-authz) has already authorized the write; the acting profile
// comes from ctx and must exist when a confirmation is recorded.
func (g *CollaboratorGate) CheckOutsideCollaborator(ctx context.Context, tx pgx.Tx, owningPartyID, memberProfileID int64, confirmed bool) error {
	cq := g.coreQuerier(tx)
	p, err := cq.GetProfileByEntityID(ctx, memberProfileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("users: collaborator gate: profile %d not found", memberProfileID)
		}
		return fmt.Errorf("users: collaborator gate: load profile: %w", err)
	}
	if p.PartyID.Valid && p.PartyID.Int64 == owningPartyID {
		return nil
	}
	if err := lockPartyPolicyShared(ctx, tx, owningPartyID); err != nil {
		return err
	}
	if _, err := cq.GetProfilePolicyConfirmation(ctx, coredb.GetProfilePolicyConfirmationParams{
		ProfileID: memberProfileID, PartyID: owningPartyID,
	}); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("users: collaborator gate: read confirmation: %w", err)
	}
	mm, err := detectPolicyMismatch(ctx, g.usersQ(tx), p.HolderID, owningPartyID, false)
	if err != nil {
		return err
	}
	if mm == nil {
		return nil
	}
	if !confirmed {
		return mm.Conflict()
	}
	ac, err := adminCaller(ctx)
	if err != nil {
		return err
	}
	return recordPolicyConfirmation(ctx, cq, memberProfileID, owningPartyID, ac.ProfileID)
}
