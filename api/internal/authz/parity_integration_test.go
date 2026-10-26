//go:build integration

package authz_test

// parity_integration_test.go proves that the production Authorizer's
// single-row answer agrees with mod-core's generated
// accessible_<slug>_ids_for_actor list functions: the single-row half of the
// two-sided check (the list half lives in mod-core and mod-authz). The list
// side is the production SQL installed through setup.ApplyFuncs and the
// grant-table generator with the slug list of the dev server (user and profile
// included, per U18); nothing here copies it.
//
// The parity contract: for every actor, every operation in the seed, every
// installed slug S and every fixture entity t whose type is S or descends from
// S, Authorize(op, t) allows exactly when t is among the entities
// accessible_S_ids_for_actor(actor, closure(op)) lists. An entity that is not
// of type S must never be listed by S's function.
//
// The fixtures are the owned-scope and own-arm ones: owned-scope grants with a
// NULL type, an exact type and include_subtypes, held directly and through
// nested actor groups; entity grants directly and through a target group;
// type-entity grants directly and through a type-only target group; a wildcard;
// personal profiles that own entities (and their own user record) and party,
// service account and anonymous actors that do not. Targets include the
// owners' own records, which an owned-scope grant must never reach.
//
// Rule-to-test mapping:
//
//	whole matrix                       TestInteg_Parity_Matrix
//	own arm and ownership_confers      TestInteg_Parity_OwnArm
//	owned-scope arm                    TestInteg_Parity_OwnedScope
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	authzsetup "github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/opctx"
)

// parityBaseSlugs is the dev server's access-function slug list (U18: user,
// profile and organization join it) without "tag", which mod-users registers no
// type for, plus the roots "entity" and "type" so that every fixture entity,
// including type entities and groups, is a candidate of at least one function.
var parityBaseSlugs = []string{
	"natural_person", "organization", "user", "profile", "service_account", "legal_entity",
	"authz_actor_group", "authz_target_group", "entity", "type",
}

type parityActor struct {
	name    string
	profile int64
	ctx     context.Context
}

type parityTarget struct {
	label string
	id    int64
}

type parityWorld struct {
	slugs   []string
	ops     []string
	actors  []parityActor
	targets []parityTarget
	ids     map[string]int64
}

func (w *parityWorld) actor(name string) parityActor {
	for _, a := range w.actors {
		if a.name == name {
			return a
		}
	}
	panic("unknown parity actor " + name)
}

func (w *parityWorld) target(label string) int64 {
	id, ok := w.ids[label]
	if !ok {
		panic("unknown parity target " + label)
	}
	return id
}

// parityOps lists every operation slug of the seed.
func parityOps(t *testing.T) []string {
	t.Helper()
	rows, err := integPool.Query(context.Background(), `SELECT slug FROM authz_operations ORDER BY id`)
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	defer rows.Close()
	var ops []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan operation: %v", err)
		}
		ops = append(ops, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("operations rows: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("no operations seeded")
	}
	return ops
}

