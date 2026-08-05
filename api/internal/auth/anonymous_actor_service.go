package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	usersdb "github.com/moduleforge/mod-users/model/db"
)

// anonymousActorSlug is the well-known system_actors.slug for the shared,
// zero-authority anonymous actor. Seeded by mod-users migration
// 0101_system_actors.sql.
const anonymousActorSlug = "anonymous"

// AnonymousActor holds the entity id of the seeded, zero-authority anonymous
// system actor, resolved once at startup.
//
// This is a shared identity, not a per-caller one: every tokenless request
// that authenticates as the anonymous actor resolves to the same entities.id.
// It owns nothing: that property is continuously enforced by a database
// constraint (a Postgres trigger). It holds no grants as of the last boot,
// verified once by the no-grants boot assertion below (see
// newAnonymousActor) — that check is not backed by a database constraint, so
// it does not continuously prevent a grant from being inserted for this
// actor's entity id after boot. It is distinct from a *guest account* — the
// per-device, stateful identity produced by POST /v1/auth/anonymous, which
// does own things and can hold grants. See the anonymous-actor architecture
// proposal's "Composition with POST /v1/auth/anonymous" section for the full
// terminology split.
type AnonymousActor struct {
	entityID int64
}

// EntityID returns the anonymous system actor's entities.id.
func (a *AnonymousActor) EntityID() int64 {
	return a.entityID
}

// lookupSystemActorBySlugFn is the slot used to resolve the anonymous
// actor's entity id by slug. Extracted as a function-typed field so tests
// can substitute a stub without needing a running Postgres, mirroring the
// injectable-function pattern used by UserResolver (see resolver.go's
// uuidLookupFn and friends).
type lookupSystemActorBySlugFn func(ctx context.Context, slug string) (usersdb.GetSystemActorBySlugRow, error)

// hasAnyGrantsFn is the slot used to run the no-grants boot assertion.
// Extracted as a function-typed field for the same reason as
// lookupSystemActorBySlugFn — see its doc comment.
type hasAnyGrantsFn func(ctx context.Context, entityID int64) (bool, error)

// NewAnonymousActor resolves the anonymous system actor's entity id by slug
// and asserts, at boot, that it holds zero grants — refusing to start if it
// finds any.
//
// The constructor signature (context.Context first, *pgxpool.Pool second,
// returning (*AnonymousActor, error)) matches the manifest wiring the
// anonymous-actor architecture proposal specifies in its §4:
// args: [context, infra:pool], returnsError: true.
//
// The no-grants assertion lives here, in application startup code, rather
// than in a mod-users migration trigger, precisely because it must run
// after *all* migrations have applied — including mod-authz's 500-series
// migrations that create the grants table itself. A mod-users migration
// runs before mod-authz's migrations in a typical composition ordering, so
// it cannot see the grants table reliably; boot-time construction runs
// after every module's Migrate call, so it can.
func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error) {
	queries := usersdb.New(pool)
	lookup := queries.GetSystemActorBySlug
	hasGrants := func(ctx context.Context, entityID int64) (bool, error) {
		var exists bool
		// grants is mod-authz's table (mod-authz/model/migrations/0505_grants.sql);
		// it is not part of mod-users' sqlc schema, so this is a direct
		// pool.QueryRow rather than a generated query. The entity id is
		// bound as a parameter, never interpolated into the SQL string.
		err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM grants WHERE actor_id = $1)", entityID).Scan(&exists)
		if err != nil {
			return false, err
		}
		return exists, nil
	}

	return newAnonymousActor(ctx, lookup, hasGrants)
}

// newAnonymousActor contains the actual resolution and assertion logic,
// factored out of NewAnonymousActor so tests can substitute the two DB
// operations with stubs, following the injectable function-field pattern
// established by UserResolver (see resolver.go, resolver_test.go).
func newAnonymousActor(ctx context.Context, lookup lookupSystemActorBySlugFn, hasGrants hasAnyGrantsFn) (*AnonymousActor, error) {
	row, err := lookup(ctx, anonymousActorSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf(
				"auth: anonymous system actor not found (slug %q); has mod-users migration 0101_system_actors.sql been applied?",
				anonymousActorSlug,
			)
		}
		return nil, fmt.Errorf("auth: resolve anonymous system actor by slug %q: %w", anonymousActorSlug, err)
	}

	// Fail closed: if the assertion query itself errors, do not treat that
	// as "no grants" — refuse to start rather than risk running with a
	// possibly-granted anonymous actor.
	exists, err := hasGrants(ctx, row.EntityID)
	if err != nil {
		return nil, fmt.Errorf("auth: check anonymous actor grants (entity_id=%d): %w", row.EntityID, err)
	}
	if exists {
		slog.ErrorContext(ctx, "auth: anonymous system actor holds one or more grants; refusing to start",
			"entity_id", row.EntityID,
			"slug", anonymousActorSlug,
		)
		return nil, fmt.Errorf("auth: anonymous system actor (entity_id=%d) holds one or more grants; it must remain zero-authority", row.EntityID)
	}

	return &AnonymousActor{entityID: row.EntityID}, nil
}
