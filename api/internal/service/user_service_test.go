package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// creationLog records the write order across the core and users fakes.
type creationLog struct{ steps []string }

func (l *creationLog) add(s string) { l.steps = append(l.steps, s) }

type creationCoreQ struct {
	coredb.Querier
	log      *creationLog
	nextID   int64
	entities map[int64]coredb.CreateEntityParams
	profiles []coredb.CreateProfileParams
}

func newCreationCoreQ(log *creationLog) *creationCoreQ {
	return &creationCoreQ{log: log, nextID: 100, entities: map[int64]coredb.CreateEntityParams{}}
}

func (c *creationCoreQ) GetTypeBySlug(_ context.Context, slug string) (coredb.Type, error) {
	return coredb.Type{ID: map[string]int64{"user": 1, "profile": 2}[slug], Slug: slug}, nil
}

func (c *creationCoreQ) CreateEntity(_ context.Context, a coredb.CreateEntityParams) (coredb.Entity, error) {
	c.nextID++
	c.entities[c.nextID] = a
	if a.FundamentalTypeID == 1 {
		c.log.add("entity:user")
	} else {
		c.log.add("entity:profile")
	}
	return coredb.Entity{ID: c.nextID, Uuid: uuid.New(), FundamentalTypeID: a.FundamentalTypeID, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}, nil
}

func (c *creationCoreQ) CreatePrincipal(_ context.Context, id int64) (coredb.Principal, error) {
	c.log.add("principal")
	return coredb.Principal{EntityID: id}, nil
}

func (c *creationCoreQ) CreateProfile(_ context.Context, a coredb.CreateProfileParams) (coredb.Profile, error) {
	c.log.add("profile")
	c.profiles = append(c.profiles, a)
	return coredb.Profile{EntityID: a.EntityID, HolderID: a.HolderID}, nil
}

type fakeNaturalPersons struct {
	coreservice.NaturalPersonServicer
	log   *creationLog
	input coreservice.CreateNaturalPersonInput
	actor int64
}

func (f *fakeNaturalPersons) CreateInTx(ctx context.Context, _ pgx.Tx, in coreservice.CreateNaturalPersonInput) (coredb.CreateNaturalPersonRow, uuid.UUID, int64, error) {
	f.log.add("person")
	f.input = in
	f.actor = observedProfile(ctx)
	return coredb.CreateNaturalPersonRow{}, uuid.New(), 500, nil
}

func observedProfile(ctx context.Context) int64 {
	rec := &recordingObserver{}
	_ = observer.NewObserverGroup(rec).Observe(ctx, nil, "create", "x", nil, nil, nil)
	return rec.seen[0].Actor
}

func creationUsersQ(log *creationLog) *fakeUsersQ {
	return &fakeUsersQ{
		createUser: func(a usersdb.CreateUserParams) (usersdb.User, error) {
			log.add("users")
			return usersdb.User{EntityID: a.EntityID, IsGuest: a.IsGuest, ManagingPartyID: a.ManagingPartyID}, nil
		},
		setUserPerson: func(a usersdb.SetUserPersonParams) (usersdb.User, error) {
			log.add("link-person")
			return usersdb.User{EntityID: a.EntityID, PersonID: a.PersonID}, nil
		},
		createCredential: func(a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
			log.add("credential:" + a.Kind)
			return credRow(9, a.PrincipalID, a.Kind), nil
		},
		createAnon: func(a usersdb.CreateAnonCredentialParams) (usersdb.CredAnon, error) {
			return usersdb.CredAnon{CredentialID: a.CredentialID}, nil
		},
	}
}

