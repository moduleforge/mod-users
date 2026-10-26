package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// gateCoreQ is a coredb.Querier over one profile and a confirmation table.
type gateCoreQ struct {
	coredb.Querier
	profile   coredb.Profile
	profErr   error
	confirmed map[[2]int64]int64 // (profile, party) -> confirmer
	upserts   int
}

func (f *gateCoreQ) GetProfileByEntityID(context.Context, int64) (coredb.Profile, error) {
	return f.profile, f.profErr
}

func (f *gateCoreQ) GetProfilePolicyConfirmation(_ context.Context, a coredb.GetProfilePolicyConfirmationParams) (coredb.ProfilePolicyConfirmation, error) {
	by, ok := f.confirmed[[2]int64{a.ProfileID, a.PartyID}]
	if !ok {
		return coredb.ProfilePolicyConfirmation{}, pgx.ErrNoRows
	}
	return coredb.ProfilePolicyConfirmation{ProfileID: a.ProfileID, PartyID: a.PartyID, ConfirmedByProfileID: by}, nil
}

func (f *gateCoreQ) UpsertProfilePolicyConfirmation(_ context.Context, a coredb.UpsertProfilePolicyConfirmationParams) (coredb.ProfilePolicyConfirmation, error) {
	f.upserts++
	if f.confirmed == nil {
		f.confirmed = map[[2]int64]int64{}
	}
	f.confirmed[[2]int64{a.ProfileID, a.PartyID}] = a.ConfirmedByProfileID
	return coredb.ProfilePolicyConfirmation{}, nil
}

// gateUsersQ answers holder_policy_mismatch with a fixed row.
type gateUsersQ struct {
	usersdb.Querier
	row   usersdb.HolderPolicyMismatchRow
	err   error
	calls int
	// got records the params of every HolderPolicyMismatch call.
	got []usersdb.HolderPolicyMismatchParams
}

func (f *gateUsersQ) HolderPolicyMismatch(_ context.Context, a usersdb.HolderPolicyMismatchParams) (usersdb.HolderPolicyMismatchRow, error) {
	f.calls++
	f.got = append(f.got, a)
	return f.row, f.err
}

func mismatchRow(reason string, minACR, holderACR int16) usersdb.HolderPolicyMismatchRow {
	return usersdb.HolderPolicyMismatchRow{
		Reason:         pgtype.Text{String: reason, Valid: reason != ""},
		CredentialRule: pgtype.Text{String: "authorized", Valid: true},
		MinAcr:         pgtype.Int2{Int16: minACR, Valid: true},
		HolderAcr:      pgtype.Int2{Int16: holderACR, Valid: holderACR != 0},
	}
}

func newTestGate(cq *gateCoreQ, uq *gateUsersQ) *CollaboratorGate {
	return &CollaboratorGate{
		newQ:       func(pgx.Tx) usersdb.Querier { return uq },
		newCoreQ:   func(pgx.Tx) coredb.Querier { return cq },
		policyLock: noopPolicyLock,
	}
}

func actingCtx(profileID int64) context.Context {
	return authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: profileID, SessionID: 3, AuthTime: time.Now()})
}

func partyProfile(id, holder, party int64) coredb.Profile {
	return coredb.Profile{EntityID: id, HolderID: holder, PartyID: pgtype.Int8{Int64: party, Valid: true}}
}

func TestCollaboratorGate_SamePartyPassesWithoutTouchingPolicy(t *testing.T) {
	t.Parallel()
	cq := &gateCoreQ{profile: partyProfile(7, 40, 5)}
	uq := &gateUsersQ{row: mismatchRow("acr_too_low", 3, 1)}
	g := newTestGate(cq, uq)
	for _, confirmed := range []bool{false, true} {
		if err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, confirmed); err != nil {
			t.Fatalf("same-party member (confirmed=%v): %v", confirmed, err)
		}
	}
	if uq.calls != 0 || cq.upserts != 0 {
		t.Fatalf("policy checks = %d, writes = %d; a same-party member needs neither", uq.calls, cq.upserts)
	}
}

