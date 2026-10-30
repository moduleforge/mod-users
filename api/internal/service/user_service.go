package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// userTypeSlug and profileTypeSlug are the mod-core type slugs the creation
// primitives instantiate.
const (
	userTypeSlug    = "user"
	profileTypeSlug = "profile"
)

// UserService owns user creation and identifier management.
//
// The creation primitives (CreateBYOUserInTx, CreateGuestUserInTx,
// CreateManagedUserInTx) are shared by registration, guest sign-in, OIDC JIT
// and guest upgrade. Each runs in the caller's transaction and does not
// commit; each satisfies the task-001 database invariants within that
// transaction (the deferred personal-profile check passes at commit).
//
// Identifier methods (Add, Verify, SetPrimary, Remove) manage `email`
// identifiers. Like the creation primitives they take the caller's
// transaction and do not authorize: self-service handlers authorize on
// "session principal = target", and sign-up and JIT flows are trusted.
type UserService struct {
	q        usersdb.Querier
	coreQ    coredb.Querier
	az       coreAuthz.Authorizer
	obs      *observer.ObserverGroup
	np       coreservice.NaturalPersonServicer
	creds    *CredentialService
	newQ     func(pgx.Tx) usersdb.Querier
	newCoreQ func(pgx.Tx) coredb.Querier
	now      func() time.Time

	// Set by WithSelfService; the creation and identifier primitives do not
	// need them.
	pool       *pgxpool.Pool
	db         usersdb.DBTX
	sessions   *SessionService
	challenges *ChallengeService
	ops        coreAuthz.OpResolver
	maxAuthAge time.Duration
}

// NewUserService constructs a UserService. np creates the user's own
// natural_person; creds creates the guest `anon` and the email_otp
// credentials. az guards only CreateManagedUserInTx.
func NewUserService(
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	np coreservice.NaturalPersonServicer,
	creds *CredentialService,
) *UserService {
	return &UserService{q: q, coreQ: coreQ, az: az, obs: obs, np: np, creds: creds, now: time.Now}
}

