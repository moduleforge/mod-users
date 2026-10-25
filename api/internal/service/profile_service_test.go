package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

func TestTransitionAllowed(t *testing.T) {
	t.Parallel()
	statuses := []string{ProfileStatusInvited, ProfileStatusActive, ProfileStatusSuspended, ProfileStatusEnded}
	allowed := map[string]bool{
		"invited>active": true, "invited>ended": true,
		"active>suspended": true, "active>ended": true,
		"suspended>active": true, "suspended>ended": true,
	}
	for _, from := range statuses {
		for _, to := range statuses {
			key := from + ">" + to
			if got := transitionAllowed(from, to); got != allowed[key] {
				t.Errorf("transitionAllowed(%s) = %v, want %v", key, got, allowed[key])
			}
		}
	}
	if transitionAllowed("ended", "active") || transitionAllowed("ended", "ended") {
		t.Error("ended must be terminal")
	}
}

func TestAdminMayChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from, to string
		want     bool
	}{
		{ProfileStatusActive, ProfileStatusSuspended, true},
		{ProfileStatusSuspended, ProfileStatusActive, true},
		{ProfileStatusInvited, ProfileStatusEnded, true},
		{ProfileStatusActive, ProfileStatusEnded, true},
		{ProfileStatusSuspended, ProfileStatusEnded, true},
		// Accepting an invitation is the holder's alone.
		{ProfileStatusInvited, ProfileStatusActive, false},
		{ProfileStatusInvited, ProfileStatusSuspended, false},
		{ProfileStatusEnded, ProfileStatusActive, false},
		{ProfileStatusActive, ProfileStatusActive, false},
	}
	for _, tc := range tests {
		if got := adminMayChange(tc.from, tc.to); got != tc.want {
			t.Errorf("adminMayChange(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestValidateInvite(t *testing.T) {
	t.Parallel()
	someUUID := uuid.New()
	tests := []struct {
		name      string
		in        InviteInput
		holder    uuid.UUID
		wantCanon string
		wantCode  string
	}{
		{"by email, canonicalized", InviteInput{PartyID: 1, HolderEmail: "  Ada@Example.COM "}, uuid.Nil, "ada@example.com", ""},
		{"by user id", InviteInput{PartyID: 1, HolderUserID: 7}, uuid.Nil, "", ""},
		{"by user uuid", InviteInput{PartyID: 1}, someUUID, "", ""},
		{"no party", InviteInput{HolderEmail: "a@example.com"}, uuid.Nil, "", "users.party_required"},
		{"no holder", InviteInput{PartyID: 1}, uuid.Nil, "", "users.holder_required"},
		{"blank email is no holder", InviteInput{PartyID: 1, HolderEmail: "   "}, uuid.Nil, "", "users.holder_required"},
		{"id and email", InviteInput{PartyID: 1, HolderUserID: 7, HolderEmail: "a@example.com"}, uuid.Nil, "", "users.holder_ambiguous"},
		{"uuid and email", InviteInput{PartyID: 1, HolderEmail: "a@example.com"}, someUUID, "", "users.holder_ambiguous"},
		{"id and uuid", InviteInput{PartyID: 1, HolderUserID: 7}, someUUID, "", "users.holder_ambiguous"},
		{"malformed email", InviteInput{PartyID: 1, HolderEmail: "not-an-email"}, uuid.Nil, "", "users.email_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			canon, err := validateInvite(tc.in, tc.holder)
			if tc.wantCode == "" {
				if err != nil || canon != tc.wantCanon {
					t.Fatalf("validateInvite = %q, %v; want %q", canon, err, tc.wantCanon)
				}
				return
			}
			if !errors.Is(err, apiresp.ErrInvalidInput) {
				t.Fatalf("err = %v, want invalid input", err)
			}
			if !strings.Contains(fmt.Sprint(fieldCodes(err)), tc.wantCode) {
				t.Fatalf("field codes = %v, want %s", fieldCodes(err), tc.wantCode)
			}
		})
	}
}

// fieldCodes returns the codes of the field errors an apiresp error carries,
// read back from the envelope apiresp.WriteError renders.
func fieldCodes(err error) []string {
	rec := httptest.NewRecorder()
	apiresp.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)
	var env struct {
		Error struct {
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(rec.Body.Bytes(), &env); jerr != nil {
		return nil
	}
	var out []string
	for _, d := range env.Error.Details {
		out = append(out, d.Code)
	}
	return out
}

func TestValidateEndReason(t *testing.T) {
	t.Parallel()
	if got, err := validateEndReason("  left the company  "); err != nil || got != "left the company" {
		t.Fatalf("ok = %q, %v", got, err)
	}
	for name, in := range map[string]string{
		"blank":    "   ",
		"empty":    "",
		"too long": strings.Repeat("x", maxEndReasonRunes+1),
		"control":  "bad\x07reason",
		"newline":  "two\nlines",
	} {
		if _, err := validateEndReason(in); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s: got %v, want invalid input", name, err)
		}
	}
	if _, err := validateEndReason(strings.Repeat("é", maxEndReasonRunes)); err != nil {
		t.Errorf("the bound counts characters, not bytes: %v", err)
	}
}

func TestValidateStatusUpdate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		in         UpdateProfileStatusInput
		wantTo     string
		wantReason string
		wantErr    bool
	}{
		{"suspend", UpdateProfileStatusInput{Status: "suspended"}, "suspended", "", false},
		{"resume", UpdateProfileStatusInput{Status: "active"}, "active", "", false},
		{"end with a reason", UpdateProfileStatusInput{Status: "ended", EndReason: " contract over "}, "ended", "contract over", false},
		{"end without a reason", UpdateProfileStatusInput{Status: "ended"}, "", "", true},
		{"reason on a suspend", UpdateProfileStatusInput{Status: "suspended", EndReason: "x"}, "", "", true},
		{"no status", UpdateProfileStatusInput{}, "", "", true},
		{"invited is not a target", UpdateProfileStatusInput{Status: "invited"}, "", "", true},
		{"unknown status", UpdateProfileStatusInput{Status: "banished"}, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			to, reason, err := validateStatusUpdate(tc.in)
			if tc.wantErr {
				if !errors.Is(err, apiresp.ErrInvalidInput) {
					t.Fatalf("err = %v, want invalid input", err)
				}
				return
			}
			if err != nil || to != tc.wantTo || reason != tc.wantReason {
				t.Fatalf("got %q %q %v, want %q %q", to, reason, err, tc.wantTo, tc.wantReason)
			}
		})
	}
}

