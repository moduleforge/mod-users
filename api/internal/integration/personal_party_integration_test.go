//go:build integration

package integration

// PersonalPartyService.EnsurePersonalParty against the real services, database
// triggers, the production Authorizer (localAuthz.New on the real pool) and the
// real mod-audit observer: creation and idempotency, the concurrency
// guarantee, the refusals, what the owned-scope grant reaches (a seat in the
// person party) and does not reach (the person record itself, mod-core D7), and
// mod-billing's B6 sequence (decision U28): ensure in its own committed
// transaction, then authorize in a fresh context.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// ensurePersonal runs EnsurePersonalParty in its own transaction on the pool
// and commits it when the call succeeds.
func (s *stack) ensurePersonal(ctx context.Context) (svc.PersonalParty, error) {
	var pp svc.PersonalParty
	err := s.inTx(ctx, func(tx pgxTx) error {
		var err error
		pp, err = s.personal.EnsurePersonalParty(ctx, tx)
		return err
	})
	return pp, err
}

// personalUser registers a user whose users.person_id is NULL again (the path
// for users created otherwise than at sign-up, decision U11) and returns the
// account, its user id, its personal profile id and an auth context acting as
// that profile.
func (s *stack) personalUser(t *testing.T) (a account, userID, profileID int64, ctx context.Context) {
	t.Helper()
	a = s.register(t)
	ctx = s.authCtx(t, a.tk.access())
	ac, _ := opctx.Auth(ctx)
	userID, profileID = ac.PrincipalID, ac.ProfileID
	s.exec(t, "unlink the sign-up person", `UPDATE mod_users.users SET person_id = NULL WHERE entity_id = $1`, userID)
	return a, userID, profileID, ctx
}

// ownedPersons counts the natural_person entities owned by the user.
func (s *stack) ownedPersons(t *testing.T, userID int64) int64 {
	t.Helper()
	return s.count(t, "owned persons", `
SELECT count(*) FROM entities e JOIN types ty ON ty.id = e.fundamental_type_id
 WHERE e.owner_id = $1 AND ty.slug = 'natural_person'`, userID)
}

// ownedManageGrants counts the personal profile's owned-scope manage grants.
func (s *stack) ownedManageGrants(t *testing.T, profileID int64) int64 {
	t.Helper()
	return s.count(t, "owned manage grants", `
SELECT count(*) FROM grants g JOIN authz_operations o ON o.id = g.operation_id
 WHERE g.actor_id = $1 AND g.scope = 'owned' AND o.slug = 'manage'`, profileID)
}

func TestEnsurePersonalParty_CreatesThePersonAndTheGrantThenIsANoOp(t *testing.T) {
	_, userID, profileID, ctx := env.personalUser(t)
	before := env.ownedPersons(t, userID) // the unlinked sign-up person stays

	env.obs.reset()
	first, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	if !first.Created || first.PartyID == 0 || first.GrantID == 0 || first.PartyUUID.String() != env.entityUUID(t, first.PartyID) {
		t.Fatalf("first = %+v, want a created party and grant", first)
	}
	if got := env.obs.pairs(); !slices.Equal(got, []string{"create natural_person", "update user", "grant grant"}) {
		t.Errorf("observations = %v, want create natural_person, update user, grant grant", got)
	}
	for _, o := range env.obs.all() {
		if o.Auth.ProfileID != profileID || o.Auth.PrincipalID != userID {
			t.Errorf("%s %s observed as %+v, want the personal profile", o.Op, o.Resource, o.Auth)
		}
	}

	// The person is the user's own natural_person, linked on the user.
	if n := env.count(t, "person", `
SELECT count(*) FROM mod_users.users u
  JOIN entities pe ON pe.id = u.person_id AND pe.owner_id = u.entity_id AND pe.created_by_profile_id = $2
  JOIN natural_persons np ON np.entity_id = pe.id
 WHERE u.entity_id = $1 AND u.person_id = $3`, userID, profileID, first.PartyID); n != 1 {
		t.Fatalf("users.person_id is not the new owned natural_person")
	}
	// The grant has exactly the U2 shape.
	if n := env.count(t, "grant", `
SELECT count(*) FROM grants g JOIN authz_operations o ON o.id = g.operation_id
 WHERE g.id = $1 AND g.actor_id = $2 AND o.slug = 'manage' AND g.scope = 'owned' AND g.target_id = $3
   AND g.target_type_id IS NULL AND NOT g.include_subtypes AND g.granted_by = $2`,
		first.GrantID, profileID, first.PartyID); n != 1 {
		t.Fatalf("the grant does not have the owned-scope manage shape")
	}

	env.obs.reset()
	second, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("second EnsurePersonalParty: %v", err)
	}
	if second.Created || second.PartyID != first.PartyID || second.PartyUUID != first.PartyUUID || second.GrantID != first.GrantID {
		t.Errorf("second = %+v, want the same ids and Created=false (first %+v)", second, first)
	}
	if got := env.obs.pairs(); len(got) != 0 {
		t.Errorf("a no-op call emitted %v", got)
	}
	if n := env.ownedPersons(t, userID) - before; n != 1 {
		t.Errorf("%d persons created, want 1", n)
	}
	if n := env.ownedManageGrants(t, profileID); n != 1 {
		t.Errorf("%d owned manage grants, want 1", n)
	}
}

