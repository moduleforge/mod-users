package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/config"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// This file holds the self-service methods behind /v1/me.
//
// Authority. Every Self* method authorizes on "session.principal = target", not
// on grants: the target is always the caller's own principal, read from the
// AuthContext on ctx, never from an argument. A caller cannot name another
// principal, so no grant is consulted and none can widen the scope.
//
// Writes additionally (1) are refused while impersonating (an administrator
// acting as a holder must not change the holder's own sign-in methods) and
// (2) require the session's auth_time to be within
// AUTH_SELF_SERVICE_MAX_AUTH_AGE, else the shared action-required error
// users.step_up_required (decision U8). The gate runs after the method's own
// input validation and before any state is read, so the error order stays
// 401, 400, 403, 404 or 409.

// maxNameRunes bounds a given or family name.
const maxNameRunes = 200

// Observer resource for a disabled or enabled service account (the user
// resource is resourceUser).
const resourceServiceAccount = "service_account"

// ErrNotGuest is returned by SelfUpgradeGuest when the caller is already a
// registered user (users.not_guest, 409).
var ErrNotGuest = apiresp.Conflict(apiresp.FieldError{
	Field: "user", Code: "users.not_guest", Message: "the account is already registered",
})

// ErrNoPersonRecord is returned when a name update targets a user that has no
// person record (a managed user: decision U11 gives them none).
var ErrNoPersonRecord = apiresp.Conflict(apiresp.FieldError{
	Field: "user", Code: "users.no_person_record", Message: "this account has no name record to update",
})

// WithSelfService wires the dependencies of the self-service and principal
// methods and returns s. NewUserService alone serves the creation and
// identifier primitives; the /v1/me and /v1/principals methods additionally
// need:
//
//   - pool: the connection pool; those methods open their own transactions.
//   - sessions: session listing, profile availability and revocation.
//   - challenges: the verify_identifier codes.
//   - ops: the operation resolver the principal list uses to compute the
//     op ids passed to the accessible_*_ids_for_actor functions.
//   - maxAuthAge: AUTH_SELF_SERVICE_MAX_AUTH_AGE; zero or negative means the
//     default (config.DefaultSelfServiceMaxAuthAge).
//
// It must be called before s is used concurrently.
func (s *UserService) WithSelfService(
	pool *pgxpool.Pool,
	sessions *SessionService,
	challenges *ChallengeService,
	ops coreAuthz.OpResolver,
	maxAuthAge time.Duration,
) *UserService {
	s.pool = pool
	if pool != nil {
		s.db = pool
	}
	s.sessions = sessions
	s.challenges = challenges
	s.ops = ops
	s.maxAuthAge = maxAuthAge
	return s
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (s *UserService) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if s.pool == nil {
		return errors.New("users: no database pool configured for self-service")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("users: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("users: commit: %w", err)
	}
	return nil
}

// selfReader returns the caller's AuthContext for a self-service read: an
// authenticated interactive session.
func selfReader(ctx context.Context) (opctx.AuthContext, error) {
	ac, ok := opctx.Auth(ctx)
	if !ok || ac.Anonymous || ac.PrincipalID <= 0 || ac.ProfileID <= 0 {
		return opctx.AuthContext{}, apiresp.ErrUnauthenticated
	}
	if ac.SessionID <= 0 {
		// Client-credentials callers have no session: the /v1/me surface is for
		// interactive users.
		return opctx.AuthContext{}, apiresp.ErrForbidden
	}
	return ac, nil
}

// selfWriter is selfReader plus the impersonation refusal.
func selfWriter(ctx context.Context) (opctx.AuthContext, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return opctx.AuthContext{}, err
	}
	if ac.ImpersonatorPrincipalID != 0 {
		return opctx.AuthContext{}, apiresp.ErrForbidden
	}
	return ac, nil
}

// RequireFreshAuth returns ErrStepUpRequired (409 users.step_up_required) when
// the caller's session auth_time is older than AUTH_SELF_SERVICE_MAX_AUTH_AGE.
// A missing or zero auth_time fails closed.
func (s *UserService) RequireFreshAuth(ctx context.Context) error {
	ac, ok := opctx.Auth(ctx)
	if !ok {
		return apiresp.ErrUnauthenticated
	}
	return s.requireFresh(ac)
}

