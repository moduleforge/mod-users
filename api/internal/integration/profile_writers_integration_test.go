//go:build integration

package integration

// Serialization of the profile writers: the credential gate of a party
// (authorize, confine) against a concurrent profile ending, and an invitation
// against a concurrent admission of the same holder. Each test holds one
// transaction open on the pool while a second writer runs, and releases the
// first only once the second is waiting on a lock.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// waitForLockWait fails the test unless some backend of the test database is
// waiting on a lock within a few seconds.
func (s *stack) waitForLockWait(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.count(t, "lock waiters",
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no writer is waiting on a lock: the writers are not serialized")
}

func TestCredentialGate_EndingCommittedWhileTheGateWaitsLeavesNothingForTheDepartedHolder(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		written    func(t *testing.T, credUUID string, partyID int64) int64
	}{
		{"authorize", "/credential-authorizations/", func(t *testing.T, c string, p int64) int64 { return env.authorizationCount(t, c, p) }},
		{"confine", "/confinements/", func(t *testing.T, c string, _ int64) int64 { return env.confinedTo(t, c) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := env.newGateParty(t)
			m := env.newGateMember(t, g)
			credUUID := env.credentialUUID(t, m.acct.tk.access(), "password")
			ctx := env.authCtx(t, g.adminTok.access())

			// The ending holds the profile lock, uncommitted.
			tx, err := env.pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if err := env.profiles.EndInTx(ctx, tx, m.profileID, "left"); err != nil {
				t.Fatalf("EndInTx: %v", err)
			}

			done := make(chan *resp, 1)
			go func() {
				done <- env.do(t, http.MethodPut, g.path(tc.path+credUUID), g.adminTok.access(), nil)
			}()
			env.waitForLockWait(t)
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit ending: %v", err)
			}

			(<-done).wantStatus(t, http.StatusNotFound)
			if n := tc.written(t, credUUID, g.id); n != 0 {
				t.Errorf("the departed holder's credential carries %s state (%d) after the ending", tc.name, n)
			}
		})
	}
}

func TestInviteInTx_ConcurrentAdmissionYieldsOneProfileAndNoProfileExists(t *testing.T) {
	env.watchLifecycle()
	f := env.newParty(t)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	ctx := env.authCtx(t, f.adminTok.access())

	// The invitation holds the holder lock, uncommitted.
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	invited, err := env.profiles.InviteInTx(ctx, tx, svc.InviteInput{PartyID: f.partyID, HolderUserID: uid})
	if err != nil {
		t.Fatalf("InviteInTx: %v", err)
	}

	type result struct {
		p   svc.Profile
		err error
	}
	done := make(chan result, 1)
	go func() {
		admitCtx := env.authCtx(t, f.adminTok.access())
		var r result
		r.err = env.inTx(admitCtx, func(tx pgxTx) error {
			var err error
			r.p, err = env.profiles.AdmitInTx(admitCtx, tx, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
			return err
		})
		done <- r
	}()
	env.waitForLockWait(t)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit invitation: %v", err)
	}

	r := <-done
	if errors.Is(r.err, svc.ErrProfileExists) {
		t.Fatalf("AdmitInTx returned ErrProfileExists next to a concurrent invitation")
	}
	if r.err != nil {
		t.Fatalf("AdmitInTx: %v", r.err)
	}
	if r.p.ID != invited.ID || r.p.Status != "active" {
		t.Errorf("admitted profile = %+v, want the invited profile %d activated", r.p, invited.ID)
	}
	if got := env.openProfiles(t, uid, f.partyID); got != 1 {
		t.Errorf("%d open profiles, want 1", got)
	}
}