func (s *UserService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

func (s *UserService) coreQuerier(tx pgx.Tx) coredb.Querier {
	if s.newCoreQ != nil {
		return s.newCoreQ(tx)
	}
	return coredb.New(tx)
}

// CreatedUser is the result of a user creation primitive. Ids are internal;
// they must not reach an HTTP response.
type CreatedUser struct {
	User     usersdb.User
	UserUUID uuid.UUID
	// PersonalProfileID and PersonalProfileUUID are zero for a managed user.
	PersonalProfileID   int64
	PersonalProfileUUID uuid.UUID
	// PersonID and PersonUUID are zero for guests and managed users.
	PersonID   int64
	PersonUUID uuid.UUID
}

// ActingContext returns ctx carrying the AuthContext the flow should use for
// further observations of this user: the user as principal, acting as its
// personal profile (API surface section 10.1, "Acting profile for
// unauthenticated flows"). It returns ctx unchanged for a managed user, which
// has no personal profile.
func (u CreatedUser) ActingContext(ctx context.Context) context.Context {
	if u.PersonalProfileID == 0 {
		return ctx
	}
	// WHY no system-actor marker: the new user's personal profile is a real
	// user's profile, never trusted platform code (withActingProfile).
	// IMPLIES: sign-up, guest creation, register and the bootstrap use this
	// context only for observations and their unauthorized creation writes; it
	// would be refused by the engine's gates if it reached an Authorize entry
	// point.
	return withActingProfile(ctx, u.User.EntityID, u.PersonalProfileID)
}

func userAudit(u usersdb.User, id uuid.UUID) map[string]any {
	m := map[string]any{"uuid": id.String(), "is_guest": u.IsGuest}
	if u.ManagingPartyID.Valid {
		m["managed"] = true
	}
	return m
}

// createEntity inserts an entity of typeID stamped created_by the acting
// profile on ctx (NULL when ctx has none: a sign-up before any actor exists).
// A NULL owner on a `user` type is defaulted to the user itself by the
// database (entities_owner_self_default).
func (s *UserService) createEntity(ctx context.Context, q coredb.Querier, typeID int64, owner pgtype.Int8) (coredb.Entity, error) {
	ent, err := q.CreateEntity(ctx, coredb.CreateEntityParams{
		FundamentalTypeID:  typeID,
		OwnerID:            owner,
		CreatedByProfileID: actingProfileParam(ctx),
	})
	if err != nil {
		return coredb.Entity{}, fmt.Errorf("users: create entity: %w", err)
	}
	return ent, nil
}

// ---------------------------------------------------------------------------
// bring-your-own user
// ---------------------------------------------------------------------------

// CreateBYOUserInput is the input of CreateBYOUserInTx.
type CreateBYOUserInput struct {
	GivenName  string
	FamilyName string
	// DefaultAppID, when set, is users.default_app_id.
	DefaultAppID *int64
}

// CreateBYOUserInTx creates a bring-your-own user: the principal (a `user`
// entity owned by itself, decision U1) with its `mod_users.users` row, its
// personal profile (active), and its own `natural_person` record (owned by the
// user, decisions U1 and U11) linked as `users.person_id`.
//
// It does no authorization: sign-up is a public flow. created_by_profile_id of
// the user and its profile is the acting profile on ctx when there is one
// (callers may install the anonymous system actor's AuthContext), and NULL
// otherwise. The person record and every observation run as the new user's
// personal profile (API surface section 10.1): `create` `user`, and the
// person's own observation from mod-core. Use CreatedUser.ActingContext for
// the caller's further observations.
func (s *UserService) CreateBYOUserInTx(ctx context.Context, tx pgx.Tx, in CreateBYOUserInput) (CreatedUser, error) {
	given, family := strings.TrimSpace(in.GivenName), strings.TrimSpace(in.FamilyName)
	if given == "" {
		return CreatedUser{}, invalidField("given_name", "users.given_name_required", "given_name is required")
	}
	if family == "" {
		return CreatedUser{}, invalidField("family_name", "users.family_name_required", "family_name is required")
	}

	created, err := s.createUserAndProfile(ctx, tx, false, in.DefaultAppID)
	if err != nil {
		return CreatedUser{}, err
	}
	actCtx := created.ActingContext(ctx)

	profileID := created.PersonalProfileID
	userID := created.User.EntityID
	_, personUUID, personID, err := s.np.CreateInTx(actCtx, tx, coreservice.CreateNaturalPersonInput{
		GivenName:          given,
		FamilyName:         family,
		OwnerID:            &userID,
		CreatedByProfileID: &profileID,
	})
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: create person: %w", err)
	}
	linked, err := s.usersQ(tx).SetUserPerson(ctx, usersdb.SetUserPersonParams{
		EntityID: userID,
		PersonID: pgtype.Int8{Int64: personID, Valid: true},
	})
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: link person: %w", err)
	}
	created.User = linked
	created.PersonID, created.PersonUUID = personID, personUUID

	if err := observeInTx(actCtx, s.obs, tx, opCreate, resourceUser, userID, nil, userAudit(linked, created.UserUUID)); err != nil {
		return CreatedUser{}, err
	}
	return created, nil
}

// createUserAndProfile inserts the principal entity, principal, users row and
// the personal profile of a bring-your-own (or guest) user. It writes no
// observation.
func (s *UserService) createUserAndProfile(ctx context.Context, tx pgx.Tx, isGuest bool, defaultAppID *int64) (CreatedUser, error) {
	cq := s.coreQuerier(tx)
	userType, err := cq.GetTypeBySlug(ctx, userTypeSlug)
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: resolve user type: %w", err)
	}
	profileType, err := cq.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: resolve profile type: %w", err)
	}

	userEnt, err := s.createEntity(ctx, cq, userType.ID, pgtype.Int8{})
	if err != nil {
		return CreatedUser{}, err
	}
	if _, err := cq.CreatePrincipal(ctx, userEnt.ID); err != nil {
		return CreatedUser{}, fmt.Errorf("users: create principal: %w", err)
	}
	user, err := s.usersQ(tx).CreateUser(ctx, usersdb.CreateUserParams{
		EntityID:     userEnt.ID,
		DefaultAppID: nullableInt8(defaultAppID),
		IsGuest:      isGuest,
	})
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: create user: %w", err)
	}

	// The personal profile is owned by its holder (profile_ownership_subject)
	// and needs the users row first (invariant 5).
	profileEnt, err := s.createEntity(ctx, cq, profileType.ID, pgtype.Int8{Int64: userEnt.ID, Valid: true})
	if err != nil {
		return CreatedUser{}, err
	}
	startedAt := profileEnt.CreatedAt.Time
	if _, err := cq.CreateProfile(ctx, coredb.CreateProfileParams{
		EntityID:  profileEnt.ID,
		HolderID:  userEnt.ID,
		Status:    "active",
		Source:    "manual",
		StartedAt: &startedAt,
	}); err != nil {
		return CreatedUser{}, fmt.Errorf("users: create personal profile: %w", err)
	}

	return CreatedUser{
		User:                user,
		UserUUID:            userEnt.Uuid,
		PersonalProfileID:   profileEnt.ID,
		PersonalProfileUUID: profileEnt.Uuid,
	}, nil
}