func TestEnsurePersonalParty_ReusesTheSignUpPerson(t *testing.T) {
	a := env.register(t)
	ctx := env.authCtx(t, a.tk.access())
	ac, _ := opctx.Auth(ctx)
	person := env.scanInt64(t, "sign-up person", `SELECT person_id FROM mod_users.users WHERE entity_id = $1`, ac.PrincipalID)

	env.obs.reset()
	pp, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	if pp.PartyID != person || !pp.Created {
		t.Errorf("pp = %+v, want the sign-up person and a created grant", pp)
	}
	if got := env.obs.pairs(); !slices.Equal(got, []string{"grant grant"}) {
		t.Errorf("observations = %v, want only grant grant (the person and its link are reused)", got)
	}
	if n := env.ownedPersons(t, ac.PrincipalID); n != 1 {
		t.Errorf("%d owned persons, want 1", n)
	}
	again, err := env.ensurePersonal(ctx)
	if err != nil || again.Created || again.GrantID != pp.GrantID {
		t.Errorf("again = %+v, %v; want a no-op", again, err)
	}
}

func TestEnsurePersonalParty_ReusesAnEqualGrantAndIgnoresOthers(t *testing.T) {
	_, _, profileID, ctx := env.personalUser(t)
	first, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	// A typed owned-scope grant on the same person is a different grant: a
	// later call still finds the untyped one and creates nothing.
	typeID := env.scanInt64(t, "profile type", `SELECT id FROM types WHERE slug = 'profile'`)
	env.exec(t, "typed owned grant", `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by, scope, target_type_id)
SELECT $1, o.id, $2, $1, 'owned', $3 FROM authz_operations o WHERE o.slug = 'read'`, profileID, first.PartyID, typeID)
	again, err := env.ensurePersonal(ctx)
	if err != nil || again.Created || again.GrantID != first.GrantID {
		t.Errorf("again = %+v, %v; want the original grant, nothing created", again, err)
	}
}

func TestEnsurePersonalParty_ConcurrentCallsYieldOnePersonAndOneGrant(t *testing.T) {
	_, userID, profileID, ctx := env.personalUser(t)
	before := env.ownedPersons(t, userID)

	const callers = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []svc.PersonalParty
		errs    []error
	)
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pp, err := env.ensurePersonal(ctx)
			mu.Lock()
			defer mu.Unlock()
			results, errs = append(results, pp), append(errs, err)
		}()
	}
	close(start)
	wg.Wait()

	created := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if results[i].PartyID != results[0].PartyID || results[i].GrantID != results[0].GrantID {
			t.Errorf("caller %d got %+v, want the same ids as %+v", i, results[i], results[0])
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d callers reported Created, want exactly 1", created)
	}
	if got := env.ownedPersons(t, userID) - before; got != 1 {
		t.Errorf("%d persons were created, want 1", got)
	}
	if n := env.ownedManageGrants(t, profileID); n != 1 {
		t.Errorf("%d owned manage grants, want 1", n)
	}
}

