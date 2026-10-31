//go:build integration

package authz_test

// row_filter_parity_integration_test.go proves the list side of the
// owning-party gate: the access functions installed through
// authz.AccessFuncGenerator (mod-core's grant-table body wrapped by
// setup.NewRowFilterGenerator in mod_users.access_row_permitted) list exactly
// the rows the production Authorizer allows, for every context that exercises
// the gate, compared through mod-core's paritytest.Compare inside a
// transaction bound with setup.BindAuthInputs.
//
// The world: two restrictive parties, "acr" (credential_rule any, min_acr 2)
// and "rule" (credential_rule authorized, min_acr 1). Each contributes its own
// record (decision F), an organization it owns reached through an entity
// grant, one reached only through an owned-scope grant, and a managed user it
// owns. The ungated controls are a party without a policy and an organization
// it owns, an unowned organization, and each personal actor's own
// organization and user record (the own arm). Every actor is "armed": it holds
// read through entity and owned-scope grants, and is a member of an
// operator-owned actor group holding a wildcard grant and a type-entity revoke
// on the organization type, so each arm reaches gated rows:
//
//	read    entity, owned-scope and own arms
//	grant   wildcard arm (no seeded operation implies grant but manage)
//	revoke  type-entity arm on the organization type, and the own arm
//
// Rule-to-test mapping:
//
//	parity over the gate's contexts            TestInteg_RowFilter_Parity
//	all-NULL and marker conformance (8FYk)     TestInteg_RowFilter_PredicateConformance
//	an unbound list fails closed               TestInteg_RowFilter_UnboundList
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	authzsetup "github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/authz/setup/paritytest"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// rfSlugs are the list functions the comparisons cover; "entity" is the root,
// so every candidate is in its subtree.
var rfSlugs = []string{"organization", "legal_entity", "user", "entity"}

// rfOps is one operation per arm family; none implies another.
var rfOps = []string{"read", "grant", "revoke"}

type rfCandidate struct {
	label string
	id    int64
	// gate is the party whose policy gates the row (its owner, or the row
	// itself for a party record); 0 for an ungated row.
	gate int64
	// org marks an organization, which the type-entity revoke reaches.
	org bool
	// own is the personal profile whose own arm reaches the row; 0 for none.
	own int64
	// entityGrant marks a row armed actors reach through an entity grant.
	entityGrant bool
}

type rfWorld struct {
	acrParty, ruleParty, openParty int64
	group                          int64
	candidates                     []rfCandidate
}

func newRFWorld(t *testing.T) *rfWorld {
	t.Helper()
	gen, err := authz.AccessFuncGenerator()
	if err != nil {
		t.Fatalf("access-function generator: %v", err)
	}
	if err := authzsetup.ApplyFuncs(context.Background(), integPool, gen, rfSlugs); err != nil {
		t.Fatalf("apply access functions: %v", err)
	}

	w := &rfWorld{
		acrParty:  seedOrg(t, "RF ACR Party "+fxTag("p")),
		ruleParty: seedOrg(t, "RF Rule Party "+fxTag("p")),
		openParty: seedOrg(t, "RF Open Party "+fxTag("p")),
	}
	fxMustTx(t, func(tx pgx.Tx) {
		fxPolicy(t, tx, w.acrParty, "any", nil, 2)
		fxPolicy(t, tx, w.ruleParty, "authorized", nil, 1)
	})
	for _, p := range []struct {
		name string
		id   int64
	}{{"acr", w.acrParty}, {"rule", w.ruleParty}} {
		party := p.id
		var managed int64
		fxMustTx(t, func(tx pgx.Tx) { managed, _ = fxUser(t, tx, userOpts{ManagingPartyID: &party}) })
		w.candidates = append(w.candidates,
			rfCandidate{label: p.name + "/party", id: party, gate: party, org: true, entityGrant: true},
			rfCandidate{label: p.name + "/entity", id: seedOwnedOrg(t, party, "RF Entity "+fxTag("o")), gate: party, org: true, entityGrant: true},
			rfCandidate{label: p.name + "/owned", id: seedOwnedOrg(t, party, "RF Owned "+fxTag("o")), gate: party, org: true},
			rfCandidate{label: p.name + "/managed-user", id: managed, gate: party},
		)
	}
	w.candidates = append(w.candidates,
		rfCandidate{label: "open/party", id: w.openParty, org: true, entityGrant: true},
		rfCandidate{label: "open/owned", id: seedOwnedOrg(t, w.openParty, "RF Open Owned "+fxTag("o")), org: true},
		rfCandidate{label: "free/org", id: seedUnownedOrg(t, "RF Free "+fxTag("o")), org: true, entityGrant: true},
	)

	// The operator-owned actor group: wildcard and type-entity grants are
	// accepted only for operator actors, and a non-operator member inherits
	// them (group composition is the service layer's check, not the
	// database's), so a gated collaborator reaches rows through both arms.
	w.group = seedActorGroup(t, "rf-group-"+fxTag("g"))
	seedWildcardGrant(t, w.group, "grant")
	targetedGrant(t, w.group, typeEntityIDForSlug(t, "organization"), "revoke")
	return w
}