// ---------------------------------------------------------------------------
// guest user
// ---------------------------------------------------------------------------

// CreateGuestUserInput is the input of CreateGuestUserInTx.
type CreateGuestUserInput struct {
	// DeviceID is a stable device fingerprint supplied by the client.
	DeviceID string
	// TokenTTL is the anon credential lifetime; zero means DefaultAnonTTL.
	TokenTTL     time.Duration
	DefaultAppID *int64
}

// CreatedGuest is the result of CreateGuestUserInTx.
type CreatedGuest struct {
	CreatedUser
	// Anon is the guest's `anon` credential. Anon.Token is the raw device
	// token, returned only now.
	Anon CreatedAnon
}

// CreateGuestUserInTx creates a guest user: a bring-your-own principal with
// `is_guest` set, no person record, its personal profile, and one `anon`
// credential. It does no authorization (guest sign-in is public); the
// observations (`create` `user`, `create` `credential`) run as the guest's own
// personal profile (API surface section 10.1).
func (s *UserService) CreateGuestUserInTx(ctx context.Context, tx pgx.Tx, in CreateGuestUserInput) (CreatedGuest, error) {
	if strings.TrimSpace(in.DeviceID) == "" {
		return CreatedGuest{}, invalidField("device_id", "users.device_id_required", "device_id is required")
	}
	created, err := s.createUserAndProfile(ctx, tx, true, in.DefaultAppID)
	if err != nil {
		return CreatedGuest{}, err
	}
	actCtx := created.ActingContext(ctx)

	if err := observeInTx(actCtx, s.obs, tx, opCreate, resourceUser, created.User.EntityID, nil, userAudit(created.User, created.UserUUID)); err != nil {
		return CreatedGuest{}, err
	}
	anon, err := s.creds.CreateAnonInTx(actCtx, tx, CreateAnonInput{
		PrincipalID: created.User.EntityID,
		DeviceID:    in.DeviceID,
		TTL:         in.TokenTTL,
	})
	if err != nil {
		return CreatedGuest{}, err
	}
	return CreatedGuest{CreatedUser: created, Anon: anon}, nil
}

// ---------------------------------------------------------------------------
// managed user
// ---------------------------------------------------------------------------

// CreateManagedUserInput is the input of CreateManagedUserInTx.
type CreateManagedUserInput struct {
	// ManagingPartyID is the legal entity that owns and manages the user.
	ManagingPartyID int64
	DefaultAppID    *int64
}

