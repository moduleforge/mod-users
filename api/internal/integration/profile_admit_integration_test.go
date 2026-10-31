//go:build integration

package integration

// The trusted admission path (ProfileService.AdmitInTx), against the real
// services, database triggers and the real mod-audit observer.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// admit runs AdmitInTx in its own transaction as the party's administrator (the
// acting profile the observations need; AdmitInTx checks no grant).
func (s *stack) admit(t *testing.T, f partyFixture, in svc.AdmitInput) (svc.Profile, error) {
	t.Helper()
	ctx := s.authCtx(t, f.adminTok.access())
	var p svc.Profile
	err := s.inTx(ctx, func(tx pgxTx) error {
		var err error
		p, err = s.profiles.AdmitInTx(ctx, tx, in)
		return err
	})
	return p, err
}

func (s *stack) openProfiles(t *testing.T, holderID, partyID int64) int64 {
	t.Helper()
	return s.count(t, "profiles", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2 AND status <> 'ended'`, holderID, partyID)
}

func TestAdmitInTx_CreatesAnActiveProfile(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)

	env.obs.reset()
	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid, InvitedByProfileID: f.adminProf})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if p.Status != "active" || p.Source != "manual" || p.PartyID != f.partyID || p.HolderPrincipalID != uid {
		t.Errorf("profile = %+v", p)
	}
	status, source, _, owner, invitedBy, started, _ := env.profileRow(t, p.UUID.String())
	if status != "active" || source != "manual" || owner != f.partyID || invitedBy != f.adminProf || !started {
		t.Errorf("row = %s %s owner=%d invited_by=%d started=%v", status, source, owner, invitedBy, started)
	}
	if created := env.scanInt64(t, "created_by", `SELECT created_by_profile_id FROM entities WHERE id = $1`, p.ID); created != f.adminProf {
		t.Errorf("created_by_profile_id = %d, want the acting profile %d", created, f.adminProf)
	}
	env.checkObservations(t, "admit create", "create profile")
	got := lc.seen()
	if len(got) != 1 || got[0].From != "" || got[0].To != "active" || got[0].PartyID != f.partyID || got[0].HolderPrincipalID != uid {
		t.Errorf("lifecycle = %+v, want one creation as active", got)
	}

	// The admitted profile is in the holder's own list, available.
	var list struct {
		Profiles []map[string]any `json:"profiles"`
	}
	env.do(t, http.MethodGet, "/v1/me/profiles", a.tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
	found := false
	for _, pr := range list.Profiles {
		if pr["uuid"] == p.UUID.String() {
			found = pr["status"] == "active"
		}
	}
	if !found {
		t.Errorf("the admitted profile is not active in GET /v1/me/profiles: %v", list.Profiles)
	}
}

func TestAdmitInTx_NullInviterWhenZero(t *testing.T) {
	f := env.newParty(t)
	a := env.registerVerified(t)
	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: env.principalID(t, a.Email)})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if _, _, _, _, invitedBy, _, _ := env.profileRow(t, p.UUID.String()); invitedBy != 0 {
		t.Errorf("invited_by_profile_id = %d, want NULL", invitedBy)
	}
}

func TestAdmitInTx_ActiveProfileIsReturnedAsIs(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a, id := env.accepted(t, f)
	uid := env.principalID(t, a.Email)

	lc.reset()
	env.obs.reset()
	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if p.UUID.String() != id || p.Status != "active" {
		t.Errorf("profile = %+v, want the existing active profile %s", p, id)
	}
	if n := len(env.obs.all()); n != 0 {
		t.Errorf("an already-active profile wrote %d observations", n)
	}
	if n := len(lc.seen()); n != 0 {
		t.Errorf("an already-active profile ran %d lifecycle observer calls", n)
	}
}

func TestAdmitInTx_InvitedProfileBecomesActive(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a, id := env.invited(t, f)
	uid := env.principalID(t, a.Email)

	lc.reset()
	env.obs.reset()
	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if p.UUID.String() != id || p.Status != "active" {
		t.Errorf("profile = %+v, want %s activated", p, id)
	}
	if got := env.profileStatus(t, id); got != "active" {
		t.Errorf("status = %s, want active", got)
	}
	env.checkObservations(t, "admit activate", "update profile")
	if got := lc.seen(); len(got) != 1 || got[0].From != "invited" || got[0].To != "active" {
		t.Errorf("lifecycle = %+v, want invited to active", got)
	}
}

func TestAdmitInTx_SuspendedProfileIsRefused(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	_, id := env.seedPartyProfile(t, uid, f.partyID, "suspended")

	lc.reset()
	env.obs.reset()
	if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid}); !errors.Is(err, svc.ErrProfileSuspended) {
		t.Fatalf("err = %v, want ErrProfileSuspended", err)
	}
	if got := env.profileStatus(t, id); got != "suspended" {
		t.Errorf("status = %s, want suspended", got)
	}
	if n := len(env.obs.all()) + len(lc.seen()); n != 0 {
		t.Errorf("a refused admission wrote %d observations or observer calls", n)
	}
}

func TestAdmitInTx_EndedProfileIsReplacedByANewOne(t *testing.T) {
	f := env.newParty(t)
	a, id := env.accepted(t, f)
	uid := env.principalID(t, a.Email)
	env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)

	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if p.UUID.String() == id || p.Status != "active" {
		t.Errorf("profile = %+v, want a new active profile", p)
	}
	if env.profileStatus(t, id) != "ended" {
		t.Error("the ended profile changed")
	}
}

func TestAdmitInTx_Refusals(t *testing.T) {
	f := env.newParty(t)

	t.Run("guest", func(t *testing.T) {
		g := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).wantStatus(t, http.StatusCreated)
		var guest tokens
		g.into(t, &guest)
		_, p := env.probe(t, "/v1/probe", guest.access())
		if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: p.PrincipalID}); !errors.Is(err, svc.ErrGuestMustUpgrade) {
			t.Errorf("err = %v, want ErrGuestMustUpgrade", err)
		}
		if n := env.openProfiles(t, p.PrincipalID, f.partyID); n != 0 {
			t.Errorf("%d profiles left for the guest", n)
		}
	})
	t.Run("managed user of another party", func(t *testing.T) {
		other := env.seedParty(t, "Other "+uniq("p"))
		managed := env.seedManagedUser(t, other, uniq("m")+"@example.test", testPassword)
		if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: managed}); !errors.Is(err, svc.ErrHolderNotAdmissible) {
			t.Errorf("err = %v, want ErrHolderNotAdmissible", err)
		}
	})
	t.Run("managed user of the party itself", func(t *testing.T) {
		managed := env.seedManagedUser(t, f.partyID, uniq("m")+"@example.test", testPassword)
		if p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: managed}); err != nil || p.Status != "active" {
			t.Errorf("admit = %+v, %v; want an active profile", p, err)
		}
	})
	t.Run("principal that is not a user", func(t *testing.T) {
		sa := env.seedServiceAccount(t)
		if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: sa.ID}); !errors.Is(err, svc.ErrHolderNotAdmissible) {
			t.Errorf("err = %v, want ErrHolderNotAdmissible", err)
		}
	})
}

func TestAdmitInTx_ObserverErrorAbortsTheTransaction(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	lc.failWith(errors.New("seat release failed"))
	defer lc.reset()
	if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid}); err == nil {
		t.Fatal("admission succeeded despite the failing observer")
	}
	if n := env.openProfiles(t, uid, f.partyID); n != 0 {
		t.Errorf("a failed observer left %d profiles", n)
	}
	lc.reset()
	if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid}); err != nil {
		t.Errorf("admission after the observer recovered: %v", err)
	}
}

func TestAdmitInTx_InvitedActivationRollsBackWithTheObserver(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	a, id := env.invited(t, f)
	lc.failWith(errors.New("boom"))
	defer lc.reset()
	if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: env.principalID(t, a.Email)}); err == nil {
		t.Fatal("activation succeeded despite the failing observer")
	}
	if got := env.profileStatus(t, id); got != "invited" {
		t.Errorf("status after a failed activation = %s, want invited", got)
	}
}

func TestAdmitInTx_ConcurrentAdmissionsYieldOneProfile(t *testing.T) {
	env.watchLifecycle()
	f := env.newParty(t)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)

	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	ids := make([]int64, n)
	errs := make([]error, n)
	ctxs := make([]context.Context, n)
	for i := range ctxs {
		ctxs[i] = env.authCtx(t, f.adminTok.access())
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = env.inTx(ctxs[i], func(tx pgxTx) error {
				p, err := env.profiles.AdmitInTx(ctxs[i], tx, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
				ids[i] = p.ID
				return err
			})
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Errorf("admission %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Errorf("admission %d returned profile %d, want %d", i, ids[i], ids[0])
		}
	}
	if got := env.count(t, "profiles", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2`, uid, f.partyID); got != 1 {
		t.Errorf("%d profiles after concurrent admissions, want 1", got)
	}
}