func TestMapProfileInsertError(t *testing.T) {
	t.Parallel()
	guest := usersdb.User{IsGuest: true}
	managed := usersdb.User{ManagingPartyID: pgtype.Int8{Int64: 9, Valid: true}}
	byo := usersdb.User{}
	tests := []struct {
		name   string
		err    error
		holder usersdb.User
		want   error
	}{
		{"open profile index", &pgconn.PgError{Code: "23505", ConstraintName: ProfilesOneOpenPerHolderParty}, byo, ErrProfileExists},
		{"MF103 for a guest", &pgconn.PgError{Code: UserInvariantSQLState}, guest, ErrGuestMustUpgrade},
		{"MF103 for a managed user", &pgconn.PgError{Code: UserInvariantSQLState}, managed, ErrHolderNotAdmissible},
		{"MF004 holder rule", &pgconn.PgError{Code: profileInvariantSQLState}, byo, ErrHolderNotAdmissible},
		{"wrapped MF103", fmt.Errorf("insert: %w", &pgconn.PgError{Code: UserInvariantSQLState}), managed, ErrHolderNotAdmissible},
	}
	for _, tc := range tests {
		if got := mapProfileInsertError(tc.err, tc.holder); !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	other := &pgconn.PgError{Code: "23505", ConstraintName: "some_other_uq"}
	got := mapProfileInsertError(other, byo)
	if errors.Is(got, apiresp.ErrConflict) || !errors.Is(got, other) {
		t.Errorf("an unmapped error must stay internal and wrapped, got %v", got)
	}
}

func TestCheckAdmissible(t *testing.T) {
	t.Parallel()
	managedOf := func(party int64) usersdb.User {
		return usersdb.User{ManagingPartyID: pgtype.Int8{Int64: party, Valid: true}}
	}
	tests := []struct {
		name   string
		holder usersdb.User
		party  int64
		want   error
	}{
		{"bring-your-own user", usersdb.User{}, 5, nil},
		{"guest", usersdb.User{IsGuest: true}, 5, ErrGuestMustUpgrade},
		{"managed user of this party", managedOf(5), 5, nil},
		{"managed user of another party", managedOf(6), 5, ErrHolderNotAdmissible},
	}
	for _, tc := range tests {
		got := checkAdmissible(tc.holder, tc.party)
		if tc.want == nil && got != nil || tc.want != nil && !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

type recordingLifecycle struct {
	name string
	log  *[]string
	err  error
}

func (r recordingLifecycle) ProfileStatusChanged(_ context.Context, _ pgx.Tx, ch ProfileStatusChange) error {
	*r.log = append(*r.log, fmt.Sprintf("%s:%s>%s", r.name, ch.From, ch.To))
	return r.err
}

func TestNotifyRunsObserversInOrderAndStopsOnError(t *testing.T) {
	t.Parallel()
	var log []string
	s := &ProfileService{}
	s.AddLifecycleObserver(nil) // ignored
	s.AddLifecycleObserver(recordingLifecycle{name: "a", log: &log})
	boom := errors.New("seat release failed")
	s.AddLifecycleObserver(recordingLifecycle{name: "b", log: &log, err: boom})
	s.AddLifecycleObserver(recordingLifecycle{name: "c", log: &log})

	err := s.notify(context.Background(), nil, ProfileStatusChange{From: "invited", To: "active"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the observer's error wrapped", err)
	}
	if got := strings.Join(log, ","); got != "a:invited>active,b:invited>active" {
		t.Fatalf("calls = %s, want a then b, then stop", got)
	}

	log = nil
	ok := &ProfileService{}
	ok.AddLifecycleObserver(recordingLifecycle{name: "a", log: &log})
	if err := ok.notify(context.Background(), nil, ProfileStatusChange{To: "invited"}); err != nil || strings.Join(log, ",") != "a:>invited" {
		t.Fatalf("creation notify = %v, %v", log, err)
	}
}

func TestMaskForbidden(t *testing.T) {
	t.Parallel()
	other := errors.New("db down")
	if maskForbidden(nil) != nil {
		t.Error("nil must stay nil")
	}
	if !errors.Is(maskForbidden(apiresp.ErrForbidden), apiresp.ErrNotFound) {
		t.Error("forbidden must be masked as not found")
	}
	if !errors.Is(maskForbidden(ErrStepUpRequired), ErrStepUpRequired) {
		t.Error("a step-up action must not be masked")
	}
	if !errors.Is(maskForbidden(other), other) || !errors.Is(maskForbidden(apiresp.ErrUnauthenticated), apiresp.ErrUnauthenticated) {
		t.Error("other errors must pass through")
	}
}

// ---------------------------------------------------------------------------
// authority and error order, against fakes
// ---------------------------------------------------------------------------

// opAuthorizer answers per operation; an operation not listed is allowed.
type opAuthorizer struct {
	deny   map[string]error
	called []string
}

func (a *opAuthorizer) Authorize(_ context.Context, op string, _ *int64) error {
	a.called = append(a.called, op)
	return a.deny[op]
}

// AuthorizeTypeIn makes the fake an OwnerScopedTypeAuthorizer, so the type-in
// checks are recorded as "<op>:type".
func (a *opAuthorizer) AuthorizeType(_ context.Context, op string, _ int64) error {
	a.called = append(a.called, op+":type")
	return a.deny[op+":type"]
}

func (a *opAuthorizer) AuthorizeTypeIn(_ context.Context, op string, _, _ int64) error {
	a.called = append(a.called, op+":type-in")
	return a.deny[op+":type-in"]
}

type profileCoreQ struct {
	coredb.Querier
	profile    func(uuid.UUID) (coredb.Profile, error)
	entityByID func(int64) (coredb.GetEntityByIDRow, error)
	legal      func(int64) (int64, error)
	principal  func(int64) (coredb.Principal, error)
}

func (f *profileCoreQ) GetTypeBySlug(_ context.Context, slug string) (coredb.Type, error) {
	return coredb.Type{ID: 2, Slug: slug}, nil
}

func (f *profileCoreQ) GetProfileByUUID(_ context.Context, id uuid.UUID) (coredb.Profile, error) {
	return f.profile(id)
}

func (f *profileCoreQ) GetEntityByID(_ context.Context, id int64) (coredb.GetEntityByIDRow, error) {
	if f.entityByID == nil {
		return coredb.GetEntityByIDRow{ID: id}, nil
	}
	return f.entityByID(id)
}

func (f *profileCoreQ) GetLegalEntityByEntityID(_ context.Context, id int64) (int64, error) {
	if f.legal == nil {
		return id, nil
	}
	return f.legal(id)
}

func (f *profileCoreQ) GetPrincipalByEntityID(_ context.Context, id int64) (coredb.Principal, error) {
	return f.principal(id)
}

type profileUsersQ struct {
	usersdb.Querier
	identifier func(usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error)
	user       func(int64) (usersdb.User, error)
}

func (f *profileUsersQ) GetIdentifierByKindValue(_ context.Context, a usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error) {
	return f.identifier(a)
}

func (f *profileUsersQ) GetUserByEntityID(_ context.Context, id int64) (usersdb.User, error) {
	return f.user(id)
}

func newInviteTestService(az *opAuthorizer, cq *profileCoreQ, uq *profileUsersQ) *ProfileService {
	s := NewProfileService(nil, uq, cq, az, nil, &UserService{now: time.Now}, nil, nil)
	s.newQ = func(pgx.Tx) usersdb.Querier { return uq }
	s.newCoreQ = func(pgx.Tx) coredb.Querier { return cq }
	return s
}

var sessionAdmin = opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, AuthTime: time.Now()}

func TestInviteInTx_AuthorizesBeforeLookingUpTheHolder(t *testing.T) {
	t.Parallel()
	az := &opAuthorizer{deny: map[string]error{"create:type-in": apiresp.ErrForbidden}}
	// A nil holder lookup panics: the refused caller must learn nothing.
	s := newInviteTestService(az, &profileCoreQ{}, &profileUsersQ{})
	_, err := s.InviteInTx(authCtx(sessionAdmin), nil, InviteInput{PartyID: 5, HolderEmail: "ada@example.com"})
	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if len(az.called) != 1 || az.called[0] != "create:type-in" {
		t.Fatalf("authorizer calls = %v, want one create check on the profile type in the party", az.called)
	}
}

func TestInviteInTx_ErrorOrder(t *testing.T) {
	t.Parallel()
	az := &opAuthorizer{}
	s := newInviteTestService(az, &profileCoreQ{}, &profileUsersQ{})
	// 400 before authentication and authorization.
	if _, err := s.InviteInTx(context.Background(), nil, InviteInput{PartyID: 5}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Fatalf("no holder = %v, want invalid input", err)
	}
	if _, err := s.InviteInTx(context.Background(), nil, InviteInput{PartyID: 5, HolderEmail: "a@example.com"}); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Fatalf("no AuthContext = %v, want unauthenticated", err)
	}
	if len(az.called) != 0 {
		t.Fatalf("authorizer called before authentication and validation: %v", az.called)
	}
}

func TestInviteInTx_HolderResolution(t *testing.T) {
	t.Parallel()
	const party = int64(5)
	managedUser := func(of int64) usersdb.User {
		return usersdb.User{EntityID: 40, ManagingPartyID: pgtype.Int8{Int64: of, Valid: true}}
	}
	tests := []struct {
		name  string
		in    InviteInput
		ident func(usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error)
		user  func(int64) (usersdb.User, error)
		prin  func(int64) (coredb.Principal, error)
		want  error
	}{
		{
			name: "unknown email is not found and creates no account",
			in:   InviteInput{PartyID: party, HolderEmail: "Nobody@Example.com"},
			ident: func(a usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error) {
				if a.Kind != "email" || a.ValueCanon != "nobody@example.com" {
					t.Errorf("lookup = %+v, want the canonical email", a)
				}
				return usersdb.UserIdentifier{}, pgx.ErrNoRows
			},
			want: apiresp.ErrNotFound,
		},
		{
			name: "guest holder must upgrade",
			in:   InviteInput{PartyID: party, HolderEmail: "g@example.com"},
			ident: func(usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error) {
				return usersdb.UserIdentifier{UserID: 40}, nil
			},
			user: func(int64) (usersdb.User, error) { return usersdb.User{EntityID: 40, IsGuest: true}, nil },
			want: ErrGuestMustUpgrade,
		},
		{
			name: "managed user of another party",
			in:   InviteInput{PartyID: party, HolderEmail: "m@example.com"},
			ident: func(usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error) {
				return usersdb.UserIdentifier{UserID: 40}, nil
			},
			user: func(int64) (usersdb.User, error) { return managedUser(party + 1), nil },
			want: ErrHolderNotAdmissible,
		},
		{
			name: "a service account is not admissible",
			in:   InviteInput{PartyID: party, HolderUserID: 41},
			user: func(int64) (usersdb.User, error) { return usersdb.User{}, pgx.ErrNoRows },
			prin: func(id int64) (coredb.Principal, error) { return coredb.Principal{EntityID: id}, nil },
			want: ErrHolderNotAdmissible,
		},
		{
			name: "an unknown entity is not found",
			in:   InviteInput{PartyID: party, HolderUserID: 42},
			user: func(int64) (usersdb.User, error) { return usersdb.User{}, pgx.ErrNoRows },
			prin: func(int64) (coredb.Principal, error) { return coredb.Principal{}, pgx.ErrNoRows },
			want: apiresp.ErrNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newInviteTestService(&opAuthorizer{}, &profileCoreQ{principal: tc.prin}, &profileUsersQ{identifier: tc.ident, user: tc.user})
			_, err := s.InviteInTx(authCtx(sessionAdmin), nil, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestInviteInTx_UnknownOrArchivedPartyIsNotFound(t *testing.T) {
	t.Parallel()
	archived := time.Now()
	for name, cq := range map[string]*profileCoreQ{
		"no such entity": {entityByID: func(int64) (coredb.GetEntityByIDRow, error) { return coredb.GetEntityByIDRow{}, pgx.ErrNoRows }},
		"archived": {entityByID: func(id int64) (coredb.GetEntityByIDRow, error) {
			return coredb.GetEntityByIDRow{ID: id, ArchivedAt: &archived}, nil
		}},
		"not legal entity": {legal: func(int64) (int64, error) { return 0, pgx.ErrNoRows }},
	} {
		s := newInviteTestService(&opAuthorizer{}, cq, &profileUsersQ{})
		_, err := s.InviteInTx(authCtx(sessionAdmin), nil, InviteInput{PartyID: 5, HolderUserID: 40})
		if !errors.Is(err, apiresp.ErrNotFound) {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
}

func TestHolderActionsAuthority(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	holder := int64(1)
	cq := &profileCoreQ{profile: func(uuid.UUID) (coredb.Profile, error) {
		return coredb.Profile{EntityID: 77, HolderID: holder, PartyID: pgtype.Int8{Int64: 5, Valid: true}, Status: ProfileStatusInvited}, nil
	}}
	s := NewProfileService(nil, nil, cq, nil, nil, &UserService{now: time.Now}, nil, nil)

	calls := map[string]func(ctx context.Context) error{
		"accept":  func(ctx context.Context) error { _, err := s.AcceptInvitation(ctx, id); return err },
		"decline": func(ctx context.Context) error { return s.DeclineInvitation(ctx, id) },
		"leave":   func(ctx context.Context) error { return s.LeaveProfile(ctx, id) },
	}
	for name, call := range calls {
		cases := []struct {
			label string
			ctx   context.Context
			want  error
		}{
			{"no AuthContext", context.Background(), apiresp.ErrUnauthenticated},
			{"client credentials (no session)", authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, CredentialID: 9}), apiresp.ErrForbidden},
			{"impersonating administrator", authCtx(opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, ImpersonatorPrincipalID: 8}), apiresp.ErrForbidden},
			// The holder is read from the AuthContext: another principal's
			// session sees a masked 404, not a 403.
			{"another principal", authCtx(opctx.AuthContext{PrincipalID: 99, ProfileID: 2, SessionID: 3}), apiresp.ErrNotFound},
		}
		for _, tc := range cases {
			if err := call(tc.ctx); !errors.Is(err, tc.want) {
				t.Errorf("%s / %s: err = %v, want %v", name, tc.label, err, tc.want)
			}
		}
	}

	// A personal profile has no lifecycle here: it is masked like a missing one.
	cq.profile = func(uuid.UUID) (coredb.Profile, error) {
		return coredb.Profile{EntityID: 78, HolderID: holder, Status: ProfileStatusActive}, nil
	}
	if err := s.LeaveProfile(authCtx(sessionAdmin), id); !errors.Is(err, apiresp.ErrNotFound) {
		t.Errorf("leave personal profile = %v, want not found", err)
	}
}

func TestUpdateProfileStatus_ErrorOrder(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	party := pgtype.Int8{Int64: 5, Valid: true}
	found := func(uuid.UUID) (coredb.Profile, error) {
		return coredb.Profile{EntityID: 77, HolderID: 40, PartyID: party, Status: ProfileStatusActive}, nil
	}
	now := time.Now()
	fresh := opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, AuthTime: now}
	stale := opctx.AuthContext{PrincipalID: 1, ProfileID: 2, SessionID: 3, AuthTime: now.Add(-time.Hour)}
	suspend := UpdateProfileStatusInput{Status: "suspended"}

	tests := []struct {
		name string
		ctx  context.Context
		in   UpdateProfileStatusInput
		prof func(uuid.UUID) (coredb.Profile, error)
		deny map[string]error
		want error
	}{
		{"401 before everything", context.Background(), UpdateProfileStatusInput{}, found, nil, apiresp.ErrUnauthenticated},
		{"400 before any lookup", authCtx(fresh), UpdateProfileStatusInput{Status: "ended"}, nil, nil, apiresp.ErrInvalidInput},
		{"404 for an unknown profile", authCtx(fresh), suspend,
			func(uuid.UUID) (coredb.Profile, error) { return coredb.Profile{}, pgx.ErrNoRows }, nil, apiresp.ErrNotFound},
		{"404 for a personal profile", authCtx(fresh), suspend,
			func(uuid.UUID) (coredb.Profile, error) { return coredb.Profile{EntityID: 78, HolderID: 40}, nil }, nil, apiresp.ErrNotFound},
		{"404 when read is refused", authCtx(fresh), suspend, found, map[string]error{"read": apiresp.ErrForbidden}, apiresp.ErrNotFound},
		{"403 when update is refused", authCtx(fresh), suspend, found, map[string]error{"update": apiresp.ErrForbidden}, apiresp.ErrForbidden},
		{"409 step-up when the authentication is stale", authCtx(stale), suspend, found, nil, ErrStepUpRequired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cq := &profileCoreQ{profile: tc.prof}
			az := &opAuthorizer{deny: tc.deny}
			s := NewProfileService(nil, nil, cq, az, nil, &UserService{now: time.Now}, nil, nil)
			_, err := s.UpdateProfileStatus(tc.ctx, id, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// A stale authentication is refused only after `update`: the step-up never
	// reveals a profile the caller cannot update.
	az := &opAuthorizer{deny: map[string]error{"update": apiresp.ErrForbidden}}
	s := NewProfileService(nil, nil, &profileCoreQ{profile: found}, az, nil, &UserService{now: time.Now}, nil, nil)
	if _, err := s.UpdateProfileStatus(authCtx(stale), id, suspend); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("stale + no update authority = %v, want forbidden", err)
	}

	// With authority and a fresh authentication the call reaches the
	// transaction; an unwired pool is a configuration error, not a refusal.
	s = NewProfileService(nil, nil, &profileCoreQ{profile: found}, &opAuthorizer{}, nil, &UserService{now: time.Now}, nil, nil)
	_, err := s.UpdateProfileStatus(authCtx(fresh), id, suspend)
	if err == nil || errors.Is(err, ErrStepUpRequired) || errors.Is(err, apiresp.ErrForbidden) || errors.Is(err, apiresp.ErrNotFound) {
		t.Errorf("authorized + fresh = %v, want the unwired-pool error", err)
	}
}

func TestPartyScopedRoutesRefuseBeforeTouchingState(t *testing.T) {
	t.Parallel()
	s := NewProfileService(nil, nil, &profileCoreQ{}, &opAuthorizer{}, nil, &UserService{now: time.Now}, nil, nil)
	party := uuid.New()
	ctx := authCtx(sessionAdmin)

	if _, err := s.ListPartyProfiles(context.Background(), party, ListPartyProfilesInput{}); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Errorf("list, no auth = %v", err)
	}
	if _, err := s.ListPartyProfiles(ctx, party, ListPartyProfilesInput{Status: "banished"}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("list, bad status = %v", err)
	}
	if _, err := s.InviteProfile(ctx, party, InviteProfileInput{}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("invite, no holder = %v", err)
	}
	if _, err := s.InviteProfile(ctx, party, InviteProfileInput{HolderEmail: "a@example.com", HolderUserUUID: uuid.New()}); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("invite, both holders = %v", err)
	}
	if _, err := s.CreateManagedUser(ctx, party, "not-an-email", "correct-horse-battery"); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("managed user, bad email = %v", err)
	}
	if _, err := s.CreateManagedUser(ctx, party, "m@example.com", "short"); !errors.Is(err, apiresp.ErrInvalidInput) {
		t.Errorf("managed user, short password = %v", err)
	}
}

func TestProvisionManagedUserInTx_ValidatesAndAuthorizesBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	az := &opAuthorizer{deny: map[string]error{"create:type-in": apiresp.ErrForbidden}}
	s := newInviteTestService(az, &profileCoreQ{}, &profileUsersQ{})
	ctx := authCtx(sessionAdmin)
	for name, in := range map[string]ManagedUserInput{
		"no party":       {Email: "m@example.com", Password: "correct-horse-battery"},
		"bad email":      {PartyID: 5, Email: "nope", Password: "correct-horse-battery"},
		"short password": {PartyID: 5, Email: "m@example.com", Password: "short"},
	} {
		if _, err := s.ProvisionManagedUserInTx(ctx, nil, in); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
	if len(az.called) != 0 {
		t.Fatalf("authorizer consulted for an invalid request: %v", az.called)
	}
	// The profile create check precedes the user creation; a refusal writes
	// nothing (the nil users service would panic on a write).
	_, err := s.ProvisionManagedUserInTx(ctx, nil, ManagedUserInput{PartyID: 5, Email: "m@example.com", Password: "correct-horse-battery"})
	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Fatalf("refused = %v, want forbidden", err)
	}
}