// CreateManagedUserInTx creates a managed user for a party: a `user` entity
// owned by the party (immutable), its principal and `users` row with
// managing_party_id set, no person and no personal profile. The party profile
// that lets the user act is created by the caller.
//
// It authorizes `create` on the `user` type within the party
// (authz.AuthorizeTypeIn) before any write, as the acting profile on ctx; an
// absent authorizer fails closed. The `create` `user` observation runs as that
// acting profile.
func (s *UserService) CreateManagedUserInTx(ctx context.Context, tx pgx.Tx, in CreateManagedUserInput) (CreatedUser, error) {
	if in.ManagingPartyID <= 0 {
		return CreatedUser{}, invalidField("party", "users.party_required", "a managing party is required")
	}
	cq := s.coreQuerier(tx)
	userType, err := cq.GetTypeBySlug(ctx, userTypeSlug)
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: resolve user type: %w", err)
	}
	party := in.ManagingPartyID
	if s.az == nil {
		return CreatedUser{}, apiresp.ErrForbidden
	}
	if err := coreAuthz.AuthorizeTypeIn(ctx, s.az, "create", userType.ID, &party); err != nil {
		return CreatedUser{}, err
	}

	userEnt, err := s.createEntity(ctx, cq, userType.ID, pgtype.Int8{Int64: party, Valid: true})
	if err != nil {
		return CreatedUser{}, err
	}
	if _, err := cq.CreatePrincipal(ctx, userEnt.ID); err != nil {
		return CreatedUser{}, fmt.Errorf("users: create principal: %w", err)
	}
	user, err := s.usersQ(tx).CreateUser(ctx, usersdb.CreateUserParams{
		EntityID:        userEnt.ID,
		DefaultAppID:    nullableInt8(in.DefaultAppID),
		ManagingPartyID: pgtype.Int8{Int64: party, Valid: true},
	})
	if err != nil {
		return CreatedUser{}, fmt.Errorf("users: create managed user: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceUser, userEnt.ID, nil, userAudit(user, userEnt.Uuid)); err != nil {
		return CreatedUser{}, err
	}
	return CreatedUser{User: user, UserUUID: userEnt.Uuid}, nil
}

// ---------------------------------------------------------------------------
// identifiers
// ---------------------------------------------------------------------------

// IdentifierKindEmail is the only identifier kind the services manage.
const IdentifierKindEmail = "email"

// Identifier is the service-layer view of a mod_users.user_identifiers row.
type Identifier struct {
	ID         int64
	UUID       uuid.UUID
	UserID     int64
	Kind       string
	Value      string // canonical
	IsPrimary  bool
	VerifiedAt *time.Time
	CreatedAt  time.Time
}

func toIdentifier(row usersdb.UserIdentifier) Identifier {
	return Identifier{
		ID: row.ID, UUID: row.Uuid, UserID: row.UserID, Kind: row.Kind, Value: row.ValueCanon,
		IsPrimary: row.IsPrimary, VerifiedAt: row.VerifiedAt, CreatedAt: row.CreatedAt.Time,
	}
}

func identifierAudit(row usersdb.UserIdentifier) map[string]any {
	return map[string]any{
		"uuid":       row.Uuid.String(),
		"kind":       row.Kind,
		"value":      row.ValueCanon,
		"is_primary": row.IsPrimary,
		"verified":   row.VerifiedAt != nil,
	}
}

// AddIdentifierInput is the input of AddIdentifierInTx.
type AddIdentifierInput struct {
	UserID int64
	Kind   string // "email"
	Value  string
	// Primary makes the identifier the primary email. The user's first email
	// is primary regardless.
	Primary bool
	// Verified records the identifier as already verified (a trusted flow: an
	// IdP-asserted email, bootstrap) and creates its email_otp credential.
	Verified bool
}

// loadNonGuest loads the user and refuses identifier and email_otp operations for a guest user
// (ErrGuestMustUpgrade); a missing user is apiresp.ErrNotFound.
func (s *UserService) loadNonGuest(ctx context.Context, q usersdb.Querier, userID int64) (usersdb.User, error) {
	user, err := q.GetUserByEntityID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return usersdb.User{}, apiresp.ErrNotFound
		}
		return usersdb.User{}, fmt.Errorf("users: load user: %w", err)
	}
	if user.IsGuest {
		return usersdb.User{}, ErrGuestMustUpgrade
	}
	return user, nil
}

