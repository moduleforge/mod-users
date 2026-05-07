// Package authz provides the users-module Authorizer implementation.
//
// Policy: users in the 'admin' group can do anything; all other users can only
// access their own data. The first account created is given root (admin)
// privileges, which is enforced at creation time by the resolver.
//
// The implementation resolves the acting user from ctx via opctx.ActorEntityID
// (and opctx.AssumedActorEntityID for assume sessions). For non-admins, all
// operations are permitted only when the target entity ID matches the actor's
// own entity ID. Reads on other users' data are denied for non-admins.
//
// Authorization for resources that have no entity ID (pre-create, or list
// operations) is denied for non-admins — create and list are admin-only.
package authz

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	usersdb "github.com/moduleforge/users-module/model/db"
)

// ErrUnauthenticated is returned when no actor is present on the context.
// HTTP handlers should map this to 401.
var ErrUnauthenticated = errors.New("authz: no authenticated actor")

// ErrForbidden is returned when the actor is authenticated but not permitted
// to perform the requested operation. HTTP handlers should map this to 403.
var ErrForbidden = errors.New("authz: forbidden")

// Compile-time assertion: Authorizer satisfies core's authz.Authorizer.
var _ coreAuthz.Authorizer = (*Authorizer)(nil)

// Authorizer is the users-module implementation of core's authz.Authorizer.
// It is constructed once at the composition root (main.go) and injected into
// all service constructors via coreservice.New.
type Authorizer struct {
	q usersdb.Querier
}

// New constructs an Authorizer. q is used to look up the actor's is_admin flag
// by account_holder (the entity_id that links the user_account to the entity
// hierarchy).
func New(q usersdb.Querier) *Authorizer {
	return &Authorizer{q: q}
}

// Authorize enforces the policy described in the package doc.
//
// The effective actor is whichever entity ID is set on ctx:
//   - When AssumedActorEntityID is set, that is the effective actor (admin is
//     acting as the assumed user; the assumed user's permissions apply).
//   - Otherwise ActorEntityID is the actor.
//
// Operations with a nil target (create, list) are always denied for
// non-admin actors.
func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
	// Resolve effective actor. Assumed actor takes priority over real actor.
	actorEntityID, ok := effectiveActor(ctx)
	if !ok {
		return ErrUnauthenticated
	}

	// Look up admin status via the user_account row. account_holder = entity_id.
	isAdmin, err := a.isAdmin(ctx, actorEntityID)
	if err != nil {
		// A lookup failure is an internal fault, not an authz denial. Return
		// it as-is so handlers can map it to 500.
		return err
	}

	if isAdmin {
		return nil // admins can do anything
	}

	// Non-admin: only allowed to act on their own entity.
	if target == nil {
		// No entity ID means this is a create or list operation — admin only.
		return ErrForbidden
	}

	if *target == actorEntityID {
		return nil // own data
	}

	return ErrForbidden
}

// effectiveActor returns the entity ID that should be used for policy checks.
// If an assumed actor is set (admin assuming another user's identity), that
// entity ID is returned, since the admin is acting as the assumed user.
func effectiveActor(ctx context.Context) (int64, bool) {
	if id, ok := opctx.AssumedActorEntityID(ctx); ok {
		return id, true
	}
	return opctx.ActorEntityID(ctx)
}

// isAdmin queries the user_account by account_holder (entity_id) to determine
// whether the actor has admin privileges.
//
// When the account_holder has no user_account row (deleted account, service
// account, or corporation with no user_account), the lookup returns ErrNoRows.
// This is a forbidden state, not a server fault: the actor cannot be
// authenticated as a user with any privileges.
func (a *Authorizer) isAdmin(ctx context.Context, entityID int64) (bool, error) {
	ua, err := a.q.GetUserAccountByAccountHolder(ctx, entityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrForbidden
		}
		return false, err
	}
	return ua.IsAdmin, nil
}