func TestCollaboratorGate_OutsideProfile(t *testing.T) {
	t.Parallel()
	outsiders := map[string]coredb.Profile{
		"profile of another party": partyProfile(7, 40, 6),
		"personal profile":         {EntityID: 7, HolderID: 40},
	}
	for name, p := range outsiders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Run("mismatch without confirmation is refused and writes nothing", func(t *testing.T) {
				cq := &gateCoreQ{profile: p}
				g := newTestGate(cq, &gateUsersQ{row: mismatchRow("credential_not_authorized", 1, 1)})
				err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, false)
				if !errors.Is(err, apiresp.ErrConflict) {
					t.Fatalf("err = %v, want an apiresp conflict", err)
				}
				if codes := fieldCodes(err); len(codes) == 0 || codes[0] != CodePolicyMismatchConfirmationRequired {
					t.Fatalf("detail codes = %v, want %s first", codes, CodePolicyMismatchConfirmationRequired)
				}
				if cq.upserts != 0 {
					t.Fatalf("a refusal wrote %d confirmation(s)", cq.upserts)
				}
			})
			t.Run("mismatch with confirmation records the acting profile once", func(t *testing.T) {
				cq := &gateCoreQ{profile: p}
				g := newTestGate(cq, &gateUsersQ{row: mismatchRow("provider_required", 1, 1)})
				if err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, true); err != nil {
					t.Fatalf("confirmed: %v", err)
				}
				if by := cq.confirmed[[2]int64{7, 5}]; by != 2 || cq.upserts != 1 {
					t.Fatalf("confirmation by %d after %d write(s), want profile 2 after one", by, cq.upserts)
				}
				// Idempotent: a second confirmation, even by another profile, writes nothing.
				if err := g.CheckOutsideCollaborator(actingCtx(9), nil, 5, 7, true); err != nil {
					t.Fatalf("re-confirmed: %v", err)
				}
				if by := cq.confirmed[[2]int64{7, 5}]; by != 2 || cq.upserts != 1 {
					t.Fatalf("after re-confirmation: by %d, %d write(s); want the first confirmer and one write", by, cq.upserts)
				}
				// An already-confirmed profile passes even without the flag.
				if err := g.CheckOutsideCollaborator(actingCtx(9), nil, 5, 7, false); err != nil {
					t.Fatalf("already confirmed, unconfirmed call: %v", err)
				}
			})
			t.Run("no mismatch passes without a write", func(t *testing.T) {
				cq := &gateCoreQ{profile: p}
				g := newTestGate(cq, &gateUsersQ{row: mismatchRow("", 1, 1)})
				if err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, true); err != nil {
					t.Fatalf("matching outsider: %v", err)
				}
				if cq.upserts != 0 {
					t.Fatalf("a matching outsider got %d confirmation(s)", cq.upserts)
				}
			})
		})
	}
}

func TestCollaboratorGate_ErrorsAreNotConflicts(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	t.Run("unknown profile", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(&gateCoreQ{profErr: pgx.ErrNoRows}, &gateUsersQ{})
		err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, true)
		if err == nil || errors.Is(err, apiresp.ErrConflict) {
			t.Fatalf("err = %v, want a plain error", err)
		}
	})
	t.Run("policy check failure", func(t *testing.T) {
		t.Parallel()
		g := newTestGate(&gateCoreQ{profile: partyProfile(7, 40, 6)}, &gateUsersQ{err: boom})
		if err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, true); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the cause wrapped", err)
		}
	})
	t.Run("confirmation without an acting profile", func(t *testing.T) {
		t.Parallel()
		cq := &gateCoreQ{profile: partyProfile(7, 40, 6)}
		g := newTestGate(cq, &gateUsersQ{row: mismatchRow("acr_too_low", 2, 1)})
		if err := g.CheckOutsideCollaborator(context.Background(), nil, 5, 7, true); !errors.Is(err, apiresp.ErrUnauthenticated) {
			t.Fatalf("err = %v, want unauthenticated", err)
		}
		if cq.upserts != 0 {
			t.Fatalf("wrote %d confirmation(s) with no acting profile", cq.upserts)
		}
	})
}