func newTestUserService(q *fakeUsersQ, cq coredb.Querier, np coreservice.NaturalPersonServicer, az *fakeAuthorizer, rec *recordingObserver) *UserService {
	obs := observer.NewObserverGroup(rec)
	var iaz coreAuthz.Authorizer
	if az != nil {
		iaz = az
	}
	creds := NewCredentialService(q, cq, iaz, obs, nil)
	creds.newQ = func(pgx.Tx) usersdb.Querier { return q }
	creds.newCoreQ = func(pgx.Tx) coredb.Querier { return cq }
	s := NewUserService(q, cq, iaz, obs, np, creds)
	s.newQ = func(pgx.Tx) usersdb.Querier { return q }
	s.newCoreQ = func(pgx.Tx) coredb.Querier { return cq }
	return s
}

func TestCreateBYOUserInTx(t *testing.T) {
	t.Parallel()
	log := &creationLog{}
	cq := newCreationCoreQ(log)
	np := &fakeNaturalPersons{log: log}
	rec := &recordingObserver{}
	s := newTestUserService(creationUsersQ(log), cq, np, nil, rec)

	got, err := s.CreateBYOUserInTx(actorCtx(42), nil, CreateBYOUserInput{GivenName: " Ada ", FamilyName: "Lovelace"})
	if err != nil {
		t.Fatalf("CreateBYOUserInTx: %v", err)
	}

	wantOrder := []string{"entity:user", "principal", "users", "entity:profile", "profile", "person", "link-person"}
	if len(log.steps) != len(wantOrder) {
		t.Fatalf("steps = %v, want %v", log.steps, wantOrder)
	}
	for i := range wantOrder {
		if log.steps[i] != wantOrder[i] {
			t.Fatalf("steps = %v, want %v", log.steps, wantOrder)
		}
	}

	userID := got.User.EntityID
	userParams := cq.entities[userID]
	if userParams.OwnerID.Valid {
		t.Error("the user entity owner must be left NULL for the database to default it to the user itself")
	}
	if !userParams.CreatedByProfileID.Valid || userParams.CreatedByProfileID.Int64 != 42 {
		t.Errorf("user created_by = %+v, want the acting profile 42", userParams.CreatedByProfileID)
	}
	profileParams := cq.entities[got.PersonalProfileID]
	if !profileParams.OwnerID.Valid || profileParams.OwnerID.Int64 != userID {
		t.Errorf("profile owner = %+v, want the user %d", profileParams.OwnerID, userID)
	}
	if len(cq.profiles) != 1 || cq.profiles[0].HolderID != userID || cq.profiles[0].PartyID.Valid || cq.profiles[0].Status != "active" {
		t.Errorf("profiles = %+v", cq.profiles)
	}

	if np.input.GivenName != "Ada" || np.input.OwnerID == nil || *np.input.OwnerID != userID ||
		np.input.CreatedByProfileID == nil || *np.input.CreatedByProfileID != got.PersonalProfileID {
		t.Errorf("person input = %+v, want owned by the user and created by the personal profile", np.input)
	}
	if np.actor != got.PersonalProfileID {
		t.Errorf("person observed as profile %d, want the new personal profile %d", np.actor, got.PersonalProfileID)
	}
	if got.User.PersonID.Int64 != 500 || got.PersonID != 500 {
		t.Errorf("person link = %+v / %d", got.User.PersonID, got.PersonID)
	}

	want := observation{Op: "create", Resource: "user", Target: userID, Actor: got.PersonalProfileID}
	if len(rec.seen) != 1 || rec.seen[0] != want {
		t.Errorf("observations = %+v, want [%+v]", rec.seen, want)
	}
}

func TestCreateBYOUserInTx_RequiresNames(t *testing.T) {
	t.Parallel()
	log := &creationLog{}
	s := newTestUserService(creationUsersQ(log), newCreationCoreQ(log), &fakeNaturalPersons{log: log}, nil, &recordingObserver{})
	for _, in := range []CreateBYOUserInput{{FamilyName: "L"}, {GivenName: "A"}, {GivenName: " ", FamilyName: "L"}} {
		if _, err := s.CreateBYOUserInTx(actorCtx(42), nil, in); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%+v: got %v, want invalid input", in, err)
		}
	}
	if len(log.steps) != 0 {
		t.Errorf("validation must precede every write, got %v", log.steps)
	}
}