// AddIdentifierInTx adds an email identifier to a (non-guest) user. The value
// is canonicalized (trimmed, lowercased) and validated; a (kind, value) that
// already exists for anyone fails with ErrIdentifierTaken (the
// user_identifiers_kind_value_uq index decides, so races lose cleanly). The
// first email of a user becomes primary. Observation: `create`
// `user_identifier`, target the user.
func (s *UserService) AddIdentifierInTx(ctx context.Context, tx pgx.Tx, in AddIdentifierInput) (Identifier, error) {
	if in.Kind != IdentifierKindEmail {
		return Identifier{}, invalidField("kind", "users.identifier_kind_unsupported", "only email identifiers are supported")
	}
	canon := CanonicalizeEmail(in.Value)
	if err := validateEmail(canon); err != nil {
		return Identifier{}, err
	}

	q := s.usersQ(tx)
	if _, err := s.loadNonGuest(ctx, q, in.UserID); err != nil {
		return Identifier{}, err
	}

	primary := in.Primary
	if !primary {
		if _, err := q.GetPrimaryEmailByUser(ctx, in.UserID); errors.Is(err, pgx.ErrNoRows) {
			primary = true
		} else if err != nil {
			return Identifier{}, fmt.Errorf("users: look up primary email: %w", err)
		}
	}
	if primary {
		if err := q.ClearPrimaryEmail(ctx, in.UserID); err != nil {
			return Identifier{}, fmt.Errorf("users: clear primary email: %w", err)
		}
	}

	var verifiedAt *time.Time
	if in.Verified {
		now := s.now().UTC()
		verifiedAt = &now
	}
	row, err := q.CreateIdentifier(ctx, usersdb.CreateIdentifierParams{
		UserID:     in.UserID,
		Kind:       IdentifierKindEmail,
		ValueCanon: canon,
		IsPrimary:  primary,
		VerifiedAt: verifiedAt,
	})
	if err != nil {
		if mapped := mapUniqueViolation(err); errors.Is(mapped, ErrIdentifierTaken) {
			return Identifier{}, ErrIdentifierTaken
		}
		return Identifier{}, fmt.Errorf("users: create identifier: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceUserIdentifier, in.UserID, nil, identifierAudit(row)); err != nil {
		return Identifier{}, err
	}
	if in.Verified {
		if _, err := s.creds.EnsureEmailOTPInTx(ctx, tx, in.UserID, row.ID); err != nil {
			return Identifier{}, err
		}
	}
	return toIdentifier(row), nil
}

func (s *UserService) loadIdentifier(ctx context.Context, q usersdb.Querier, userID, identifierID int64) (usersdb.UserIdentifier, error) {
	row, err := q.GetIdentifierByID(ctx, identifierID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return usersdb.UserIdentifier{}, apiresp.ErrNotFound
		}
		return usersdb.UserIdentifier{}, fmt.Errorf("users: load identifier: %w", err)
	}
	if row.UserID != userID {
		return usersdb.UserIdentifier{}, apiresp.ErrNotFound
	}
	return row, nil
}

// VerifyIdentifierInTx marks the identifier verified and, for an email, creates
// its email_otp credential unless one is active (decision U12). The caller has
// already proven control of the identifier (a consumed verify_identifier
// challenge, or a trusted flow). Idempotent: an already verified identifier
// keeps its first verification time and writes no observation. Observation:
// `update` `user_identifier`, target the user.
func (s *UserService) VerifyIdentifierInTx(ctx context.Context, tx pgx.Tx, userID, identifierID int64) (Identifier, error) {
	q := s.usersQ(tx)
	if _, err := s.loadNonGuest(ctx, q, userID); err != nil {
		return Identifier{}, err
	}
	before, err := s.loadIdentifier(ctx, q, userID, identifierID)
	if err != nil {
		return Identifier{}, err
	}
	after := before
	if before.VerifiedAt == nil {
		after, err = q.MarkIdentifierVerified(ctx, identifierID)
		if err != nil {
			return Identifier{}, fmt.Errorf("users: mark identifier verified: %w", err)
		}
		if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceUserIdentifier, userID, identifierAudit(before), identifierAudit(after)); err != nil {
			return Identifier{}, err
		}
	}
	if after.Kind == IdentifierKindEmail {
		if _, err := s.creds.EnsureEmailOTPInTx(ctx, tx, userID, identifierID); err != nil {
			return Identifier{}, err
		}
	}
	return toIdentifier(after), nil
}

