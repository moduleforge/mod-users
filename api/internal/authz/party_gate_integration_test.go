//go:build integration

package authz_test

// party_gate_integration_test.go proves the engine's owning-party gate
// (mod_users.session_meets_party_policy, consulted in the Authorizer prelude)
// against the real composed schema: an acting profile outside the party that
// owns the target must meet that party's credential_rule and min_acr with its
// session's factors, or, on the SSH personal-profile fallback, with its key.
// It also pins the own arm's ownership_confers gate and AuthorizeTypeIn's
// owned-scope arm. The full engine matrix lives with the engine tests.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/service"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// gateParty is a party with a policy, an entity it owns, and an outside
// collaborator (a user acting through its personal profile) holding read on
// that entity.
type gateParty struct {
	party, content            int64
	collabUser, collabProfile int64
}

func seedGateParty(t *testing.T, rule string, minACR int) gateParty {
	t.Helper()
	var g gateParty
	g.party = seedOrg(t, "Gate Party "+fxTag("p"))
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, g.party, rule, nil, minACR) })
	g.content = seedOwnedOrg(t, g.party, "Gate Content "+fxTag("c"))
	g.collabUser, g.collabProfile = seedUserEntity(t)
	targetedGrant(t, g.collabProfile, g.content, "read")
	return g
}

// sessionCtx inserts a session for principal with the given stored acr and
// factors and returns a fresh context acting as profile in it.
func sessionCtx(t *testing.T, principal, profile int64, acr int, factors ...int64) context.Context {
	t.Helper()
	sessionID, _ := fxSession(t, integPool, principal, sessionOpts{ACR: acr})
	for _, f := range factors {
		fxFactor(t, integPool, sessionID, f, nil)
	}
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: principal, ProfileID: profile, SessionID: sessionID, ACR: int16(acr), AuthTime: time.Now(),
	})
}

func authorizeCredential(t *testing.T, credentialID, partyID, byProfile int64) {
	t.Helper()
	fxExec(t, integPool, "authorize credential for party",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		credentialID, partyID, byProfile)
}

func requireForbidden403(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("%s: err = %v, want ErrForbidden", what, err)
	}
	if errors.Is(err, authz.ErrStepUpRequired) {
		t.Errorf("%s: the owning-party refusal must be a plain 403, not a step-up action", what)
	}
}

func TestInteg_PartyGate_MinACR(t *testing.T) {
	g := seedGateParty(t, "any", 2)
	pw := fxPassword(t, integPool, g.collabUser, nil)

	low := sessionCtx(t, g.collabUser, g.collabProfile, 1, pw)
	requireForbidden403(t, integAZ.Authorize(low, "read", &g.content), "collaborator session below the party's min_acr")

	high := sessionCtx(t, g.collabUser, g.collabProfile, 2, pw)
	if err := integAZ.Authorize(high, "read", &g.content); err != nil {
		t.Errorf("collaborator session at the party's min_acr: err = %v, want nil", err)
	}

	// A profile_policy_confirmations row never exempts min_acr.
	fxExec(t, integPool, "confirm collaborator",
		`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
		g.collabProfile, g.party)
	requireForbidden403(t, integAZ.Authorize(low, "read", &g.content), "confirmed collaborator below min_acr")
}

func TestInteg_PartyGate_AuthorizedRule(t *testing.T) {
	g := seedGateParty(t, "authorized", 1)
	pw := fxPassword(t, integPool, g.collabUser, nil)
	ctx := sessionCtx(t, g.collabUser, g.collabProfile, 1, pw)

	requireForbidden403(t, integAZ.Authorize(ctx, "read", &g.content), "collaborator whose credential is not authorized for the party")

	authorizeCredential(t, pw, g.party, g.collabProfile)
	if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
		t.Errorf("collaborator whose credential is authorized: err = %v, want nil", err)
	}
}

func TestInteg_PartyGate_ConfirmationExemptsCredentialRule(t *testing.T) {
	g := seedGateParty(t, "authorized", 1)
	pw := fxPassword(t, integPool, g.collabUser, nil)
	ctx := sessionCtx(t, g.collabUser, g.collabProfile, 1, pw)
	requireForbidden403(t, integAZ.Authorize(ctx, "read", &g.content), "unconfirmed collaborator")

	fxExec(t, integPool, "confirm collaborator",
		`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
		g.collabProfile, g.party)
	if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
		t.Errorf("confirmed collaborator under credential_rule authorized: err = %v, want nil", err)
	}
}

func TestInteg_PartyGate_AppliesToEveryArm(t *testing.T) {
	g := seedGateParty(t, "any", 2)

	// Owned-scope grant over the party, held by a second outside collaborator.
	user, profile := seedUserEntity(t)
	ownedGrant(t, profile, g.party, "read")
	pw2 := fxPassword(t, integPool, user, nil)
	requireForbidden403(t, integAZ.Authorize(sessionCtx(t, user, profile, 1, pw2), "read", &g.content), "owned-scope collaborator below min_acr")
	if err := integAZ.Authorize(sessionCtx(t, user, profile, 2, pw2), "read", &g.content); err != nil {
		t.Errorf("owned-scope collaborator at min_acr: err = %v, want nil", err)
	}

	// AuthorizeTypeIn resolves the owning party from ownerID.
	orgType := organizationTypeID(t)
	requireForbidden403(t, integAZ.AuthorizeTypeIn(sessionCtx(t, user, profile, 1, pw2), "read", orgType, g.party), "AuthorizeTypeIn below min_acr")
	if err := integAZ.AuthorizeTypeIn(sessionCtx(t, user, profile, 2, pw2), "read", orgType, g.party); err != nil {
		t.Errorf("AuthorizeTypeIn owned-scope at min_acr: err = %v, want nil", err)
	}

	// The gate runs in the prelude, before the wildcard arm: an operator
	// profile reaching another party's content is held to that party's policy.
	opUser, opProfile := seedOperatorUser(t)
	seedWildcardGrant(t, opProfile, "read")
	opPW := fxPassword(t, integPool, opUser, nil)
	requireForbidden403(t, integAZ.Authorize(sessionCtx(t, opUser, opProfile, 1, opPW), "read", &g.content), "wildcard holder outside the owning party below min_acr")
	if err := integAZ.Authorize(sessionCtx(t, opUser, opProfile, 2, opPW), "read", &g.content); err != nil {
		t.Errorf("wildcard holder at min_acr: err = %v, want nil", err)
	}
}