func TestCreateGuestUserInTx(t *testing.T) {
	t.Parallel()
	log := &creationLog{}
	cq := newCreationCoreQ(log)
	rec := &recordingObserver{}
	q := creationUsersQ(log)
	s := newTestUserService(q, cq, &fakeNaturalPersons{log: log}, nil, rec)

	got, err := s.CreateGuestUserInTx(context.Background(), nil, CreateGuestUserInput{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("CreateGuestUserInTx: %v", err)
	}
	if !got.User.IsGuest || got.PersonID != 0 {
		t.Errorf("guest = %+v", got)
	}
	for _, step := range log.steps {
		if step == "person" || step == "link-person" {
			t.Errorf("a guest has no person record, steps = %v", log.steps)
		}
	}
	if cq.entities[got.User.EntityID].CreatedByProfileID.Valid {
		t.Error("without an acting profile on ctx, created_by must be NULL")
	}
	if got.Anon.Token == "" || got.Anon.Credential.Kind != KindAnon {
		t.Errorf("anon = %+v", got.Anon)
	}
	want := []observation{
		{Op: "create", Resource: "user", Target: got.User.EntityID, Actor: got.PersonalProfileID},
		{Op: "create", Resource: "credential", Target: got.User.EntityID, Actor: got.PersonalProfileID},
	}
	if len(rec.seen) != 2 || rec.seen[0] != want[0] || rec.seen[1] != want[1] {
		t.Errorf("observations = %+v, want %+v", rec.seen, want)
	}
}

func TestCreateManagedUserInTx(t *testing.T) {
	t.Parallel()

	t.Run("denied is refused before any write", func(t *testing.T) {
		t.Parallel()
		log := &creationLog{}
		az := &fakeAuthorizer{err: apiresp.ErrForbidden}
		s := newTestUserService(creationUsersQ(log), newCreationCoreQ(log), nil, az, &recordingObserver{})
		if _, err := s.CreateManagedUserInTx(actorCtx(42), nil, CreateManagedUserInput{ManagingPartyID: 7}); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("got %v, want forbidden", err)
		}
		if len(log.steps) != 0 {
			t.Errorf("writes before authorization: %v", log.steps)
		}
	})

	t.Run("no authorizer fails closed", func(t *testing.T) {
		t.Parallel()
		log := &creationLog{}
		s := newTestUserService(creationUsersQ(log), newCreationCoreQ(log), nil, nil, &recordingObserver{})
		if _, err := s.CreateManagedUserInTx(actorCtx(42), nil, CreateManagedUserInput{ManagingPartyID: 7}); !errors.Is(err, apiresp.ErrForbidden) {
			t.Fatalf("got %v, want forbidden", err)
		}
	})

	t.Run("owned by the party with no person and no personal profile", func(t *testing.T) {
		t.Parallel()
		log := &creationLog{}
		cq := newCreationCoreQ(log)
		rec := &recordingObserver{}
		s := newTestUserService(creationUsersQ(log), cq, nil, &fakeAuthorizer{}, rec)
		got, err := s.CreateManagedUserInTx(actorCtx(42), nil, CreateManagedUserInput{ManagingPartyID: 7})
		if err != nil {
			t.Fatalf("CreateManagedUserInTx: %v", err)
		}
		if p := cq.entities[got.User.EntityID]; !p.OwnerID.Valid || p.OwnerID.Int64 != 7 {
			t.Errorf("owner = %+v, want the managing party 7", p.OwnerID)
		}
		if !got.User.ManagingPartyID.Valid || got.User.ManagingPartyID.Int64 != 7 || got.PersonalProfileID != 0 || got.PersonID != 0 {
			t.Errorf("managed user = %+v", got)
		}
		if len(cq.profiles) != 0 {
			t.Errorf("a managed user gets no personal profile, got %+v", cq.profiles)
		}
		want := observation{Op: "create", Resource: "user", Target: got.User.EntityID, Actor: 42}
		if len(rec.seen) != 1 || rec.seen[0] != want {
			t.Errorf("observations = %+v, want [%+v]", rec.seen, want)
		}
	})
}

