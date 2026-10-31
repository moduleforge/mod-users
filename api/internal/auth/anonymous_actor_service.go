package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
)

// anonymousActorSlug is the well-known system_actors.slug of the shared,
// zero-authority anonymous actor seeded by mod-core.
const anonymousActorSlug = "anonymous"

// AnonymousActor holds the ids of mod-core's seeded anonymous system actor
// and its personal profile, resolved once at startup.
//
// It is a shared identity, not a per-caller one: every tokenless request on a
// route group that opts into NewResolveOrAnonymous acts as the same profile.
// It owns nothing (system actors never own entities) and held no grants at
// boot (newAnonymousActor's assertion, which is not a continuous database
// constraint). It is distinct from a guest user: a real principal with an
// anon credential and a session.
type AnonymousActor struct {
	principalID int64
	profileID   int64
}

// PrincipalID returns the anonymous system actor's principal entity id.
func (a *AnonymousActor) PrincipalID() int64 { return a.principalID }

// ProfileID returns the anonymous system actor's personal profile entity id:
// the effective actor of the AuthContext{..., Anonymous: true} that
// NewResolveOrAnonymous installs.
func (a *AnonymousActor) ProfileID() int64 { return a.profileID }

// lookupSystemActorBySlugFn resolves a system actor by slug. A function type
// so tests can substitute a stub without a running Postgres.
type lookupSystemActorBySlugFn func(ctx context.Context, slug string) (coredb.GetSystemActorBySlugRow, error)

// hasAnyGrantsFn runs the no-grants boot assertion for the given actor ids.
type hasAnyGrantsFn func(ctx context.Context, actorIDs []int64) (bool, error)

// NewAnonymousActor resolves mod-core's anonymous system actor (principal and
// personal profile) by slug and asserts, at boot, that neither holds a grant
// — refusing to start if one does.
//
// The assertion lives in startup code rather than a migration trigger because
// it must run after every module's migrations, including mod-authz's, which
// create the grants table.
func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error) {
	lookup := coredb.New(pool).GetSystemActorBySlug
	hasGrants := func(ctx context.Context, actorIDs []int64) (bool, error) {
		var exists bool
		// grants is mod-authz's table, outside mod-users' sqlc schema, so
		// this is a direct query. The ids are bound, never interpolated.
		err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM grants WHERE actor_id = ANY($1::bigint[]))", actorIDs).Scan(&exists)
		if err != nil {
			return false, err
		}
		return exists, nil
	}
	return newAnonymousActor(ctx, lookup, hasGrants)
}

// newAnonymousActor holds the resolution and assertion logic, factored out so
// tests can substitute both database operations.
func newAnonymousActor(ctx context.Context, lookup lookupSystemActorBySlugFn, hasGrants hasAnyGrantsFn) (*AnonymousActor, error) {
	row, err := lookup(ctx, anonymousActorSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf(
				"auth: anonymous system actor not found (slug %q); have mod-core's migrations been applied?",
				anonymousActorSlug,
			)
		}
		return nil, fmt.Errorf("auth: resolve anonymous system actor by slug %q: %w", anonymousActorSlug, err)
	}
	if row.EntityID <= 0 || row.PersonalProfileID <= 0 {
		return nil, fmt.Errorf("auth: anonymous system actor (slug %q) has no personal profile", anonymousActorSlug)
	}

	// Fail closed: an assertion-query error is not "no grants".
	exists, err := hasGrants(ctx, []int64{row.EntityID, row.PersonalProfileID})
	if err != nil {
		return nil, fmt.Errorf("auth: check anonymous actor grants (principal_id=%d, profile_id=%d): %w",
			row.EntityID, row.PersonalProfileID, err)
	}
	if exists {
		slog.ErrorContext(ctx, "auth: anonymous system actor holds one or more grants; refusing to start",
			"principal_id", row.EntityID,
			"profile_id", row.PersonalProfileID,
			"slug", anonymousActorSlug,
		)
		return nil, fmt.Errorf("auth: anonymous system actor (principal_id=%d, profile_id=%d) holds one or more grants; it must remain zero-authority",
			row.EntityID, row.PersonalProfileID)
	}

	return &AnonymousActor{principalID: row.EntityID, profileID: row.PersonalProfileID}, nil
}