func TestInteg_PartyGate_Skips(t *testing.T) {
	g := seedGateParty(t, "authorized", 3)

	t.Run("acting profile inside the owning party", func(t *testing.T) {
		user, _ := seedUserEntity(t)
		var member int64
		fxMustTx(t, func(tx pgx.Tx) { member = fxPartyProfile(t, tx, user, g.party, "active") })
		targetedGrant(t, member, g.content, "read")
		pw := fxPassword(t, integPool, user, nil)
		if err := integAZ.Authorize(sessionCtx(t, user, member, 1, pw), "read", &g.content); err != nil {
			t.Errorf("member of the owning party: err = %v, want nil (its own party's policy is liveness's)", err)
		}
	})

	t.Run("personal profile reaching its own record", func(t *testing.T) {
		user, profile := seedUserEntity(t)
		pw := fxPassword(t, integPool, user, nil)
		own := seedOwnedOrg(t, profile, "Own Record "+fxTag("o"))
		if err := integAZ.Authorize(sessionCtx(t, user, profile, 1, pw), "read", &own); err != nil {
			t.Errorf("own record: err = %v, want nil", err)
		}
	})

	t.Run("system actor", func(t *testing.T) {
		ctx := opctx.WithSystemActor(context.Background(), g.collabUser, g.collabProfile)
		if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
			t.Errorf("system-actor context: err = %v, want nil", err)
		}
	})
}

func TestInteg_PartyGate_SSHFallback(t *testing.T) {
	resolver := service.NewSSHKeyResolver(usersdb.New(integPool), coredb.New(integPool))

	for _, tc := range []struct {
		name      string
		rule      string
		minACR    int
		authorize bool
		wantAllow bool
	}{
		{"min_acr above the key's base_acr", "any", 2, false, false},
		{"key not authorized under credential_rule authorized", "authorized", 1, false, false},
		{"key authorized under credential_rule authorized", "authorized", 1, true, true},
		{"policy met", "any", 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := seedGateParty(t, tc.rule, tc.minACR)
			keyID, _ := fxSSHKey(t, integPool, g.collabUser, "SHA256:"+fxTag("fp"))
			if tc.authorize {
				authorizeCredential(t, keyID, g.party, g.collabProfile)
			}

			// The principal holds no profile in the owning party, so the
			// resolver falls back to the personal profile.
			ac, err := resolver.AuthContextForOwner(context.Background(),
				service.SSHCredential{CredentialID: keyID, PrincipalID: g.collabUser}, &g.party)
			if err != nil {
				t.Fatalf("AuthContextForOwner: %v", err)
			}
			if ac.ProfileID != g.collabProfile || ac.CredentialID != keyID || ac.SessionID != 0 {
				t.Fatalf("AuthContextForOwner: got %+v, want the personal profile %d with credential %d", ac, g.collabProfile, keyID)
			}

			err = integAZ.Authorize(opctx.WithAuthContext(context.Background(), ac), "read", &g.content)
			if tc.wantAllow {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			requireForbidden403(t, err, "SSH personal-profile fallback")
		})
	}
}

// TestInteg_OwnArm_OwnershipConfers pins the own arm's operation gate: owning
// the target confers read, update and grant, never impersonate, manage or
// login.
func TestInteg_OwnArm_OwnershipConfers(t *testing.T) {
	owner := seedUser(t, "own-arm-confers", false)
	owned := seedOwnedOrg(t, owner, "Own Arm Confers "+fxTag("o"))
	ctx := actorCtx(owner)

	for _, op := range []string{"read", "update", "grant"} {
		if err := integAZ.Authorize(ctx, op, &owned); err != nil {
			t.Errorf("own arm %s: err = %v, want nil", op, err)
		}
	}
	for _, op := range []string{"impersonate", "manage", "login"} {
		if err := integAZ.Authorize(ctx, op, &owned); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("own arm %s: err = %v, want ErrForbidden (ownership_confers is false)", op, err)
		}
	}

	// AuthorizeTypeIn's own arm: ownerID is the actor's own subject.
	orgType := organizationTypeID(t)
	ownerUser := ownerSubject(t, owner)
	if err := integAZ.AuthorizeTypeIn(ctx, "create", orgType, ownerUser); err != nil {
		t.Errorf("AuthorizeTypeIn own arm create: err = %v, want nil", err)
	}
	if err := integAZ.AuthorizeTypeIn(ctx, "manage", orgType, ownerUser); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("AuthorizeTypeIn own arm manage: err = %v, want ErrForbidden", err)
	}
}