func identifierRow(id, user int64, value string, primary bool, verified bool) usersdb.UserIdentifier {
	r := usersdb.UserIdentifier{ID: id, Uuid: uuid.New(), UserID: user, Kind: "email", ValueCanon: value, IsPrimary: primary}
	if verified {
		now := time.Now()
		r.VerifiedAt = &now
	}
	return r
}

func TestAddIdentifierInTx(t *testing.T) {
	t.Parallel()

	base := func() *fakeUsersQ {
		return &fakeUsersQ{
			getUser: func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil },
			createIdentifier: func(a usersdb.CreateIdentifierParams) (usersdb.UserIdentifier, error) {
				r := identifierRow(11, a.UserID, a.ValueCanon, a.IsPrimary, a.VerifiedAt != nil)
				return r, nil
			},
		}
	}

	t.Run("canonicalizes and makes the first email primary", func(t *testing.T) {
		t.Parallel()
		q := base()
		q.getPrimaryEmail = func(int64) (usersdb.UserIdentifier, error) { return usersdb.UserIdentifier{}, pgx.ErrNoRows }
		rec := &recordingObserver{}
		s := newTestUserService(q, nil, nil, nil, rec)
		id, err := s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "  Ada@Example.COM "})
		if err != nil {
			t.Fatalf("AddIdentifierInTx: %v", err)
		}
		if id.Value != "ada@example.com" || !id.IsPrimary {
			t.Errorf("identifier = %+v", id)
		}
		want := observation{Op: "create", Resource: "user_identifier", Target: 5, Actor: 42}
		if len(rec.seen) != 1 || rec.seen[0] != want {
			t.Errorf("observations = %+v", rec.seen)
		}
	})

	t.Run("a later email is not primary unless asked", func(t *testing.T) {
		t.Parallel()
		q := base()
		q.getPrimaryEmail = func(int64) (usersdb.UserIdentifier, error) {
			return identifierRow(1, 5, "x@y.example", true, true), nil
		}
		s := newTestUserService(q, nil, nil, nil, &recordingObserver{})
		id, err := s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "b@y.example"})
		if err != nil || id.IsPrimary || q.clearPrimary != 0 {
			t.Fatalf("id=%+v err=%v cleared=%d", id, err, q.clearPrimary)
		}
		id, err = s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "c@y.example", Primary: true})
		if err != nil || !id.IsPrimary || q.clearPrimary != 1 {
			t.Fatalf("id=%+v err=%v cleared=%d", id, err, q.clearPrimary)
		}
	})

	t.Run("a taken value maps to ErrIdentifierTaken", func(t *testing.T) {
		t.Parallel()
		q := base()
		q.getPrimaryEmail = func(int64) (usersdb.UserIdentifier, error) {
			return identifierRow(1, 5, "x@y.example", true, true), nil
		}
		q.createIdentifier = func(usersdb.CreateIdentifierParams) (usersdb.UserIdentifier, error) {
			return usersdb.UserIdentifier{}, &pgconn.PgError{Code: "23505", ConstraintName: IdentifierKindValueIndex}
		}
		rec := &recordingObserver{}
		s := newTestUserService(q, nil, nil, nil, rec)
		_, err := s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "b@y.example"})
		if !errors.Is(err, ErrIdentifierTaken) || !errors.Is(err, apiresp.ErrConflict) {
			t.Fatalf("got %v, want ErrIdentifierTaken", err)
		}
		if len(rec.seen) != 0 {
			t.Errorf("observations on failure: %+v", rec.seen)
		}
	})

	t.Run("a guest must upgrade first", func(t *testing.T) {
		t.Parallel()
		q := base()
		q.getUser = func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id, IsGuest: true}, nil }
		s := newTestUserService(q, nil, nil, nil, &recordingObserver{})
		if _, err := s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "b@y.example"}); !errors.Is(err, ErrGuestMustUpgrade) {
			t.Fatalf("got %v, want ErrGuestMustUpgrade", err)
		}
	})

	t.Run("bad kind and bad value", func(t *testing.T) {
		t.Parallel()
		s := newTestUserService(base(), nil, nil, nil, &recordingObserver{})
		for _, in := range []AddIdentifierInput{
			{UserID: 5, Kind: "phone", Value: "+15555550100"},
			{UserID: 5, Kind: "email", Value: "not-an-email"},
			{UserID: 5, Kind: "email", Value: "   "},
		} {
			if _, err := s.AddIdentifierInTx(actorCtx(42), nil, in); !errors.Is(err, apiresp.ErrInvalidInput) {
				t.Errorf("%+v: got %v, want invalid input", in, err)
			}
		}
	})

	t.Run("pre-verified creates the email_otp credential", func(t *testing.T) {
		t.Parallel()
		q := base()
		q.getPrimaryEmail = func(int64) (usersdb.UserIdentifier, error) { return usersdb.UserIdentifier{}, pgx.ErrNoRows }
		q.getActiveEmailOTP = func(int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error) {
			return usersdb.GetActiveEmailOTPByIdentifierRow{}, pgx.ErrNoRows
		}
		q.createCredential = func(a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
			return credRow(3, a.PrincipalID, a.Kind), nil
		}
		var otp usersdb.CreateEmailOTPCredentialParams
		q.createEmailOTP = func(a usersdb.CreateEmailOTPCredentialParams) (usersdb.CredEmailOtp, error) {
			otp = a
			return usersdb.CredEmailOtp{}, nil
		}
		rec := &recordingObserver{}
		s := newTestUserService(q, nil, nil, nil, rec)
		if _, err := s.AddIdentifierInTx(actorCtx(42), nil, AddIdentifierInput{UserID: 5, Kind: "email", Value: "a@b.example", Verified: true}); err != nil {
			t.Fatal(err)
		}
		if otp.IdentifierID != 11 || otp.CredentialID != 3 {
			t.Errorf("email_otp = %+v", otp)
		}
		if len(rec.seen) != 2 || rec.seen[0].Resource != "user_identifier" || rec.seen[1].Resource != "credential" || rec.seen[1].Op != "create" {
			t.Errorf("observations = %+v", rec.seen)
		}
	})
}

