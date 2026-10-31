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

// TestInteg_PartyGate_ImpersonationUsesParentFactors proves decision G as
// resolved on the owning-party gate: an impersonation of the collaborator
// meets the party's credential rule with the administrator's parent-session
// factors, never skipped for a non-operator administrator; an administrator
// acting through an operator profile (the recorded impersonator_profile_id,
// decision J) skips the credential rule but not min_acr.
func TestInteg_PartyGate_ImpersonationUsesParentFactors(t *testing.T) {
	g := seedGateParty(t, "authorized", 1)
	// The collaborator's own credential is authorized; it is not the
	// impersonation's factor.
	authorizeCredential(t, fxPassword(t, integPool, g.collabUser, nil), g.party, g.collabProfile)

	// actingProfile is the administrator's recorded acting profile
	// (impersonator_profile_id); nil picks a profile the administrator holds.
	impCtx := func(adminUser int64, actingProfile *int64, adminCred int64, acr int) (context.Context, int64) {
		t.Helper()
		parent, _ := fxSession(t, integPool, adminUser, sessionOpts{ACR: acr})
		fxFactor(t, integPool, parent, adminCred, nil)
		child, _ := fxSession(t, integPool, g.collabUser, sessionOpts{ACR: acr, ParentID: &parent,
			ImpersonateProfile: &g.collabProfile, ImpersonatorProfile: actingProfile})
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: g.collabUser, ProfileID: g.collabProfile, SessionID: child,
			ImpersonatorPrincipalID: adminUser, ACR: int16(acr), AuthTime: time.Now(),
		}), parent
	}

	admin, _ := seedUserEntity(t)
	adminPw := fxPassword(t, integPool, admin, nil)
	ctx, _ := impCtx(admin, nil, adminPw, 1)
	requireForbidden403(t, integAZ.Authorize(ctx, "read", &g.content), "non-operator administrator's factor not authorized for the party")
	authorizeCredential(t, adminPw, g.party, g.collabProfile)
	if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
		t.Errorf("administrator's factor authorized for the party: err = %v, want nil", err)
	}

	// Decision J: the skip follows the recorded acting profile. The operator's
	// principal acting through its personal profile is bound by the rule;
	// acting through its operator profile it is exempt.
	opUser, opProfile := seedOperatorUser(t)
	opPw := fxPassword(t, integPool, opUser, nil)
	var opPersonal int64
	if err := integPool.QueryRow(context.Background(),
		`SELECT entity_id FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, opUser).Scan(&opPersonal); err != nil {
		t.Fatalf("operator's personal profile: %v", err)
	}
	viaPersonal, _ := impCtx(opUser, &opPersonal, opPw, 1)
	requireForbidden403(t, integAZ.Authorize(viaPersonal, "read", &g.content), "operator's principal acting through its personal profile")
	opCtx, _ := impCtx(opUser, &opProfile, opPw, 1)
	if err := integAZ.Authorize(opCtx, "read", &g.content); err != nil {
		t.Errorf("operator administrator under the credential rule: err = %v, want nil", err)
	}
	fxExec(t, integPool, "raise min_acr", `UPDATE mod_users.party_authn_policies SET min_acr = 2 WHERE party_id = $1`, g.party)
	requireForbidden403(t, integAZ.Authorize(opCtx, "read", &g.content), "operator administrator below the party's min_acr")
}

// TestInteg_PartyGate_ImpersonationConfinesNonOperatorParentFactors proves
// decision L on the owning-party gate: a non-operator administrator's parent
// factor confined to another party fails the gate even when it is authorized
// for the gate party; an operator impersonation is exempt.
func TestInteg_PartyGate_ImpersonationConfinesNonOperatorParentFactors(t *testing.T) {
	g := seedGateParty(t, "authorized", 1)
	elsewhere := seedOrg(t, "Gate Elsewhere "+fxTag("p"))
	impCtx := func(adminUser int64, actingProfile *int64, adminCred int64) context.Context {
		t.Helper()
		parent, _ := fxSession(t, integPool, adminUser, sessionOpts{ACR: 1})
		fxFactor(t, integPool, parent, adminCred, nil)
		child, _ := fxSession(t, integPool, g.collabUser, sessionOpts{ACR: 1, ParentID: &parent,
			ImpersonateProfile: &g.collabProfile, ImpersonatorProfile: actingProfile})
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: g.collabUser, ProfileID: g.collabProfile, SessionID: child,
			ImpersonatorPrincipalID: adminUser, ACR: 1, AuthTime: time.Now(),
		})
	}

	admin, _ := seedUserEntity(t)
	adminPw := fxPassword(t, integPool, admin, nil)
	authorizeCredential(t, adminPw, g.party, g.collabProfile)
	ctx := impCtx(admin, nil, adminPw)
	if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
		t.Fatalf("unconfined administrator factor authorized for the party: err = %v, want nil", err)
	}
	confine(t, adminPw, elsewhere)
	requireForbidden403(t, integAZ.Authorize(ctx, "read", &g.content),
		"non-operator administrator's factor confined to another party, though authorized for the gate party")

	opUser, opProfile := seedOperatorUser(t)
	opPw := fxPassword(t, integPool, opUser, nil)
	confine(t, opPw, operatorPartyID(t))
	if err := integAZ.Authorize(impCtx(opUser, &opProfile, opPw), "read", &g.content); err != nil {
		t.Errorf("operator administrator with a credential confined to the operator party: err = %v, want nil", err)
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
}

// Decision D: platform operator profiles are exempt from the owning-party
// gate, while non-operator outside collaborators stay gated by the same restrictive policy.
func TestInteg_PartyGate_OperatorExempt(t *testing.T) {
	g := seedGateParty(t, "authorized", 3)

	opUser, opProfile := seedOperatorUser(t)
	seedWildcardGrant(t, opProfile, "read")
	opPW := fxPassword(t, integPool, opUser, nil)
	// acr 1 and an unauthorized credential: both the credential rule and
	// min_acr of the owning party are unmet, and the operator still passes.
	if err := integAZ.Authorize(sessionCtx(t, opUser, opProfile, 1, opPW), "read", &g.content); err != nil {
		t.Errorf("operator profile under a restrictive owning-party policy: err = %v, want nil", err)
	}

	// A non-operator outside collaborator holding a targeted grant stays gated
	// (the schema allows wildcard grants to operator actors only).
	cpw := fxPassword(t, integPool, g.collabUser, nil)
	requireForbidden403(t, integAZ.Authorize(sessionCtx(t, g.collabUser, g.collabProfile, 1, cpw), "read", &g.content), "non-operator collaborator")
}

// Decision F: a party is its own owning party. Authorize on the party record
// itself (an organization with no owner, so owner_id resolves to NULL) is
// gated by the party's own policy: an outside collaborator holding an
// entity-scope update grant on the record is refused from a session that does
// not meet the policy and admitted once it does, while an operator (decision D)
// and a compliant member pass.
func TestInteg_PartyGate_PartyRecordIsGatedByItsOwnPolicy(t *testing.T) {
	party := seedUnownedOrg(t, "Party Record "+fxTag("p"))
	fxMustTx(t, func(tx pgx.Tx) { fxPolicy(t, tx, party, "authorized", nil, 2) })

	t.Run("outside collaborator with an update grant", func(t *testing.T) {
		user, profile := seedUserEntity(t)
		targetedGrant(t, profile, party, "update")
		pw := fxPassword(t, integPool, user, nil)

		requireForbidden403(t, integAZ.Authorize(sessionCtx(t, user, profile, 1, pw), "update", &party),
			"collaborator below min_acr with an unauthorized credential")
		requireForbidden403(t, integAZ.Authorize(sessionCtx(t, user, profile, 2, pw), "update", &party),
			"collaborator at min_acr with an unauthorized credential")

		authorizeCredential(t, pw, party, profile)
		requireForbidden403(t, integAZ.Authorize(sessionCtx(t, user, profile, 1, pw), "update", &party),
			"collaborator with an authorized credential below min_acr")
		if err := integAZ.Authorize(sessionCtx(t, user, profile, 2, pw), "update", &party); err != nil {
			t.Errorf("collaborator meeting the party's own policy: err = %v, want nil", err)
		}
	})

	t.Run("operator", func(t *testing.T) {
		opUser, opProfile := seedOperatorUser(t)
		seedWildcardGrant(t, opProfile, "update")
		opPW := fxPassword(t, integPool, opUser, nil)
		if err := integAZ.Authorize(sessionCtx(t, opUser, opProfile, 1, opPW), "update", &party); err != nil {
			t.Errorf("operator profile on a restrictive party's record: err = %v, want nil", err)
		}
	})

	t.Run("compliant member", func(t *testing.T) {
		user, _ := seedUserEntity(t)
		var member int64
		fxMustTx(t, func(tx pgx.Tx) { member = fxPartyProfile(t, tx, user, party, "active") })
		targetedGrant(t, member, party, "update")
		pw := fxPassword(t, integPool, user, nil)
		authorizeCredential(t, pw, party, member)
		if err := integAZ.Authorize(sessionCtx(t, user, member, 2, pw), "update", &party); err != nil {
			t.Errorf("compliant member on its own party's record: err = %v, want nil", err)
		}
	})

	// The owner's gate is kept: a party-owned party is gated by its own policy
	// and its direct owner's (one level, no ancestor walk).
	t.Run("party owned by a restrictive party", func(t *testing.T) {
		child := seedOwnedOrg(t, party, "Child Party "+fxTag("c"))
		user, profile := seedUserEntity(t)
		targetedGrant(t, profile, child, "update")
		pw := fxPassword(t, integPool, user, nil)
		requireForbidden403(t, integAZ.Authorize(sessionCtx(t, user, profile, 1, pw), "update", &child),
			"collaborator below the owning party's policy on a child party without a policy")

		grandchild := seedOwnedOrg(t, child, "Grandchild Party "+fxTag("g"))
		targetedGrant(t, profile, grandchild, "update")
		if err := integAZ.Authorize(sessionCtx(t, user, profile, 1, pw), "update", &grandchild); err != nil {
			t.Errorf("grandchild of the restrictive party (one-level gate): err = %v, want nil", err)
		}
	})
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

// TestInteg_PartyGate_SessionlessUserContextIsRefused proves the engine fails
// closed for the context mod-users builds for a real user in its
// unauthenticated flows (internal/service withActingProfile: principal and
// acting profile only, no session, no credential, no system-actor marker). On
// a restrictive third party's row it is refused: by the assurance gate as
// built (ACR 0), and, with an ACR and AuthTime that pass the assurance gate,
// by the owning-party gate (mod_users.session_meets_party_policy has no factors
// to read). The same profile in an interactive session meeting the policy is
// allowed.
func TestInteg_PartyGate_SessionlessUserContextIsRefused(t *testing.T) {
	g := seedGateParty(t, "authorized", 1)
	// The collaborator holds read on the row (targeted) and over the party's
	// content (owned scope), so only a gate can refuse it.
	ownedGrant(t, g.collabProfile, g.party, "read")

	asBuilt := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: g.collabUser, ProfileID: g.collabProfile,
	})
	if opctx.IsSystemActor(asBuilt) {
		t.Fatal("precondition: a real user's sessionless context must not be a system actor")
	}
	requireStepUpAction(t, integAZ.Authorize(asBuilt, "read", &g.content), "sessionless user context as built (ACR 0)")

	assured := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: g.collabUser, ProfileID: g.collabProfile, ACR: 3, AuthTime: time.Now(),
	})
	requireForbidden403(t, integAZ.Authorize(assured, "read", &g.content),
		"sessionless user context meeting the assurance gate, on a restrictive third party's row")
	requireForbidden403(t, integAZ.AuthorizeTypeIn(assured, "read", organizationTypeID(t), g.party),
		"sessionless user context, AuthorizeTypeIn under a restrictive third party")

	pw := fxPassword(t, integPool, g.collabUser, nil)
	authorizeCredential(t, pw, g.party, g.collabProfile)
	session := sessionCtx(t, g.collabUser, g.collabProfile, 1, pw)
	if err := integAZ.Authorize(session, "read", &g.content); err != nil {
		t.Errorf("same profile in an interactive session meeting the policy: err = %v, want nil", err)
	}
	if err := integAZ.AuthorizeTypeIn(session, "read", organizationTypeID(t), g.party); err != nil {
		t.Errorf("same profile in an interactive session meeting the policy, AuthorizeTypeIn: err = %v, want nil", err)
	}
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

// TestInteg_PartyGate_ClientCredentials runs the gate for a non-interactive
// caller: an outside collaborator acting through a single API key (CredentialID
// set, SessionID zero). The key's factors and base_acr take the session's
// place.
func TestInteg_PartyGate_ClientCredentials(t *testing.T) {
	keyCtx := func(g gateParty, keyID int64, acr int16) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: g.collabUser, ProfileID: g.collabProfile, CredentialID: keyID, ACR: acr, AuthTime: time.Now(),
		})
	}

	t.Run("credential_rule authorized", func(t *testing.T) {
		g := seedGateParty(t, "authorized", 1)
		key, _ := fxAPIKey(t, integPool, g.collabUser)
		ctx := keyCtx(g, key, 1)

		requireForbidden403(t, integAZ.Authorize(ctx, "read", &g.content), "unauthorized API key")
		requireForbidden403(t, integAZ.AuthorizeTypeIn(ctx, "read", organizationTypeID(t), g.party), "unauthorized API key, AuthorizeTypeIn")

		authorizeCredential(t, key, g.party, g.collabProfile)
		if err := integAZ.Authorize(ctx, "read", &g.content); err != nil {
			t.Errorf("authorized API key: err = %v, want nil", err)
		}
	})

	t.Run("min_acr above the key's base_acr", func(t *testing.T) {
		g := seedGateParty(t, "any", 2)
		key, _ := fxAPIKey(t, integPool, g.collabUser)
		requireForbidden403(t, integAZ.Authorize(keyCtx(g, key, 1), "read", &g.content), "API key below the party's min_acr")

		// A confirmation exempts the credential rule only, never min_acr.
		fxExec(t, integPool, "confirm collaborator",
			`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
			g.collabProfile, g.party)
		requireForbidden403(t, integAZ.Authorize(keyCtx(g, key, 1), "read", &g.content), "confirmed collaborator, API key below min_acr")
	})

	t.Run("confirmation exempts the credential rule", func(t *testing.T) {
		g := seedGateParty(t, "authorized", 1)
		key, _ := fxAPIKey(t, integPool, g.collabUser)
		requireForbidden403(t, integAZ.Authorize(keyCtx(g, key, 1), "read", &g.content), "unconfirmed, unauthorized key")
		fxExec(t, integPool, "confirm collaborator",
			`INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
			g.collabProfile, g.party)
		if err := integAZ.Authorize(keyCtx(g, key, 1), "read", &g.content); err != nil {
			t.Errorf("confirmed collaborator, unauthorized key under credential_rule authorized: err = %v, want nil", err)
		}
	})

	t.Run("policy met", func(t *testing.T) {
		g := seedGateParty(t, "any", 1)
		key, _ := fxAPIKey(t, integPool, g.collabUser)
		if err := integAZ.Authorize(keyCtx(g, key, 1), "read", &g.content); err != nil {
			t.Errorf("API key meeting the policy: err = %v, want nil", err)
		}
	})
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