// arm gives profile the world's grants (see the file header).
func (w *rfWorld) arm(t *testing.T, profile int64) {
	t.Helper()
	for _, c := range w.candidates {
		if c.entityGrant {
			targetedGrant(t, profile, c.id, "read")
		}
	}
	for _, owner := range []int64{w.acrParty, w.ruleParty, w.openParty} {
		ownedGrant(t, profile, owner, "read")
	}
	addActorGroupMember(t, w.group, profile)
}

// addOwn adds the personal profile's own organization and user record.
func (w *rfWorld) addOwn(t *testing.T, profile int64) {
	t.Helper()
	w.candidates = append(w.candidates,
		rfCandidate{label: fmt.Sprintf("own-org/%d", profile), id: seedOwnedOrg(t, profile, "RF Own "+fxTag("o")), org: true, own: profile},
		rfCandidate{label: fmt.Sprintf("own-user/%d", profile), id: ownerSubject(t, profile), own: profile},
	)
}

// collaborator seeds an armed outside collaborator (a user acting through its
// personal profile) with a password credential.
func (w *rfWorld) collaborator(t *testing.T) (user, profile, pw int64) {
	t.Helper()
	user, profile = seedUserEntity(t)
	w.arm(t, profile)
	w.addOwn(t, profile)
	return user, profile, fxPassword(t, integPool, user, nil)
}

func (w *rfWorld) ids() []int64 {
	out := make([]int64, 0, len(w.candidates))
	for _, c := range w.candidates {
		out = append(out, c.id)
	}
	return out
}

func (w *rfWorld) candidate(label string) rfCandidate {
	for _, c := range w.candidates {
		if c.label == label {
			return c
		}
	}
	panic("unknown row filter candidate " + label)
}

// rfScenario is one request context and what the gate decides for it.
type rfScenario struct {
	name  string
	ctx   context.Context
	actor int64
	// pass is, per restrictive party, whether the context meets its gate.
	pass map[int64]bool
	// none marks a context that reaches nothing (the anonymous one).
	none bool
}

// reaches reports whether an armed actor reaches c for op through some arm,
// before any gate.
func reaches(op string, actor int64, c rfCandidate) bool {
	switch op {
	case "read":
		return c.own == 0 || c.own == actor
	case "grant":
		return true
	case "revoke":
		return c.org || (c.own != 0 && c.own == actor)
	}
	panic("unknown row filter operation " + op)
}

func (sc rfScenario) want(op string, c rfCandidate) bool {
	if sc.none {
		return false
	}
	return reaches(op, sc.actor, c) && (c.gate == 0 || sc.pass[c.gate])
}