func TestVerifyIdentifierInTx(t *testing.T) {
	t.Parallel()

	setup := func(verified bool, hasOTP bool) (*UserService, *fakeUsersQ, *recordingObserver, *int) {
		created := 0
		q := &fakeUsersQ{
			getUser: func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil },
			getIdentifierByID: func(id int64) (usersdb.UserIdentifier, error) {
				return identifierRow(id, 5, "a@b.example", true, verified), nil
			},
			markVerified: func(id int64) (usersdb.UserIdentifier, error) {
				return identifierRow(id, 5, "a@b.example", true, true), nil
			},
			getActiveEmailOTP: func(int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error) {
				if hasOTP {
					return usersdb.GetActiveEmailOTPByIdentifierRow{}, nil
				}
				return usersdb.GetActiveEmailOTPByIdentifierRow{}, pgx.ErrNoRows
			},
			createCredential: func(a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
				return credRow(3, a.PrincipalID, a.Kind), nil
			},
			createEmailOTP: func(usersdb.CreateEmailOTPCredentialParams) (usersdb.CredEmailOtp, error) {
				created++
				return usersdb.CredEmailOtp{}, nil
			},
		}
		rec := &recordingObserver{}
		return newTestUserService(q, nil, nil, nil, rec), q, rec, &created
	}

	t.Run("verifying creates the email_otp credential", func(t *testing.T) {
		t.Parallel()
		s, _, rec, created := setup(false, false)
		id, err := s.VerifyIdentifierInTx(actorCtx(42), nil, 5, 11)
		if err != nil || id.VerifiedAt == nil {
			t.Fatalf("id=%+v err=%v", id, err)
		}
		if *created != 1 {
			t.Errorf("email_otp credentials created = %d, want 1", *created)
		}
		if len(rec.seen) != 2 || rec.seen[0].Op != "update" || rec.seen[0].Resource != "user_identifier" || rec.seen[1].Op != "create" || rec.seen[1].Resource != "credential" {
			t.Errorf("observations = %+v", rec.seen)
		}
	})

	t.Run("already verified with an active email_otp is a silent no-op", func(t *testing.T) {
		t.Parallel()
		s, _, rec, created := setup(true, true)
		if _, err := s.VerifyIdentifierInTx(actorCtx(42), nil, 5, 11); err != nil {
			t.Fatal(err)
		}
		if *created != 0 || len(rec.seen) != 0 {
			t.Errorf("created=%d observations=%+v", *created, rec.seen)
		}
	})

	t.Run("another user's identifier is masked as not found", func(t *testing.T) {
		t.Parallel()
		s, _, _, _ := setup(false, false)
		if _, err := s.VerifyIdentifierInTx(actorCtx(42), nil, 6, 11); !errors.Is(err, apiresp.ErrNotFound) {
			t.Fatalf("got %v, want not found", err)
		}
	})
}