func TestEnsurePersonalParty_Refusals(t *testing.T) {
	assertNothingWritten := func(t *testing.T, userID int64) {
		t.Helper()
		if n := env.count(t, "grants", `SELECT count(*) FROM grants WHERE scope = 'owned' AND actor_id IN (SELECT entity_id FROM profiles WHERE holder_id = $1)`, userID); n != 0 {
			t.Errorf("a refused call left %d owned grants", n)
		}
	}

	t.Run("no acting profile", func(t *testing.T) {
		if _, err := env.ensurePersonal(context.Background()); !errors.Is(err, apiresp.ErrUnauthenticated) {
			t.Errorf("err = %v, want ErrUnauthenticated", err)
		}
	})
	t.Run("guest", func(t *testing.T) {
		var guest tokens
		env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).
			wantStatus(t, http.StatusCreated).into(t, &guest)
		ctx := env.authCtx(t, guest.access())
		ac, _ := opctx.Auth(ctx)
		if _, err := env.ensurePersonal(ctx); !errors.Is(err, svc.ErrGuestMustUpgrade) {
			t.Errorf("err = %v, want ErrGuestMustUpgrade", err)
		}
		assertNothingWritten(t, ac.PrincipalID)
	})
	t.Run("managed user", func(t *testing.T) {
		party := env.seedParty(t, "Managed "+uniq("p"))
		managed := env.seedManagedUser(t, party, uniq("m")+"@example.test", testPassword)
		prof, _ := env.seedPartyProfile(t, managed, party, "active")
		ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: managed, ProfileID: prof})
		if _, err := env.ensurePersonal(ctx); !errors.Is(err, svc.ErrPersonalProfileRequired) {
			t.Errorf("err = %v, want ErrPersonalProfileRequired", err)
		}
		assertNothingWritten(t, managed)
	})
	t.Run("party profile of a regular user", func(t *testing.T) {
		a := env.register(t)
		userID := env.principalID(t, a.Email)
		party := env.seedParty(t, "Member "+uniq("p"))
		prof, _ := env.seedPartyProfile(t, userID, party, "active")
		ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: userID, ProfileID: prof})
		if _, err := env.ensurePersonal(ctx); !errors.Is(err, svc.ErrPersonalProfileRequired) {
			t.Errorf("err = %v, want ErrPersonalProfileRequired", err)
		}
		assertNothingWritten(t, userID)
	})
	t.Run("another user's personal profile", func(t *testing.T) {
		a, b := env.register(t), env.register(t)
		actx := env.authCtx(t, a.tk.access())
		bctx := env.authCtx(t, b.tk.access())
		aac, _ := opctx.Auth(actx)
		bac, _ := opctx.Auth(bctx)
		ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: aac.PrincipalID, ProfileID: bac.ProfileID})
		if _, err := env.ensurePersonal(ctx); !errors.Is(err, svc.ErrPersonalProfileRequired) {
			t.Errorf("err = %v, want ErrPersonalProfileRequired", err)
		}
		assertNothingWritten(t, aac.PrincipalID)
		assertNothingWritten(t, bac.PrincipalID)
	})
	t.Run("person not owned", func(t *testing.T) {
		a, b := env.register(t), env.register(t)
		actx := env.authCtx(t, a.tk.access())
		aac, _ := opctx.Auth(actx)
		bPerson := env.scanInt64(t, "b person", `SELECT person_id FROM mod_users.users WHERE entity_id = $1`, env.principalID(t, b.Email))
		env.exec(t, "link a foreign person", `UPDATE mod_users.users SET person_id = $2 WHERE entity_id = $1`, aac.PrincipalID, bPerson)
		if _, err := env.ensurePersonal(actx); !errors.Is(err, svc.ErrPersonalPartyNotOwned) {
			t.Errorf("err = %v, want ErrPersonalPartyNotOwned", err)
		}
		assertNothingWritten(t, aac.PrincipalID)
	})
	t.Run("refusals map to their statuses", func(t *testing.T) {
		var forbidden *svc.AuthError
		if !errors.As(svc.ErrPersonalProfileRequired, &forbidden) || forbidden.Status != http.StatusForbidden {
			t.Errorf("ErrPersonalProfileRequired = %+v, want a 403", forbidden)
		}
		if !errors.Is(svc.ErrPersonalProfileRequired, apiresp.ErrForbidden) {
			t.Error("ErrPersonalProfileRequired must classify as forbidden")
		}
		rec := httptest.NewRecorder()
		apiresp.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), svc.ErrPersonalPartyNotOwned)
		if rec.Code != http.StatusConflict {
			t.Errorf("ErrPersonalPartyNotOwned renders %d, want 409", rec.Code)
		}
	})
}

func TestEnsurePersonalParty_GrantReachesASeatButNotThePersonRecord(t *testing.T) {
	a, _, profileID, ctx := env.personalUser(t)
	pp, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	// Authorization is a fresh context against the committed grant.
	fresh := env.authCtx(t, a.tk.access())
	profileType := env.scanInt64(t, "profile type", `SELECT id FROM types WHERE slug = 'profile'`)
	person := pp.PartyID

	if err := coreAuthz.AuthorizeTypeIn(fresh, env.az, "create", profileType, &person); err != nil {
		t.Fatalf("create a profile in the person party: %v", err)
	}

	// A seat created through InviteInTx is owned by the person party, and the
	// grant reaches it.
	invitee := env.registerVerified(t)
	var seat svc.Profile
	err = env.inTx(fresh, func(tx pgxTx) error {
		var err error
		seat, err = env.profiles.InviteInTx(fresh, tx, svc.InviteInput{PartyID: pp.PartyID, HolderUserID: env.principalID(t, invitee.Email)})
		return err
	})
	if err != nil {
		t.Fatalf("InviteInTx into the person party: %v", err)
	}
	seatOwner := env.scanInt64(t, "seat owner", `SELECT owner_id FROM entities WHERE id = $1`, seat.ID)
	if seatOwner != pp.PartyID {
		t.Fatalf("seat owner = %d, want the person party %d", seatOwner, pp.PartyID)
	}
	seatID := seat.ID
	for _, op := range []string{"read", "update", "manage"} {
		if err := env.az.Authorize(fresh, op, &seatID); err != nil {
			t.Errorf("%s on the seat: %v, want allowed through the owned-scope manage grant", op, err)
		}
	}

	// Mod-core D7: the owned-scope grant never reaches the person record itself.
	if err := env.az.Authorize(fresh, "manage", &person); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("manage on the person record = %v, want forbidden", err)
	}
	// (The user still owns the record, so the own arm lets the personal profile
	// update it; `manage` does not confer through ownership, so only the
	// grant could have reached it.)

	// Another user's personal profile does not benefit from the grant.
	other := env.register(t)
	octx := env.authCtx(t, other.tk.access())
	if err := coreAuthz.AuthorizeTypeIn(octx, env.az, "create", profileType, &person); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("another user creating in the person party = %v, want forbidden", err)
	}
	if n := env.ownedManageGrants(t, profileID); n != 1 {
		t.Errorf("%d owned manage grants, want 1", n)
	}
}