func newParityWorld(t *testing.T) *parityWorld {
	t.Helper()
	ty := registerOwnedTypes(t)
	parentSlug, childSlug := typeSlugForID(t, ty.parent), typeSlugForID(t, ty.child)
	w := &parityWorld{
		slugs: append(slices.Clone(parityBaseSlugs), parentSlug, childSlug),
		ops:   parityOps(t),
		ids:   map[string]int64{},
	}
	if err := authzsetup.ApplyFuncs(context.Background(), integPool, authzsetup.NewGrantTableGenerator(), w.slugs); err != nil {
		t.Fatalf("apply access functions: %v", err)
	}

	// Owners: two organizations and two bring-your-own users.
	orgA := seedOrg(t, "Parity Org A")
	orgB := seedOrg(t, "Parity Org B")
	u1, personal := seedUserEntity(t)
	u2, personal2 := seedUserEntity(t)

	// Actors.
	addActor := func(name string, profile int64) {
		ctx := actorCtx(profile)
		w.actors = append(w.actors, parityActor{name: name, profile: profile, ctx: ctx})
	}
	addActor("personal", personal)
	u3, grantee := seedUserEntity(t)
	addActor("personal-owned-grants", grantee)
	u4, _ := seedUserEntity(t)
	var partyProfile int64
	fxMustTx(t, func(tx pgx.Tx) { partyProfile = fxPartyProfile(t, tx, u4, orgA, "active") })
	addActor("party", partyProfile)
	_, member := seedUserEntity(t)
	addActor("group-member", member)
	_, saProfile := seedServiceAccount(t, orgA)
	addActor("service-account", saProfile)
	anonProfile := anonymousProfileID(t)
	w.actors = append(w.actors, parityActor{name: "anonymous", profile: anonProfile, ctx: opctx.WithAuthContext(context.Background(),
		opctx.AuthContext{PrincipalID: ownerHolder(t, anonProfile), ProfileID: anonProfile, Anonymous: true})})
	operator := seedOperator(t)
	addActor("operator", operator)
	manager := seedOperator(t, "manage")
	addActor("operator-wildcard-manage", manager)
	_, stranger := seedUserEntity(t)
	addActor("stranger", stranger)
	_, nobody := seedUserEntity(t) // no grants: it owns only what its own user owns
	addActor("nobody", nobody)

	// Entities, owned by the two organizations and the two users.
	owners := map[string]int64{"orgA": orgA, "orgB": orgB, "u1": u1, "u2": u2}
	for name, owner := range owners {
		w.ids[name+"/parent"] = seedOwnedInstance(t, ty.parent, owner)
		w.ids[name+"/child"] = seedOwnedInstance(t, ty.child, owner)
		w.ids[name+"/org"] = seedOwnedOrg(t, owner, "Parity Org owned by "+name)
		w.ids[name] = owner // the owner's own record
	}
	w.ids["free/parent"] = seedInstanceOfType(t, ty.parent)
	w.ids["free/org"] = seedUnownedOrg(t, "Parity Free Org")
	w.ids["free/org2"] = seedUnownedOrg(t, "Parity Free Org 2")
	w.ids["free/parent2"] = seedInstanceOfType(t, ty.parent)
	w.ids["u3"] = u3
	w.ids["u3/org"] = seedOwnedOrg(t, u3, "Parity Org owned by u3")
	w.ids["u4"] = u4

	// Type entities and groups.
	typeEntOrg := typeEntityIDForSlug(t, "organization")
	typeEntParent := typeEntityIDForSlug(t, parentSlug)
	typeEntChild := typeEntityIDForSlug(t, childSlug)
	typeEntSentinel := typeEntityIDForSlug(t, "type")
	w.ids["type/organization"] = typeEntOrg
	w.ids["type/parent"] = typeEntParent
	w.ids["type/child"] = typeEntChild
	w.ids["type/type"] = typeEntSentinel
	instanceGroup := seedTargetGroup(t, "parity-instances")
	addTargetGroupMember(t, instanceGroup, w.ids["free/org2"])
	addTargetGroupMember(t, instanceGroup, w.ids["free/parent2"])
	typeGroup := seedTargetGroup(t, "parity-types")
	addTargetGroupMember(t, typeGroup, typeEntChild)
	w.ids["group/instances"], w.ids["group/types"] = instanceGroup, typeGroup

	inner := seedActorGroup(t, "parity-inner")
	outer := seedActorGroup(t, "parity-outer")
	addActorGroupMember(t, inner, member)
	addActorGroupMember(t, outer, inner)
	w.ids["agroup/inner"], w.ids["agroup/outer"] = inner, outer

	// Grants.
	// personal: an entity grant on one entity and one on the instance group.
	targetedGrant(t, personal, w.ids["orgB/parent"], "read")
	targetedGrant(t, personal, instanceGroup, "update")
	// personal-owned-grants: NULL type, exact, include_subtypes, and a grant on
	// a self-owned user (reaches what the user owns, never the user itself).
	ownedGrant(t, grantee, orgA, "read")
	ownedGrantTyped(t, grantee, orgB, "update", &ty.parent, false)
	ownedGrantTyped(t, grantee, orgB, "delete", &ty.parent, true)
	ownedGrantTyped(t, grantee, orgA, "grant", &ty.child, false)
	ownedGrant(t, grantee, u2, "sread")
	ownedGrantTyped(t, grantee, u1, "list", &ty.other, false)
	// party and service account: owned-scope grants like any other profile.
	ownedGrant(t, partyProfile, orgA, "update")
	ownedGrantTyped(t, saProfile, orgA, "read", &ty.parent, true)
	// group-member inherits the outer group's grants through the nested groups.
	ownedGrantTyped(t, outer, orgA, "read", &ty.parent, true)
	ownedGrant(t, outer, u1, "update")
	targetedGrant(t, outer, instanceGroup, "update")
	// operator: wildcard read, type-entity update on organization, a type-only
	// group delete (reaching the child type's instances), an entity grant, a
	// type-entity login on the parent, and a grant on the sentinel type entity
	// that must reach no type entity.
	seedWildcardGrant(t, operator, "read")
	targetedGrant(t, operator, typeEntOrg, "update")
	targetedGrant(t, operator, typeGroup, "delete")
	targetedGrant(t, operator, w.ids["free/parent"], "grant")
	targetedGrant(t, operator, typeEntParent, "login")
	targetedGrant(t, operator, typeEntSentinel, "sread")
	// stranger: an explicit owned-scope impersonate grant (control for the
	// "ownership never confers impersonate" assertions).
	ownedGrant(t, stranger, orgB, "impersonate")

	// Every named entity is a target, plus each actor's profile.
	for label, id := range w.ids {
		w.targets = append(w.targets, parityTarget{label: label, id: id})
	}
	for _, a := range w.actors {
		w.targets = append(w.targets, parityTarget{label: "profile:" + a.name, id: a.profile})
	}
	w.targets = append(w.targets, parityTarget{label: "profile:u2", id: personal2})
	slices.SortFunc(w.targets, func(a, b parityTarget) int { return strings.Compare(a.label, b.label) })
	return w
}