func TestSetPrimaryIdentifierInTx(t *testing.T) {
	t.Parallel()
	q := &fakeUsersQ{
		getIdentifierByID: func(id int64) (usersdb.UserIdentifier, error) {
			return identifierRow(id, 5, "a@b.example", id == 1, true), nil
		},
		setIdentifierPrim: func(a usersdb.SetIdentifierPrimaryParams) (usersdb.UserIdentifier, error) {
			return identifierRow(a.ID, a.UserID, "a@b.example", true, true), nil
		},
	}
	rec := &recordingObserver{}
	s := newTestUserService(q, nil, nil, nil, rec)

	if _, err := s.SetPrimaryIdentifierInTx(actorCtx(42), nil, 5, 1); err != nil || q.clearPrimary != 0 || len(rec.seen) != 0 {
		t.Fatalf("already primary must be a no-op: err=%v cleared=%d obs=%+v", err, q.clearPrimary, rec.seen)
	}
	id, err := s.SetPrimaryIdentifierInTx(actorCtx(42), nil, 5, 2)
	if err != nil || !id.IsPrimary || q.clearPrimary != 1 {
		t.Fatalf("id=%+v err=%v cleared=%d", id, err, q.clearPrimary)
	}
	want := observation{Op: "update", Resource: "user_identifier", Target: 5, Actor: 42}
	if len(rec.seen) != 1 || rec.seen[0] != want {
		t.Errorf("observations = %+v", rec.seen)
	}
}

// savepointTx is a pgx.Tx whose Begin returns itself and records how the
// savepoint ended.
type savepointTx struct {
	pgx.Tx
	begun, committed, rolledBack int
}

func (t *savepointTx) Begin(context.Context) (pgx.Tx, error) {
	t.begun++
	return t, nil
}
func (t *savepointTx) Commit(context.Context) error   { t.committed++; return nil }
func (t *savepointTx) Rollback(context.Context) error { t.rolledBack++; return nil }