// rfQuerier is a pool or a transaction.
type rfQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// listOn runs accessible_<slug>_ids_for_actor on q. slug comes from rfSlugs.
func listOn(t *testing.T, q rfQuerier, slug string, actor int64, op string) map[int64]bool {
	t.Helper()
	opIDs, err := integOpReg.SatisfiedBy(op)
	if err != nil {
		t.Fatalf("SatisfiedBy(%q): %v", op, err)
	}
	rows, err := q.Query(context.Background(), fmt.Sprintf(`SELECT entity_id FROM accessible_%s_ids_for_actor($1, $2::int[])`, slug), actor, opIDs)
	if err != nil {
		t.Fatalf("list %s: %v", slug, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("list %s: %v", slug, err)
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// boundList lists in a transaction bound with ctx's authentication inputs,
// the way production list paths must.
func boundList(t *testing.T, ctx context.Context, slug, op string) map[int64]bool {
	t.Helper()
	actor, ok := opctx.ProfileID(ctx)
	if !ok {
		t.Fatal("boundList: ctx carries no AuthContext")
	}
	tx, err := integPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck // read-only, always discarded
	if err := authzsetup.BindAuthInputs(ctx, tx); err != nil {
		t.Fatalf("BindAuthInputs: %v", err)
	}
	return listOn(t, tx, slug, actor, op)
}

// unboundList lists in a transaction on which BindAuthInputs was never called.
func unboundList(t *testing.T, actor int64, slug, op string) map[int64]bool {
	t.Helper()
	tx, err := integPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck // read-only, always discarded
	return listOn(t, tx, slug, actor, op)
}

// check runs paritytest.Compare for every operation and pins the bound list
// to the scenario's expectation, so neither side can pass vacuously.
func (w *rfWorld) check(t *testing.T, sc rfScenario) {
	t.Helper()
	listedSomething := false
	for _, op := range rfOps {
		opIDs, err := integOpReg.SatisfiedBy(op)
		if err != nil {
			t.Fatalf("SatisfiedBy(%q): %v", op, err)
		}
		mismatches, err := paritytest.Compare(sc.ctx, integPool, integAZ, op, opIDs, rfSlugs, w.ids())
		if err != nil {
			t.Fatalf("%s: Compare(%s): %v", sc.name, op, err)
		}
		for _, m := range mismatches {
			t.Errorf("%s: %s: %s", sc.name, op, m)
		}

		listed := boundList(t, sc.ctx, "entity", op)
		for _, c := range w.candidates {
			want := sc.want(op, c)
			if listed[c.id] != want {
				t.Errorf("%s: %s: accessible_entity_ids_for_actor lists %s (%d) = %v, want %v",
					sc.name, op, c.label, c.id, listed[c.id], want)
			}
			listedSomething = listedSomething || listed[c.id]
		}
	}
	if !sc.none && !listedSomething {
		t.Errorf("%s: listed no candidate; the scenario is vacuous", sc.name)
	}
}

// rfImpersonation inserts an administrator's parent session (stored acr, one
// factor) and an impersonation of target's personal profile through the
// recorded acting profile (nil: a profile the administrator holds), and
// returns the impersonating context.
func rfImpersonation(t *testing.T, admin int64, acting *int64, adminCred int64, acr int, targetUser, targetProfile int64) context.Context {
	t.Helper()
	parent, _ := fxSession(t, integPool, admin, sessionOpts{ACR: acr})
	fxFactor(t, integPool, parent, adminCred, nil)
	child, _ := fxSession(t, integPool, targetUser, sessionOpts{ACR: acr, ParentID: &parent,
		ImpersonateProfile: &targetProfile, ImpersonatorProfile: acting})
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: targetUser, ProfileID: targetProfile, SessionID: child,
		ImpersonatorPrincipalID: admin, ACR: int16(acr), AuthTime: time.Now(),
	})
}

func confirmCollaborator(t *testing.T, profile, party int64) {
	t.Helper()
	fxExec(t, integPool, "confirm collaborator",
		`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
		profile, party)
}

// TestInteg_RowFilter_Parity compares the hooked list functions with the
// Authorizer for every context the owning-party gate distinguishes.
func TestInteg_RowFilter_Parity(t *testing.T) {
	w := newRFWorld(t)
	acr, rule := w.acrParty, w.ruleParty
	passes := func(acrOK, ruleOK bool) map[int64]bool { return map[int64]bool{acr: acrOK, rule: ruleOK} }

	// Seed every actor first so each comparison sees the full candidate set.
	belowUser, belowProfile, belowPw := w.collaborator(t)
	authorizeCredential(t, belowPw, rule, belowProfile)
	unauthUser, unauthProfile, unauthPw := w.collaborator(t)
	meetUser, meetProfile, meetPw := w.collaborator(t)
	authorizeCredential(t, meetPw, rule, meetProfile)
	confUser, confProfile, confPw := w.collaborator(t)
	confirmCollaborator(t, confProfile, acr)
	confirmCollaborator(t, confProfile, rule)
	opUser, opProfile := seedOperatorUser(t)
	w.arm(t, opProfile)
	opPw := fxPassword(t, integPool, opUser, nil)
	targetUser, targetProfile, _ := w.collaborator(t)

	impOpUser, impOpProfile := seedOperatorUser(t)
	impOpPw := fxPassword(t, integPool, impOpUser, nil)
	impAdmin, _ := seedUserEntity(t)
	impAdminPw := fxPassword(t, integPool, impAdmin, nil)
	confinedAdmin, _ := seedUserEntity(t)
	confinedPw := fxPassword(t, integPool, confinedAdmin, nil)
	authorizeCredential(t, confinedPw, rule, targetProfile)
	elsewhere := seedOrg(t, "RF Elsewhere "+fxTag("p"))

	anon := anonymousProfileID(t)

	t.Run("collaborator below the owning party's min_acr", func(t *testing.T) {
		w.check(t, rfScenario{name: "below min_acr", ctx: sessionCtx(t, belowUser, belowProfile, 1, belowPw),
			actor: belowProfile, pass: passes(false, true)})
	})
	t.Run("collaborator whose credential is not authorized", func(t *testing.T) {
		w.check(t, rfScenario{name: "credential not authorized", ctx: sessionCtx(t, unauthUser, unauthProfile, 2, unauthPw),
			actor: unauthProfile, pass: passes(true, false)})
	})
	t.Run("collaborator meeting the policy", func(t *testing.T) {
		w.check(t, rfScenario{name: "meeting the policy", ctx: sessionCtx(t, meetUser, meetProfile, 2, meetPw),
			actor: meetProfile, pass: passes(true, true)})
	})
	t.Run("confirmation exempts the credential rule, never min_acr", func(t *testing.T) {
		w.check(t, rfScenario{name: "confirmed collaborator", ctx: sessionCtx(t, confUser, confProfile, 1, confPw),
			actor: confProfile, pass: passes(false, true)})
	})
	t.Run("operator profile (decision D)", func(t *testing.T) {
		w.check(t, rfScenario{name: "operator", ctx: sessionCtx(t, opUser, opProfile, 1, opPw),
			actor: opProfile, pass: passes(true, true)})
	})
	t.Run("operator impersonation (decisions G and J)", func(t *testing.T) {
		// The recorded operator acting profile skips the credential rule, never
		// min_acr: the parent session's acr 1 is below the acr party's 2.
		w.check(t, rfScenario{name: "operator impersonation",
			ctx:   rfImpersonation(t, impOpUser, &impOpProfile, impOpPw, 1, targetUser, targetProfile),
			actor: targetProfile, pass: passes(false, true)})
	})
	t.Run("non-operator impersonation (decision G)", func(t *testing.T) {
		// The administrator's own parent-session factors must meet the policy;
		// its unauthorized password fails the credential rule.
		w.check(t, rfScenario{name: "non-operator impersonation",
			ctx:   rfImpersonation(t, impAdmin, nil, impAdminPw, 2, targetUser, targetProfile),
			actor: targetProfile, pass: passes(true, false)})
	})
	t.Run("non-operator impersonation with a confined parent factor (decision L)", func(t *testing.T) {
		ctx := rfImpersonation(t, confinedAdmin, nil, confinedPw, 2, targetUser, targetProfile)
		w.check(t, rfScenario{name: "unconfined parent factor", ctx: ctx, actor: targetProfile, pass: passes(true, true)})
		confine(t, confinedPw, elsewhere)
		w.check(t, rfScenario{name: "confined parent factor", ctx: ctx, actor: targetProfile, pass: passes(false, false)})
	})
	t.Run("system actor", func(t *testing.T) {
		ctx := opctx.WithSystemActor(context.Background(), belowUser, belowProfile)
		if !opctx.IsSystemActor(ctx) {
			t.Fatal("precondition: WithSystemActor must build a system-actor context")
		}
		w.check(t, rfScenario{name: "system actor", ctx: ctx, actor: belowProfile, pass: passes(true, true)})
	})
	t.Run("system actor marker carrying a session is gated", func(t *testing.T) {
		// A struct-literal context with the exported SystemActor marker plus a
		// session below the policy: the engine and the list filter must agree
		// that it is gated, not trusted (paritytest.Compare checks the parity).
		ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: belowUser, ProfileID: belowProfile, SessionID: sessionWith(t, belowUser, 1, belowPw),
			CredentialID: belowPw, ACR: 1, AuthTime: time.Now(), SystemActor: true,
		})
		if !opctx.IsSystemActor(ctx) {
			t.Fatal("precondition: opctx.IsSystemActor must be true for the marked context")
		}
		w.check(t, rfScenario{name: "marked with session", ctx: ctx, actor: belowProfile, pass: passes(false, true)})
	})
	t.Run("anonymous context", func(t *testing.T) {
		ctx := opctx.WithAuthContext(context.Background(),
			opctx.AuthContext{PrincipalID: ownerHolder(t, anon), ProfileID: anon, Anonymous: true})
		w.check(t, rfScenario{name: "anonymous", ctx: ctx, actor: anon, none: true})
	})
	t.Run("personal profile reaching its own records", func(t *testing.T) {
		// Below both parties' policies, the own arm's rows stay listed and
		// authorized: the actor's own subject is never gated.
		ctx := sessionCtx(t, unauthUser, unauthProfile, 1, unauthPw)
		w.check(t, rfScenario{name: "own records", ctx: ctx, actor: unauthProfile, pass: passes(false, false)})
		listed := boundList(t, ctx, "organization", "read")
		if own := w.candidate(fmt.Sprintf("own-org/%d", unauthProfile)); !listed[own.id] {
			t.Errorf("own organization %d not listed for its personal profile below every policy", own.id)
		}
	})
	t.Run("party record under its own policy (decision F)", func(t *testing.T) {
		// The acr party's own record (no owner) is listed and authorized only
		// for a session meeting that party's policy.
		party := w.candidate("acr/party")
		below := boundList(t, sessionCtx(t, belowUser, belowProfile, 1, belowPw), "organization", "read")
		meet := boundList(t, sessionCtx(t, meetUser, meetProfile, 2, meetPw), "organization", "read")
		if below[party.id] || !meet[party.id] {
			t.Errorf("party record %d: listed below its policy = %v (want false), meeting it = %v (want true)", party.id, below[party.id], meet[party.id])
		}
		assertOwnerIDNull(t, party.id)
	})
}

// TestInteg_RowFilter_PredicateConformance calls the predicate directly: an
// unbound call (all three authentication inputs NULL) is not true for a row
// the gate refuses, a NULL or false marker never exempts, and only a true
// marker does (finding 8FYk).
func TestInteg_RowFilter_PredicateConformance(t *testing.T) {
	w := newRFWorld(t)
	user, profile, pw := w.collaborator(t)
	failing := sessionWith(t, user, 1, pw) // below the acr party's min_acr 2
	meeting := sessionWith(t, user, 2, pw)

	gated := w.candidate("acr/entity")
	partyRecord := w.candidate("acr/party")
	open := w.candidate("open/owned")
	ownerOf := func(id int64) *int64 {
		var owner *int64
		if err := integPool.QueryRow(context.Background(), `SELECT owner_id FROM entities WHERE id = $1`, id).Scan(&owner); err != nil {
			t.Fatalf("owner of %d: %v", id, err)
		}
		return owner
	}
	i64 := func(v int64) *int64 { return &v }
	b := func(v bool) *bool { return &v }

	for _, tc := range []struct {
		name     string
		row      rfCandidate
		session  *int64
		cred     *int64
		system   *bool
		wantTrue bool
	}{
		{"unbound (all NULL) on a gated row", gated, nil, nil, nil, false},
		{"unbound (all NULL) on a party record under its own policy", partyRecord, nil, nil, nil, false},
		{"unbound (all NULL) on an ungated row", open, nil, nil, nil, true},
		{"bound with no session and no credential (0, 0, false)", gated, i64(0), i64(0), b(false), false},
		{"marker NULL with a failing session", gated, i64(failing), nil, nil, false},
		{"marker false with a failing session", gated, i64(failing), i64(0), b(false), false},
		{"marker false with a meeting session (control)", gated, i64(meeting), i64(0), b(false), true},
		// A marked context that carries a session or credential is gated, not
		// trusted: SystemActor is an exported field, so the predicate mirrors
		// gated() and exempts only a sessionless, credential-less marker.
		{"marker true with a failing session", gated, i64(failing), i64(0), b(true), false},
		{"marker true with a failing session and NULL credential", gated, i64(failing), nil, b(true), false},
		{"marker true with a meeting session (still gated, then passes)", gated, i64(meeting), i64(0), b(true), true},
		{"marker true with a credential and no session", gated, i64(0), i64(pw), b(true), false},
		{"marker true with explicit zero session and credential", gated, i64(0), i64(0), b(true), true},
		{"marker true with no session and no credential", gated, nil, nil, b(true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *bool
			if err := integPool.QueryRow(context.Background(),
				`SELECT mod_users.access_row_permitted($1, $2, $3, $4, $5, $6)`,
				profile, tc.row.id, ownerOf(tc.row.id), tc.session, tc.cred, tc.system).Scan(&got); err != nil {
				t.Fatalf("access_row_permitted: %v", err)
			}
			isTrue := got != nil && *got
			if isTrue != tc.wantTrue {
				t.Errorf("access_row_permitted(%s) = %v, want true=%v", tc.row.label, got, tc.wantTrue)
			}
		})
	}
}

// TestInteg_RowFilter_UnboundList runs list queries in a transaction that
// never called BindAuthInputs: the gated rows are dropped for an outside
// collaborator, while every row the gate does not refuse stays listed (the
// ungated rows, the actor's own subject, its own party's rows for a member, and
// everything for a platform operator, decision D).
func TestInteg_RowFilter_UnboundList(t *testing.T) {
	w := newRFWorld(t)
	collabUser, collab, collabPw := w.collaborator(t)
	authorizeCredential(t, collabPw, w.ruleParty, collab)
	m := seedMember(t, w.acrParty)
	w.arm(t, m.PartyProfile)
	_, operator := seedOperatorUser(t)
	w.arm(t, operator)

	for _, tc := range []struct {
		name  string
		actor int64
		pass  map[int64]bool
	}{
		{"outside collaborator", collab, map[int64]bool{w.acrParty: false, w.ruleParty: false}},
		{"member of the acr party", m.PartyProfile, map[int64]bool{w.acrParty: true, w.ruleParty: false}},
		{"operator profile (decision D)", operator, map[int64]bool{w.acrParty: true, w.ruleParty: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := rfScenario{name: tc.name, actor: tc.actor, pass: tc.pass}
			for _, op := range rfOps {
				listed := unboundList(t, tc.actor, "entity", op)
				for _, c := range w.candidates {
					if want := sc.want(op, c); listed[c.id] != want {
						t.Errorf("unbound %s: %s (%d) listed = %v, want %v", op, c.label, c.id, listed[c.id], want)
					}
				}
			}
		})
	}

	// Control: the same collaborator, bound with a session meeting both
	// policies, lists the gated rows, so the drop above is the missing binding,
	// not the grants.
	bound := boundList(t, sessionCtx(t, collabUser, collab, 2, collabPw), "entity", "read")
	for _, label := range []string{"acr/entity", "acr/owned", "acr/managed-user", "rule/party"} {
		if c := w.candidate(label); !bound[c.id] {
			t.Errorf("bound, meeting both policies: %s (%d) not listed", label, c.id)
		}
	}
}