// allows runs Authorize and reports whether it allowed; any error other than
// ErrForbidden fails the test.
func (w *parityWorld) allows(t *testing.T, a parityActor, op string, target int64) bool {
	t.Helper()
	err := integAZ.Authorize(a.ctx, op, &target)
	switch {
	case err == nil:
		return true
	case errors.Is(err, apiresp.ErrForbidden):
		return false
	default:
		t.Fatalf("Authorize(%s, %d) as %s: unexpected error %v", op, target, a.name, err)
		return false
	}
}

// listed returns the entity ids accessible_<slug>_ids_for_actor lists for the
// actor and operation closure.
func (w *parityWorld) listed(t *testing.T, slug string, a parityActor, op string) map[int64]bool {
	t.Helper()
	opIDs, err := integOpReg.SatisfiedBy(op)
	if err != nil {
		t.Fatalf("SatisfiedBy(%q): %v", op, err)
	}
	return accessibleIDs(t, slug, a.profile, opIDs)
}

// memberships reports, per installed slug, which targets are of a type that is
// or descends from the slug.
func (w *parityWorld) memberships(t *testing.T) map[string]map[int64]bool {
	t.Helper()
	out := map[string]map[int64]bool{}
	for _, slug := range w.slugs {
		m := map[int64]bool{}
		for _, tg := range w.targets {
			m[tg.id] = typeIsUnderSlug(t, tg.id, slug)
		}
		out[slug] = m
	}
	return out
}

// compare asserts parity for the given actors, operations and slugs and
// returns how many single-row decisions were allowed per actor.
func (w *parityWorld) compare(t *testing.T, actors []parityActor, ops, slugs []string) map[string]int {
	t.Helper()
	member := w.memberships(t)
	allowedPerActor := map[string]int{}
	mismatches := 0
	report := func(format string, args ...any) {
		mismatches++
		if mismatches <= 40 {
			t.Errorf(format, args...)
		}
	}
	for _, a := range actors {
		for _, op := range ops {
			decision := map[int64]bool{}
			for _, tg := range w.targets {
				decision[tg.id] = w.allows(t, a, op, tg.id)
				if decision[tg.id] {
					allowedPerActor[a.name]++
				}
			}
			for _, slug := range slugs {
				list := w.listed(t, slug, a, op)
				for _, tg := range w.targets {
					if !member[slug][tg.id] {
						if list[tg.id] {
							report("actor %s op %s: accessible_%s_ids_for_actor listed %s (%d), which is not of that type", a.name, op, slug, tg.label, tg.id)
						}
						continue
					}
					if decision[tg.id] != list[tg.id] {
						report("actor %s op %s slug %s target %s (%d): Authorize allows=%v, generated list contains=%v",
							a.name, op, slug, tg.label, tg.id, decision[tg.id], list[tg.id])
					}
				}
			}
		}
	}
	return allowedPerActor
}

// TestInteg_Parity_Matrix is the whole matrix: every actor, every operation of
// the seed, every installed slug, every fixture entity.
func TestInteg_Parity_Matrix(t *testing.T) {
	w := newParityWorld(t)
	allowed := w.compare(t, w.actors, w.ops, w.slugs)

	// The matrix must not be vacuous: every actor that holds or owns something
	// is allowed something, and the anonymous actor is allowed nothing.
	for _, a := range w.actors {
		switch {
		case a.name == "anonymous":
			if allowed[a.name] != 0 {
				t.Errorf("actor %s was allowed %d decisions, want none", a.name, allowed[a.name])
			}
		case allowed[a.name] == 0:
			t.Errorf("actor %s was allowed nothing across the matrix; the fixture is vacuous for it", a.name)
		}
	}
	t.Logf("parity: %d actors x %d operations x %d slugs x %d targets compared", len(w.actors), len(w.ops), len(w.slugs), len(w.targets))
}