func TestRemoveIdentifierInTx(t *testing.T) {
	t.Parallel()

	build := func(deleteErr error, deleted int64) (*UserService, *fakeUsersQ, *recordingObserver) {
		q := &fakeUsersQ{
			getUser: func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil },
			getIdentifierByID: func(id int64) (usersdb.UserIdentifier, error) {
				return identifierRow(id, 5, "a@b.example", false, true), nil
			},
			getActiveEmailOTP: func(int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error) {
				return usersdb.GetActiveEmailOTPByIdentifierRow{}, pgx.ErrNoRows
			},
		}
		rec := &recordingObserver{}
		s := newTestUserService(q, nil, nil, nil, rec)
		s.newQ = func(pgx.Tx) usersdb.Querier {
			return &deleteStore{fakeUsersQ: q, err: deleteErr, rows: deleted}
		}
		return s, q, rec
	}

	t.Run("deletes and observes delete", func(t *testing.T) {
		t.Parallel()
		s, _, rec := build(nil, 1)
		tx := &savepointTx{}
		if err := s.RemoveIdentifierInTx(actorCtx(42), tx, 5, 11); err != nil {
			t.Fatal(err)
		}
		if tx.committed != 1 || tx.rolledBack != 0 {
			t.Errorf("savepoint committed=%d rolledBack=%d", tx.committed, tx.rolledBack)
		}
		want := observation{Op: "delete", Resource: "user_identifier", Target: 5, Actor: 42}
		if len(rec.seen) != 1 || rec.seen[0] != want {
			t.Errorf("observations = %+v", rec.seen)
		}
	})

	t.Run("locks the identifier row before listing bound passwords", func(t *testing.T) {
		t.Parallel()
		s, _, _ := build(nil, 1)
		var order []string
		s.newQ = func(pgx.Tx) usersdb.Querier {
			return &deleteStore{
				fakeUsersQ: &fakeUsersQ{
					getUser: func(id int64) (usersdb.User, error) { return usersdb.User{EntityID: id}, nil },
					getIdentifierByID: func(id int64) (usersdb.UserIdentifier, error) {
						return identifierRow(id, 5, "a@b.example", false, true), nil
					},
					getActiveEmailOTP: func(int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error) {
						return usersdb.GetActiveEmailOTPByIdentifierRow{}, pgx.ErrNoRows
					},
				},
				rows:  1,
				order: &order,
			}
		}
		if err := s.RemoveIdentifierInTx(actorCtx(42), &savepointTx{}, 5, 11); err != nil {
			t.Fatal(err)
		}
		if len(order) < 2 || order[0] != "lock" || order[1] != "list" {
			t.Errorf("call order = %v, want lock before list", order)
		}
	})

	t.Run("a credential still referencing the identifier is ErrIdentifierInUse and rolls back", func(t *testing.T) {
		t.Parallel()
		s, _, rec := build(&pgconn.PgError{Code: "23503"}, 0)
		tx := &savepointTx{}
		if err := s.RemoveIdentifierInTx(actorCtx(42), tx, 5, 11); !errors.Is(err, ErrIdentifierInUse) {
			t.Fatalf("got %v, want ErrIdentifierInUse", err)
		}
		if tx.rolledBack != 1 || tx.committed != 0 {
			t.Errorf("savepoint committed=%d rolledBack=%d, want a rollback", tx.committed, tx.rolledBack)
		}
		if len(rec.seen) != 0 {
			t.Errorf("observations on failure: %+v", rec.seen)
		}
	})
}

type deleteStore struct {
	*fakeUsersQ
	err  error
	rows int64
	// order, when set, records the lock and list calls in call order.
	order *[]string
}

func (d *deleteStore) LockIdentifierForUpdate(_ context.Context, a usersdb.LockIdentifierForUpdateParams) (usersdb.UserIdentifier, error) {
	if d.order != nil {
		*d.order = append(*d.order, "lock")
	}
	return identifierRow(a.ID, a.UserID, "a@b.example", false, true), nil
}

func (d *deleteStore) DeleteIdentifier(context.Context, usersdb.DeleteIdentifierParams) (int64, error) {
	return d.rows, d.err
}

func (d *deleteStore) ListActivePasswordsByPrincipal(context.Context, int64) ([]usersdb.ListActivePasswordsByPrincipalRow, error) {
	if d.order != nil {
		*d.order = append(*d.order, "list")
	}
	return nil, nil
}