// TestEnsurePersonalParty_BillingB6Sequence is decision U28: ensure in its own
// transaction and commit, then authorize in a fresh context through the
// production Authorizer, which reads grants through its own pool.
func TestEnsurePersonalParty_BillingB6Sequence(t *testing.T) {
	a, _, _, ctx := env.personalUser(t)
	profileType := env.scanInt64(t, "profile type", `SELECT id FROM types WHERE slug = 'profile'`)

	// 1. Ensure in its own transaction, committed.
	pp, err := env.ensurePersonal(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	// 2. In a fresh context, authorizing against the new party succeeds.
	fresh := env.authCtx(t, a.tk.access())
	party := pp.PartyID
	if err := coreAuthz.AuthorizeTypeIn(fresh, env.az, "create", profileType, &party); err != nil {
		t.Fatalf("authorize after the commit: %v", err)
	}
	// 3. A repeated sequence is a no-op.
	again, err := env.ensurePersonal(fresh)
	if err != nil || again.Created || again.PartyID != pp.PartyID || again.GrantID != pp.GrantID {
		t.Fatalf("repeat = %+v, %v; want a no-op", again, err)
	}
	if err := coreAuthz.AuthorizeTypeIn(env.authCtx(t, a.tk.access()), env.az, "create", profileType, &party); err != nil {
		t.Fatalf("authorize after the repeat: %v", err)
	}
}

// TestEnsurePersonalParty_GrantInvisibleBeforeCommit documents the limitation
// of decision U28: inside the still-open transaction, the same authorization is
// refused, because the grant is not yet visible to the Authorizer's pool.
func TestEnsurePersonalParty_GrantInvisibleBeforeCommit(t *testing.T) {
	a, _, _, ctx := env.personalUser(t)
	profileType := env.scanInt64(t, "profile type", `SELECT id FROM types WHERE slug = 'profile'`)

	tx, err := env.pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	pp, err := env.personal.EnsurePersonalParty(ctx, tx)
	if err != nil {
		t.Fatalf("EnsurePersonalParty: %v", err)
	}
	fresh := env.authCtx(t, a.tk.access())
	party := pp.PartyID
	if err := coreAuthz.AuthorizeTypeIn(fresh, env.az, "create", profileType, &party); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("authorize before the commit = %v, want forbidden (the grant is invisible to the pool)", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := coreAuthz.AuthorizeTypeIn(fresh, env.az, "create", profileType, &party); err != nil {
		t.Errorf("authorize after the commit: %v", err)
	}
}

// TestEnsurePersonalParty_RollbackLeavesNothing: the call runs entirely in the
// caller's transaction, so a rollback removes the person, the link and the
// grant.
func TestEnsurePersonalParty_RollbackLeavesNothing(t *testing.T) {
	_, userID, profileID, ctx := env.personalUser(t)
	before := env.ownedPersons(t, userID)
	rollback := errors.New("rollback")
	err := pgx.BeginFunc(context.Background(), env.pool, func(tx pgx.Tx) error {
		if _, err := env.personal.EnsurePersonalParty(ctx, tx); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("err = %v, want the rollback sentinel", err)
	}
	if got := env.ownedPersons(t, userID); got != before {
		t.Errorf("%d owned persons after rollback, want %d", got, before)
	}
	if n := env.ownedManageGrants(t, profileID); n != 0 {
		t.Errorf("%d owned manage grants after rollback, want 0", n)
	}
	if n := env.count(t, "linked", `SELECT count(*) FROM mod_users.users WHERE entity_id = $1 AND person_id IS NOT NULL`, userID); n != 0 {
		t.Errorf("the person link survived the rollback")
	}
}