func (s *UserService) requireFresh(ac opctx.AuthContext) error {
	maxAge := s.maxAuthAge
	if maxAge <= 0 {
		maxAge = config.DefaultSelfServiceMaxAuthAge
	}
	if ac.AuthTime.IsZero() || s.now().Sub(ac.AuthTime) > maxAge {
		return ErrStepUpRequired
	}
	return nil
}

// selfWrite performs the checks every self-service write starts with, after
// the method's own input validation: impersonation refusal and the freshness
// gate. It returns the caller's AuthContext.
func (s *UserService) selfWrite(ctx context.Context) (opctx.AuthContext, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return opctx.AuthContext{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return opctx.AuthContext{}, err
	}
	return ac, nil
}

// ---------------------------------------------------------------------------
// the user's own record
// ---------------------------------------------------------------------------

// UserView is the self-service view of a user (GET /v1/me). Internal ids are
// never serialized.
type UserView struct {
	UUID    uuid.UUID `json:"uuid"`
	Kind    string    `json:"kind"`
	IsGuest bool      `json:"is_guest"`
	// Managed is true for a managed user (owned by a party).
	Managed bool `json:"managed"`
	// GivenName and FamilyName are null when the user has no person record.
	GivenName   *string `json:"given_name"`
	FamilyName  *string `json:"family_name"`
	DisplayName string  `json:"display_name"`
	// Email is the primary email identifier, null when there is none.
	Email *string `json:"email"`
	// EmailVerified is true when the user holds at least one verified email.
	EmailVerified      bool       `json:"email_verified"`
	DefaultProfileUUID *uuid.UUID `json:"default_profile_uuid"`
	DefaultAppUUID     *uuid.UUID `json:"default_app_uuid"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// userDisplayNameSQL is the display name of a user (decision U19): the person's
// name, else the primary email, else "Guest" for a guest. It expects the
// aliases u (mod_users.users), np (the person's public.natural_persons row,
// LEFT JOINed) and pe (the primary email identifier, LEFT JOINed).
const userDisplayNameSQL = `COALESCE(NULLIF(btrim(concat_ws(' ', np.given_name, np.family_name)), ''), ` +
	`pe.value_canon, CASE WHEN u.is_guest THEN 'Guest' END, '')`

const userViewSQL = `
SELECT e.uuid,
       u.is_guest,
       u.managing_party_id IS NOT NULL,
       np.given_name,
       np.family_name,
       ` + userDisplayNameSQL + `,
       pe.value_canon,
       EXISTS (SELECT 1 FROM mod_users.user_identifiers v
                WHERE v.user_id = u.entity_id AND v.kind = 'email' AND v.verified_at IS NOT NULL),
       dpe.uuid,
       dae.uuid,
       e.created_at,
       u.updated_at
  FROM mod_users.users u
  JOIN public.entities e ON e.id = u.entity_id
  LEFT JOIN public.natural_persons np ON np.entity_id = u.person_id
  LEFT JOIN mod_users.user_identifiers pe ON pe.user_id = u.entity_id AND pe.kind = 'email' AND pe.is_primary
  LEFT JOIN public.entities dpe ON dpe.id = u.default_profile_id
  LEFT JOIN public.entities dae ON dae.id = u.default_app_id
 WHERE u.entity_id = $1`

func optionalUUID(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := uuid.UUID(v.Bytes)
	return &id
}

func optionalText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// loadUserView reads the user's view on db; a missing user is
// apiresp.ErrNotFound.
func loadUserView(ctx context.Context, db usersdb.DBTX, userID int64) (UserView, error) {
	var (
		v                          UserView
		given, family, email       pgtype.Text
		defaultProfile, defaultApp pgtype.UUID
		createdAt, updatedAt       pgtype.Timestamptz
	)
	err := db.QueryRow(ctx, userViewSQL, userID).Scan(
		&v.UUID, &v.IsGuest, &v.Managed, &given, &family, &v.DisplayName, &email, &v.EmailVerified,
		&defaultProfile, &defaultApp, &createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserView{}, apiresp.ErrNotFound
	}
	if err != nil {
		return UserView{}, fmt.Errorf("users: load user view: %w", err)
	}
	v.Kind = "user"
	v.GivenName, v.FamilyName, v.Email = optionalText(given), optionalText(family), optionalText(email)
	v.DefaultProfileUUID, v.DefaultAppUUID = optionalUUID(defaultProfile), optionalUUID(defaultApp)
	v.CreatedAt, v.UpdatedAt = createdAt.Time, updatedAt.Time
	return v, nil
}

// SelfGet returns the caller's own user record (GET /v1/me). A caller that is
// not a user (a service account) is apiresp.ErrForbidden.
func (s *UserService) SelfGet(ctx context.Context) (UserView, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return UserView{}, err
	}
	v, err := loadUserView(ctx, s.db, ac.PrincipalID)
	if errors.Is(err, apiresp.ErrNotFound) {
		return UserView{}, apiresp.ErrForbidden
	}
	return v, err
}

// OptionalUUID is a nullable, optionally supplied uuid field of an update:
// Set is false when the client omitted the field, and Set with a nil Value
// (uuid.Nil) clears it.
type OptionalUUID struct {
	Set   bool
	Value uuid.UUID
}

// UpdateSelfInput is the input of SelfUpdate. Nil and unset fields are left
// unchanged.
type UpdateSelfInput struct {
	GivenName  *string
	FamilyName *string
	// DefaultProfile is users.default_profile_id; the profile must be held by
	// the caller.
	DefaultProfile OptionalUUID
	// DefaultApp is users.default_app_id.
	DefaultApp OptionalUUID
}

func validateName(field string, v *string) (string, error) {
	if v == nil {
		return "", nil
	}
	name := strings.TrimSpace(*v)
	if name == "" {
		return "", invalidField(field, "users."+field+"_required", field+" is required")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", invalidField(field, "users."+field+"_too_long", field+" is too long")
	}
	return name, nil
}

// SelfUpdate updates the caller's own user record (PUT /v1/me): the given and
// family name on the user's own person (decisions U1 and U11), and the default
// profile and default app. A guest or a user with no person record cannot
// change a name (ErrGuestMustUpgrade, ErrNoPersonRecord); a default profile
// the caller does not hold, or an unknown app, is apiresp.ErrNotFound.
// Observation: `update` `user`, target the user, written only when something
// changed.
func (s *UserService) SelfUpdate(ctx context.Context, in UpdateSelfInput) (UserView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return UserView{}, err
	}
	given, err := validateName("given_name", in.GivenName)
	if err != nil {
		return UserView{}, err
	}
	family, err := validateName("family_name", in.FamilyName)
	if err != nil {
		return UserView{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return UserView{}, err
	}

	userID := ac.PrincipalID
	var view UserView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		q, cq := s.usersQ(tx), s.coreQuerier(tx)
		user, err := q.GetUserByEntityIDForUpdate(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("users: lock user: %w", err)
		}
		before, after := map[string]any{}, map[string]any{}

		if in.GivenName != nil || in.FamilyName != nil {
			if err := s.updatePersonName(ctx, cq, user, in.GivenName != nil, given, in.FamilyName != nil, family, before, after); err != nil {
				return err
			}
		}

		defProfile, defApp := user.DefaultProfileID, user.DefaultAppID
		if in.DefaultProfile.Set {
			defProfile = pgtype.Int8{}
			if in.DefaultProfile.Value != uuid.Nil {
				id, err := heldProfileID(ctx, cq, userID, in.DefaultProfile.Value)
				if err != nil {
					return err
				}
				defProfile = pgtype.Int8{Int64: id, Valid: true}
			}
		}
		if in.DefaultApp.Set {
			defApp = pgtype.Int8{}
			if in.DefaultApp.Value != uuid.Nil {
				app, err := cq.GetAppByUUID(ctx, in.DefaultApp.Value)
				if errors.Is(err, pgx.ErrNoRows) {
					return apiresp.ErrNotFound
				}
				if err != nil {
					return fmt.Errorf("users: load app: %w", err)
				}
				defApp = pgtype.Int8{Int64: app.ID, Valid: true}
			}
		}
		if defProfile != user.DefaultProfileID || defApp != user.DefaultAppID {
			if _, err := q.UpdateUserDefaults(ctx, usersdb.UpdateUserDefaultsParams{
				EntityID: userID, DefaultProfileID: defProfile, DefaultAppID: defApp,
			}); err != nil {
				return fmt.Errorf("users: update defaults: %w", err)
			}
			if in.DefaultProfile.Set {
				before["default_profile_set"], after["default_profile_set"] = user.DefaultProfileID.Valid, defProfile.Valid
			}
			if in.DefaultApp.Set {
				before["default_app_set"], after["default_app_set"] = user.DefaultAppID.Valid, defApp.Valid
			}
		}

		if len(after) > 0 {
			if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceUser, userID, before, after); err != nil {
				return err
			}
		}
		view, err = loadUserView(ctx, tx, userID)
		return err
	})
	if err != nil {
		return UserView{}, err
	}
	return view, nil
}

// updatePersonName applies a name change to the user's own person record and
// records the old and new values in before and after. It refuses a guest, a
// user without a person, and a person the user does not own.
func (s *UserService) updatePersonName(
	ctx context.Context, cq coredb.Querier, user usersdb.User,
	setGiven bool, given string, setFamily bool, family string,
	before, after map[string]any,
) error {
	if user.IsGuest {
		return ErrGuestMustUpgrade
	}
	if !user.PersonID.Valid {
		return ErrNoPersonRecord
	}
	ent, err := cq.GetEntityByID(ctx, user.PersonID.Int64)
	if err != nil {
		return fmt.Errorf("users: load person entity: %w", err)
	}
	if !ent.OwnerID.Valid || ent.OwnerID.Int64 != user.EntityID {
		// users.person_id links a person the user does not own: the user's own
		// record is not theirs to edit through this route.
		return apiresp.ErrForbidden
	}
	np, err := cq.GetNaturalPersonByEntityID(ctx, user.PersonID.Int64)
	if err != nil {
		return fmt.Errorf("users: load person: %w", err)
	}
	newGiven, newFamily := np.GivenName, np.FamilyName
	if setGiven {
		newGiven = pgtype.Text{String: given, Valid: true}
	}
	if setFamily {
		newFamily = pgtype.Text{String: family, Valid: true}
	}
	if newGiven == np.GivenName && newFamily == np.FamilyName {
		return nil
	}
	// Ssn nil leaves the stored value unchanged.
	if err := cq.UpdateNaturalPerson(ctx, coredb.UpdateNaturalPersonParams{
		EntityID: user.PersonID.Int64, GivenName: newGiven, FamilyName: newFamily,
	}); err != nil {
		return fmt.Errorf("users: update person: %w", err)
	}
	before["given_name"], after["given_name"] = np.GivenName.String, newGiven.String
	before["family_name"], after["family_name"] = np.FamilyName.String, newFamily.String
	return nil
}

// heldProfileID resolves a profile uuid to the entity id of a non-ended
// profile held by userID. Anything else, including an unknown uuid, is
// apiresp.ErrNotFound (masking).
func heldProfileID(ctx context.Context, cq coredb.Querier, userID int64, profileUUID uuid.UUID) (int64, error) {
	p, err := cq.GetProfileByUUID(ctx, profileUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, apiresp.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("users: load profile: %w", err)
	}
	if p.HolderID != userID || p.Status == "ended" {
		return 0, apiresp.ErrNotFound
	}
	return p.EntityID, nil
}

// SelfProfiles lists the caller's profiles with their availability to the
// caller's session and the lock reason and remedy of each unavailable one
// (GET /v1/me/profiles).
func (s *UserService) SelfProfiles(ctx context.Context) ([]ProfileView, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return nil, err
	}
	if s.sessions == nil {
		return nil, errors.New("users: no session service configured for self-service")
	}
	return s.sessions.Profiles(ctx, ac.SessionID)
}

// ---------------------------------------------------------------------------
// guest upgrade
// ---------------------------------------------------------------------------

// UpgradeGuestInput is the input of SelfUpgradeGuest.
type UpgradeGuestInput struct {
	Email      string
	Password   string
	GivenName  string
	FamilyName string
}

// SelfUpgradeGuest turns the calling guest into a registered user
// (POST /v1/me/upgrade). In one transaction it creates the user's own person
// record (owned by the user, decisions U1 and U11), clears is_guest and links
// the person, adds the email identifier (primary, unverified), adds the
// password bound to it, and revokes the guest's `anon` credentials with reason
// `guest_upgraded`. Revoking the `anon` credential ends every session that
// holds it as a factor, including the one that made this request, so the
// client signs in again with the new password.
//
// The personal profile already exists: a guest is a bring-your-own user. An
// already registered caller is ErrNotGuest; an email that is taken is
// ErrIdentifierTaken. ErrBYOIdentityDisabled when the platform does not allow
// bring-your-own identities; nothing is written. Observations: `update` `user` (the upgrade), plus the
// `create` and `revoke` events of the identifier, credential, person and
// sessions it touches.
func (s *UserService) SelfUpgradeGuest(ctx context.Context, in UpgradeGuestInput) (UserView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return UserView{}, err
	}
	given, family := strings.TrimSpace(in.GivenName), strings.TrimSpace(in.FamilyName)
	if given == "" {
		return UserView{}, invalidField("given_name", "users.given_name_required", "given_name is required")
	}
	if family == "" {
		return UserView{}, invalidField("family_name", "users.family_name_required", "family_name is required")
	}
	if utf8.RuneCountInString(given) > maxNameRunes {
		return UserView{}, invalidField("given_name", "users.given_name_too_long", "given_name is too long")
	}
	if utf8.RuneCountInString(family) > maxNameRunes {
		return UserView{}, invalidField("family_name", "users.family_name_too_long", "family_name is too long")
	}
	canon := CanonicalizeEmail(in.Email)
	if err := validateEmail(canon); err != nil {
		return UserView{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return UserView{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return UserView{}, err
	}

	userID := ac.PrincipalID
	var view UserView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		q, cq := s.usersQ(tx), s.coreQuerier(tx)
		user, err := q.GetUserByEntityIDForUpdate(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("users: lock user: %w", err)
		}
		if !user.IsGuest {
			return ErrNotGuest
		}
		settings, err := cq.GetPlatformSettings(ctx)
		if err != nil {
			return fmt.Errorf("users: load platform settings: %w", err)
		}
		if !settings.AllowByoIdentity {
			return ErrBYOIdentityDisabled
		}
		userEnt, err := cq.GetEntityByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("users: load user entity: %w", err)
		}
		personal, err := cq.GetPersonalProfileByHolder(ctx, userID)
		if err != nil {
			return fmt.Errorf("users: load personal profile: %w", err)
		}
		profileID := personal.EntityID

		_, _, personID, err := s.np.CreateInTx(ctx, tx, coreservice.CreateNaturalPersonInput{
			GivenName:          given,
			FamilyName:         family,
			OwnerID:            &userID,
			CreatedByProfileID: &profileID,
		})
		if err != nil {
			return fmt.Errorf("users: create person: %w", err)
		}
		upgraded, err := q.UpgradeGuestUser(ctx, usersdb.UpgradeGuestUserParams{
			EntityID: userID, PersonID: pgtype.Int8{Int64: personID, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotGuest // upgraded concurrently
		}
		if err != nil {
			return fmt.Errorf("users: upgrade guest: %w", err)
		}

		ident, err := s.AddIdentifierInTx(ctx, tx, AddIdentifierInput{
			UserID: userID, Kind: IdentifierKindEmail, Value: canon, Primary: true,
		})
		if err != nil {
			return err
		}
		if _, err := s.creds.CreatePasswordInTx(ctx, tx, CreatePasswordInput{
			PrincipalID: userID, IdentifierID: &ident.ID, Password: in.Password,
		}); err != nil {
			return err
		}

		anons, err := q.ListActiveCredentialsByPrincipal(ctx, userID)
		if err != nil {
			return fmt.Errorf("users: list credentials: %w", err)
		}
		for _, c := range anons {
			if c.Kind != KindAnon {
				continue
			}
			if err := s.creds.RevokeInTx(ctx, tx, c.ID, RevokeReasonGuestUpgraded); err != nil {
				return err
			}
		}

		if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceUser, userID,
			userAudit(user, userEnt.Uuid), userAudit(upgraded, userEnt.Uuid)); err != nil {
			return err
		}
		view, err = loadUserView(ctx, tx, userID)
		return err
	})
	if err != nil {
		return UserView{}, err
	}
	return view, nil
}