// TestInteg_Parity_OwnArm pins the own arm on both sides: a personal profile is
// allowed, and listed, for every conferring operation over what its user owns,
// its own user record included; and for no non-conferring operation.
// Party, service account and anonymous actors get nothing from ownership.
func TestInteg_Parity_OwnArm(t *testing.T) {
	w := newParityWorld(t)
	confers, nonConfers := conferralByOperation(t)
	personal := w.actor("personal")
	owned := map[string]int64{
		"u1/org": w.target("u1/org"), "u1/parent": w.target("u1/parent"), "u1/child": w.target("u1/child"),
		"u1 (the user's own record)": w.target("u1"), "the personal profile itself": personal.profile,
	}

	for _, op := range confers {
		for label, id := range owned {
			if !w.allows(t, personal, op, id) {
				t.Errorf("own arm: personal profile must be allowed %s over %s", op, label)
			}
		}
		if !w.listed(t, "organization", personal, op)[w.target("u1/org")] {
			t.Errorf("own arm: accessible_organization_ids_for_actor must list the personal profile's own organization for %s", op)
		}
		if !w.listed(t, "user", personal, op)[w.target("u1")] {
			t.Errorf("own arm: accessible_user_ids_for_actor must list the personal profile's own user record for %s", op)
		}
		if !w.listed(t, "profile", personal, op)[personal.profile] {
			t.Errorf("own arm: accessible_profile_ids_for_actor must list the personal profile's own profile for %s", op)
		}
	}
	for _, op := range nonConfers {
		for label, id := range owned {
			if w.allows(t, personal, op, id) {
				t.Errorf("non-conferring %s was allowed through ownership over %s", op, label)
			}
		}
		if w.listed(t, "organization", personal, op)[w.target("u1/org")] {
			t.Errorf("accessible_organization_ids_for_actor listed an owned organization for non-conferring %s", op)
		}
	}

	// Actors with no own subject: nothing from ownership on either side.
	for _, name := range []string{"party", "service-account", "anonymous"} {
		a := w.actor(name)
		for _, op := range confers {
			for _, label := range []string{"u1/org", "u1"} {
				if w.allows(t, a, op, w.target(label)) {
					t.Errorf("%s: %s over %s allowed without an own subject", name, op, label)
				}
			}
			if w.listed(t, "organization", a, op)[w.target("u1/org")] {
				t.Errorf("%s: accessible_organization_ids_for_actor listed a user's organization for %s", name, op)
			}
		}
	}

	// The full parity comparison for the ownership-only actors.
	w.compare(t, []parityActor{w.actor("personal"), w.actor("nobody"), w.actor("party")}, w.ops, w.slugs)
}

// TestInteg_Parity_OwnedScope pins the owned-scope arm on both sides over the
// owned-scope fixtures: the typed grants of personal-owned-grants and the
// grants held through the nested actor groups by group-member.
func TestInteg_Parity_OwnedScope(t *testing.T) {
	w := newParityWorld(t)
	grantee, member := w.actor("personal-owned-grants"), w.actor("group-member")

	// Pinned expectations: a failure here is a fixture or engine drift, not only
	// a parity gap.
	for _, tc := range []struct {
		actor  parityActor
		op     string
		target string
		want   bool
	}{
		{grantee, "read", "orgA/parent", true},   // NULL type
		{grantee, "read", "orgA", false},         // the owner's own record
		{grantee, "update", "orgB/parent", true}, // exact type
		{grantee, "update", "orgB/child", false}, // exact type, subtype instance
		{grantee, "delete", "orgB/child", true},  // include_subtypes
		{grantee, "delete", "orgB/org", false},   // include_subtypes, unrelated type
		{grantee, "grant", "orgA/child", true},   // exact child
		{grantee, "grant", "orgA/parent", false}, // exact child, parent instance
		{grantee, "sread", "u2/org", true},       // owner is a self-owned user
		{grantee, "sread", "u2", false},          // ... whose own record is not reached
		{grantee, "read", "orgB/parent", true},   // update implies read
		{member, "read", "orgA/child", true},     // through the nested groups
		{member, "read", "orgB/parent", false},   // another owner
		{member, "update", "u1/org", true},       // NULL type through the outer group
		{member, "update", "u1", false},          // the owner's own record
	} {
		if got := w.allows(t, tc.actor, tc.op, w.target(tc.target)); got != tc.want {
			t.Errorf("%s %s %s: Authorize allows=%v, want %v", tc.actor.name, tc.op, tc.target, got, tc.want)
		}
	}

	w.compare(t, []parityActor{grantee, member, w.actor("party"), w.actor("service-account"), w.actor("stranger")}, w.ops, w.slugs)
}