// SetPrimaryIdentifierInTx makes the email identifier the user's primary
// email, demoting the current one. Making the primary identifier primary again
// is a no-op without an observation. Observation: `update` `user_identifier`.
func (s *UserService) SetPrimaryIdentifierInTx(ctx context.Context, tx pgx.Tx, userID, identifierID int64) (Identifier, error) {
	q := s.usersQ(tx)
	before, err := s.loadIdentifier(ctx, q, userID, identifierID)
	if err != nil {
		return Identifier{}, err
	}
	if before.Kind != IdentifierKindEmail {
		return Identifier{}, invalidField("identifier", "users.identifier_kind_unsupported", "only email identifiers can be primary")
	}
	if before.IsPrimary {
		return toIdentifier(before), nil
	}
	if err := q.ClearPrimaryEmail(ctx, userID); err != nil {
		return Identifier{}, fmt.Errorf("users: clear primary email: %w", err)
	}
	after, err := q.SetIdentifierPrimary(ctx, usersdb.SetIdentifierPrimaryParams{ID: identifierID, UserID: userID})
	if err != nil {
		return Identifier{}, fmt.Errorf("users: set primary identifier: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceUserIdentifier, userID, identifierAudit(before), identifierAudit(after)); err != nil {
		return Identifier{}, err
	}
	return toIdentifier(after), nil
}

// RemoveIdentifierInTx removes an identifier. Credentials bound to it (a
// password, an email_otp) are revoked first with reason `user`, honoring the
// last-interactive-credential rule (ErrLastCredential); the revocation is what
// lets the row be deleted, because an active email_otp must keep its identifier
// (cred_email_otp_active_needs_identifier). The identifier is then hard-deleted
// and the foreign keys from cred_password and cred_email_otp set the revoked
// credentials' identifier_id to NULL; the credential rows stay as history. A
// foreign key from elsewhere (another module referencing the identifier) that
// still restricts the delete fails with ErrIdentifierInUse, and the whole
// removal (including the revocations) is rolled back to a savepoint, so the
// caller's transaction stays usable. Observation: `delete` `user_identifier`,
// target the user.
func (s *UserService) RemoveIdentifierInTx(ctx context.Context, tx pgx.Tx, userID, identifierID int64) error {
	q := s.usersQ(tx)
	row, err := s.loadIdentifier(ctx, q, userID, identifierID)
	if err != nil {
		return err
	}

	sp, err := tx.Begin(ctx) // savepoint
	if err != nil {
		return fmt.Errorf("users: begin savepoint: %w", err)
	}
	if err := s.removeIdentifier(ctx, sp, row); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return fmt.Errorf("users: release savepoint: %w", err)
	}
	return nil
}

func (s *UserService) removeIdentifier(ctx context.Context, tx pgx.Tx, row usersdb.UserIdentifier) error {
	q := s.usersQ(tx)
	userID := row.UserID

	// Lock the identifier row before listing bound credentials, so a concurrent
	// password create or reset that binds it serializes behind this removal.
	if _, err := q.LockIdentifierForUpdate(ctx, usersdb.LockIdentifierForUpdateParams{ID: row.ID, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrNotFound
		}
		return fmt.Errorf("users: lock identifier: %w", err)
	}

	// Revoke the credentials bound to the identifier.
	if otp, err := q.GetActiveEmailOTPByIdentifier(ctx, row.ID); err == nil {
		if err := s.creds.RemoveInTx(ctx, tx, userID, otp.Credential.ID, RevokeReasonUser); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("users: look up email_otp: %w", err)
	}
	passwords, err := q.ListActivePasswordsByPrincipal(ctx, userID)
	if err != nil {
		return fmt.Errorf("users: list passwords: %w", err)
	}
	for _, p := range passwords {
		if p.IdentifierID.Valid && p.IdentifierID.Int64 == row.ID {
			if err := s.creds.RemoveInTx(ctx, tx, userID, p.Credential.ID, RevokeReasonUser); err != nil {
				return err
			}
		}
	}

	n, err := q.DeleteIdentifier(ctx, usersdb.DeleteIdentifierParams{ID: row.ID, UserID: userID})
	if err != nil {
		if state, _, ok := pgConstraint(err); ok && state == foreignKeyViolationSQLState {
			return ErrIdentifierInUse
		}
		return fmt.Errorf("users: delete identifier: %w", err)
	}
	if n == 0 {
		return apiresp.ErrNotFound
	}
	return observeInTx(ctx, s.obs, tx, opDelete, resourceUserIdentifier, userID, identifierAudit(row), nil)
}

// ListIdentifiers returns the user's identifiers, primary first. It does not
// authorize.
func (s *UserService) ListIdentifiers(ctx context.Context, userID int64) ([]Identifier, error) {
	rows, err := s.q.ListIdentifiersByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("users: list identifiers: %w", err)
	}
	out := make([]Identifier, len(rows))
	for i, r := range rows {
		out[i] = toIdentifier(r)
	}
	return out, nil
}