func TestPolicyMismatchConflict_NamesTheFailingRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		m         PolicyMismatch
		wantField string
		wantCode  string
	}{
		{PolicyMismatch{Reason: MismatchCredentialNotAuthorized}, "credential_rule", "users.credential_not_authorized"},
		{PolicyMismatch{Reason: MismatchProviderRequired, RequiredProviderID: "corp"}, "credential_rule", "users.provider_required"},
		{PolicyMismatch{Reason: MismatchACRTooLow, MinACR: 2, HolderACR: 1}, "min_acr", "users.acr_too_low"},
	}
	for _, tc := range tests {
		err := tc.m.Conflict()
		if !errors.Is(err, apiresp.ErrConflict) {
			t.Fatalf("%s: err = %v, want a conflict", tc.m.Reason, err)
		}
		codes := fieldCodes(err)
		if len(codes) != 2 || codes[0] != CodePolicyMismatchConfirmationRequired || codes[1] != tc.wantCode {
			t.Errorf("%s: detail codes = %v, want [%s %s]", tc.m.Reason, codes, CodePolicyMismatchConfirmationRequired, tc.wantCode)
		}
	}
}

func TestInviteInTx_MismatchIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	const party = int64(5)
	uq := &profileUsersQ{
		user: func(int64) (usersdb.User, error) { return usersdb.User{EntityID: 40}, nil },
	}
	// The embedded Querier panics on any write: the refused invitation must not
	// reach one, and the mismatch check must run on the holder and the party.
	s := newInviteTestService(&opAuthorizer{}, &profileCoreQ{}, uq)
	mq := &gateUsersQ{row: mismatchRow("acr_too_low", 2, 1)}
	mq2 := &mismatchOnlyUsersQ{profileUsersQ: uq, gate: mq}
	s.newQ = func(pgx.Tx) usersdb.Querier { return mq2 }
	_, err := s.InviteInTx(authCtx(sessionAdmin), nil, InviteInput{PartyID: party, HolderUserID: 40})
	if !errors.Is(err, apiresp.ErrConflict) {
		t.Fatalf("err = %v, want the policy-mismatch conflict", err)
	}
	if codes := fieldCodes(err); len(codes) == 0 || codes[0] != CodePolicyMismatchConfirmationRequired {
		t.Fatalf("detail codes = %v, want %s first", codes, CodePolicyMismatchConfirmationRequired)
	}
	if mq.calls != 1 {
		t.Fatalf("mismatch checks = %d, want 1", mq.calls)
	}
	if len(mq.got) != 1 || !mq.got[0].InviteTime || mq.got[0].HolderID != 40 || mq.got[0].PartyID != party {
		t.Fatalf("invitation mismatch params = %+v, want one call for holder 40, party %d, InviteTime=true", mq.got, party)
	}
}

// The outside-collaborator gate must check the policy in collaborator mode
// (InviteTime=false): a collaborator gains no profile in the party, so the
// relaxed invitee rule must not apply.
func TestCollaboratorGate_ChecksPolicyWithInviteTimeFalse(t *testing.T) {
	t.Parallel()
	uq := &gateUsersQ{row: mismatchRow("", 1, 1)}
	g := newTestGate(&gateCoreQ{profile: partyProfile(7, 40, 6)}, uq)
	if err := g.CheckOutsideCollaborator(actingCtx(2), nil, 5, 7, false); err != nil {
		t.Fatalf("matching outsider: %v", err)
	}
	if len(uq.got) != 1 || uq.got[0].InviteTime || uq.got[0].HolderID != 40 || uq.got[0].PartyID != 5 {
		t.Fatalf("collaborator mismatch params = %+v, want one call for holder 40, party 5, InviteTime=false", uq.got)
	}
}

// mismatchOnlyUsersQ routes the holder lookup to the invite fake and the policy
// check to the gate fake.
type mismatchOnlyUsersQ struct {
	*profileUsersQ
	gate *gateUsersQ
}

func (m *mismatchOnlyUsersQ) HolderPolicyMismatch(ctx context.Context, a usersdb.HolderPolicyMismatchParams) (usersdb.HolderPolicyMismatchRow, error) {
	return m.gate.HolderPolicyMismatch(ctx, a)
}
