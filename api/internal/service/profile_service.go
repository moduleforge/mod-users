package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// ProfileService owns the lifecycle of party profiles: invitation, acceptance,
// suspension, resumption and ending, with the departure cleanup an ending
// triggers, managed-user provisioning, and the lifecycle observers that other
// modules (mod-billing seats) subscribe to.
//
// Two layers share it:
//
//   - The in-transaction primitives (InviteInTx, EndInTx, ProvisionManagedUserInTx
//     and the unexported createInTx and transitionLocked that the trusted
//     admission path reuses) take the caller's transaction and do not commit.
//     InviteInTx and ProvisionManagedUserInTx authorize the creation they perform;
//     EndInTx does not: its caller authorizes (the holder acting on their own
//     profile, or an administrator holding `update` on it).
//   - The HTTP-facing methods (ListPartyProfiles, InviteProfile,
//     CreateManagedUser, UpdateProfileStatus, AcceptInvitation, DeclineInvitation,
//     LeaveProfile) take uuids, authorize, open their own transaction and
//     render views. See profile_admin.go and profile_holder.go.
//
// Every status change, creation included, runs the registered
// ProfileLifecycleObservers inside the transaction and emits a `profile`
// observation with an op from the closed audit vocabulary (API surface section
// 10.1): `create` for a new profile and `update` for every status change. An
// observer error aborts the transaction.
type ProfileService struct {
	q     usersdb.Querier
	coreQ coredb.Querier
	az    coreAuthz.Authorizer
	obs   *observer.ObserverGroup
	pool  *pgxpool.Pool
	users *UserService
	creds *CredentialService
	// sessions renders the holder's view of a profile after accepting it.
	sessions *SessionService

	newQ     func(pgx.Tx) usersdb.Querier
	newCoreQ func(pgx.Tx) coredb.Querier
	now      func() time.Time

	mu        sync.RWMutex
	observers []ProfileLifecycleObserver
}

// NewProfileService constructs a ProfileService. users must have been wired
// with UserService.WithSelfService (it supplies the freshness gate, the
// session revocation of a disabled managed user and the primitives that create
// the managed user); creds revokes the credentials confined to a party the
// holder leaves; sessions renders the holder's ProfileView after an accept.
// pool serves the HTTP-facing methods' transactions.
func NewProfileService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
	creds *CredentialService,
	sessions *SessionService,
) *ProfileService {
	return &ProfileService{
		q: q, coreQ: coreQ, az: az, obs: obs, pool: pool,
		users: users, creds: creds, sessions: sessions, now: time.Now,
	}
}

func (s *ProfileService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

func (s *ProfileService) coreQuerier(tx pgx.Tx) coredb.Querier {
	if s.newCoreQ != nil {
		return s.newCoreQ(tx)
	}
	return coredb.New(tx)
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (s *ProfileService) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if s.pool == nil {
		return errors.New("users: no database pool configured for profile administration")
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

// Profile statuses (public.profiles.status) and the profile source this
// service writes.
const (
	ProfileStatusInvited   = "invited"
	ProfileStatusActive    = "active"
	ProfileStatusSuspended = "suspended"
	ProfileStatusEnded     = "ended"

	// ProfileSourceManual is the source of every profile this service creates.
	ProfileSourceManual = "manual"
)

// End reasons the holder's own actions record.
const (
	EndReasonLeft     = "left"
	EndReasonDeclined = "declined"
)

// maxEndReasonRunes bounds the free-text end reason of a profile.
const maxEndReasonRunes = 500

// Observer resources this service emits beyond the shared ones.
const (
	resourceProfile                      = "profile"
	resourceCredentialPartyAuthorization = "credential_party_authorization"
)

// Error codes owned by the profile lifecycle.
const (
	CodeHolderNotAdmissible = "users.holder_not_admissible"
	CodeProfileTransition   = "users.profile_transition_invalid"
	CodeProfileEndReason    = "users.end_reason_required"
)

// ErrHolderNotAdmissible is returned when the intended holder cannot take a
// profile in the party: a managed user of another party, or a principal that
// is not a user (users.holder_not_admissible, 409). The trusted admission path
// reuses it (decision U22).
var ErrHolderNotAdmissible = apiresp.Conflict(apiresp.FieldError{
	Field: "holder", Code: CodeHolderNotAdmissible, Message: "this account cannot hold a profile in this party",
})

// ErrProfileExists is returned when the holder already has an open (not ended)
// profile in the party (users.profile_exists, 409), as decided by mod-core's
// profiles_one_open_per_holder_party index.
var ErrProfileExists = apiresp.Conflict(apiresp.FieldError{
	Field: "holder", Code: CodeProfileExists, Message: "the account already has a profile in this party",
})

// ErrProfileTransition is returned when the requested status change is not
// allowed from the profile's current status (users.profile_transition_invalid,
// 409). The graph is invited to active, active and suspended to each other,
// and any open status to ended, which is terminal.
var ErrProfileTransition = apiresp.Conflict(apiresp.FieldError{
	Field: "status", Code: CodeProfileTransition, Message: "the profile cannot change to that status from its current status",
})

// profileTransitions is mod-core's status graph (profiles_check_invariants).
var profileTransitions = map[[2]string]bool{
	{ProfileStatusInvited, ProfileStatusActive}:   true,
	{ProfileStatusInvited, ProfileStatusEnded}:    true,
	{ProfileStatusActive, ProfileStatusSuspended}: true,
	{ProfileStatusSuspended, ProfileStatusActive}: true,
	{ProfileStatusActive, ProfileStatusEnded}:     true,
	{ProfileStatusSuspended, ProfileStatusEnded}:  true,
}

// transitionAllowed reports whether from to to is an edge of mod-core's status
// graph. A change to the same status is not an edge.
func transitionAllowed(from, to string) bool {
	return profileTransitions[[2]string{from, to}]
}

// ---------------------------------------------------------------------------
// types (API surface 6.3, binding)
// ---------------------------------------------------------------------------

// InviteInput is the input of InviteInTx. Exactly one of HolderUserID and
// HolderEmail names the holder.
type InviteInput struct {
	PartyID      int64
	HolderUserID int64
	HolderEmail  string
	// ConfirmPolicyMismatch confirms an invitation whose holder cannot meet the
	// party's authentication policy with their current credentials. It is
	// carried to the policy-mismatch seam (checkInvitePolicyMismatch).
	ConfirmPolicyMismatch bool
}

// Profile is the service-layer view of a party profile. Ids are internal; they
// must not reach an HTTP response.
type Profile struct {
	ID                int64
	UUID              uuid.UUID
	HolderPrincipalID int64
	PartyID           int64
	Status            string
	Source            string
}

// ProfileStatusChange describes one profile creation or status change. From is
// empty when the profile was just created.
type ProfileStatusChange struct {
	ProfileID, HolderPrincipalID, PartyID int64
	From, To                              string
}

// ProfileLifecycleObserver is told of every profile creation and status
// change, inside the transaction that makes it. An error aborts the
// transaction.
type ProfileLifecycleObserver interface {
	ProfileStatusChanged(ctx context.Context, tx pgx.Tx, ch ProfileStatusChange) error
}

// AddLifecycleObserver registers o. Observers run in registration order.
func (s *ProfileService) AddLifecycleObserver(o ProfileLifecycleObserver) {
	if o == nil {
		return
	}
	s.mu.Lock()
	s.observers = append(s.observers, o)
	s.mu.Unlock()
}

// notify runs every registered observer for ch.
func (s *ProfileService) notify(ctx context.Context, tx pgx.Tx, ch ProfileStatusChange) error {
	s.mu.RLock()
	obs := append([]ProfileLifecycleObserver(nil), s.observers...)
	s.mu.RUnlock()
	for _, o := range obs {
		if err := o.ProfileStatusChanged(ctx, tx, ch); err != nil {
			return fmt.Errorf("users: profile lifecycle observer: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// reading
// ---------------------------------------------------------------------------

// lockProfileSQL reads a profile row and locks it, serializing the status
// changes of one profile.
const lockProfileSQL = `
SELECT entity_id, holder_id, party_id, status, source, invited_by_profile_id,
       started_at, ended_at, end_reason, created_at, updated_at
  FROM public.profiles WHERE entity_id = $1 FOR UPDATE`

// lockProfile loads and locks the profile; a missing one is apiresp.ErrNotFound.
func lockProfile(ctx context.Context, tx pgx.Tx, id int64) (coredb.Profile, error) {
	var p coredb.Profile
	err := tx.QueryRow(ctx, lockProfileSQL, id).Scan(
		&p.EntityID, &p.HolderID, &p.PartyID, &p.Status, &p.Source, &p.InvitedByProfileID,
		&p.StartedAt, &p.EndedAt, &p.EndReason, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return coredb.Profile{}, apiresp.ErrNotFound
	}
	if err != nil {
		return coredb.Profile{}, fmt.Errorf("users: lock profile: %w", err)
	}
	return p, nil
}

func toProfile(p coredb.Profile, id uuid.UUID) Profile {
	return Profile{
		ID: p.EntityID, UUID: id, HolderPrincipalID: p.HolderID, PartyID: p.PartyID.Int64,
		Status: p.Status, Source: p.Source,
	}
}

// profileAudit is the observer payload for a profile: public identifiers and
// the non-secret attributes only.
func profileAudit(id, holder, party uuid.UUID, status, source string) map[string]any {
	return map[string]any{
		"uuid": id.String(), "holder_uuid": holder.String(), "party_uuid": party.String(),
		"status": status, "source": source,
	}
}

// auditUUIDs resolves the public uuids of a profile, its holder and its party.
func auditUUIDs(ctx context.Context, tx pgx.Tx, profileID, holderID, partyID int64) (profile, holder, party uuid.UUID, err error) {
	m, err := entityUUIDs(ctx, tx, []int64{profileID, holderID, partyID})
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	return m[profileID], m[holderID], m[partyID], nil
}

// ---------------------------------------------------------------------------
// creation
// ---------------------------------------------------------------------------

// newProfile describes a profile createInTx inserts.
type newProfile struct {
	HolderID int64
	// Holder is the holder's users row, used to name the cause of an MF103
	// refusal. It must be the row of HolderID.
	Holder             usersdb.User
	PartyID            int64
	Status             string // invited or active
	Source             string
	InvitedByProfileID int64 // 0 when none
}

// createInTx inserts the profile entity (owned by the party, created by the
// acting profile on ctx) and its profiles row, runs the lifecycle observers and
// emits the `create` `profile` observation. It performs no authorization and
// no admissibility pre-check; a refusal by the database triggers is mapped by
// mapProfileInsertError. The trusted admission path (AdmitInTx) reuses it.
func (s *ProfileService) createInTx(ctx context.Context, tx pgx.Tx, np newProfile) (Profile, error) {
	cq := s.coreQuerier(tx)
	profileType, err := cq.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return Profile{}, fmt.Errorf("users: resolve profile type: %w", err)
	}
	ent, err := s.users.createEntity(ctx, cq, profileType.ID, pgtype.Int8{Int64: np.PartyID, Valid: true})
	if err != nil {
		return Profile{}, err
	}
	var startedAt *time.Time
	if np.Status == ProfileStatusActive {
		t := ent.CreatedAt.Time
		startedAt = &t
	}
	row, err := cq.CreateProfile(ctx, coredb.CreateProfileParams{
		EntityID:           ent.ID,
		HolderID:           np.HolderID,
		PartyID:            pgtype.Int8{Int64: np.PartyID, Valid: true},
		Status:             np.Status,
		Source:             np.Source,
		InvitedByProfileID: pgtype.Int8{Int64: np.InvitedByProfileID, Valid: np.InvitedByProfileID > 0},
		StartedAt:          startedAt,
	})
	if err != nil {
		return Profile{}, mapProfileInsertError(err, np.Holder)
	}

	prof := toProfile(row, ent.Uuid)
	_, holderUUID, partyUUID, err := auditUUIDs(ctx, tx, ent.ID, np.HolderID, np.PartyID)
	if err != nil {
		return Profile{}, err
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceProfile, ent.ID, nil,
		profileAudit(ent.Uuid, holderUUID, partyUUID, row.Status, row.Source)); err != nil {
		return Profile{}, err
	}
	if err := s.notify(ctx, tx, ProfileStatusChange{
		ProfileID: ent.ID, HolderPrincipalID: np.HolderID, PartyID: np.PartyID, To: row.Status,
	}); err != nil {
		return Profile{}, err
	}
	return prof, nil
}

// mapProfileInsertError translates the refusals of a profile insert: the open
// profile index to ErrProfileExists, and the mod-users holder trigger (MF103)
// or mod-core's holder rule (MF004) to the admissibility errors, naming a
// guest holder from holder. Anything else is wrapped as an internal error.
func mapProfileInsertError(err error, holder usersdb.User) error {
	if code, ok := UniqueViolationCode(err); ok && code == CodeProfileExists {
		return ErrProfileExists
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == UserInvariantSQLState || pgErr.Code == profileInvariantSQLState) {
		if holder.IsGuest {
			return ErrGuestMustUpgrade
		}
		return ErrHolderNotAdmissible
	}
	return fmt.Errorf("users: create profile: %w", err)
}

// profileInvariantSQLState is mod-core's profile invariant code (MF004).
const profileInvariantSQLState = "MF004"

// checkAdmissible reports why holder cannot take a profile in partyID: a guest
// must upgrade first, a managed user belongs to its managing party only. It is
// the Go-side form of the profiles_users_holder_rules trigger, checked first so
// the caller gets the binding errors without a failed insert.
func checkAdmissible(holder usersdb.User, partyID int64) error {
	if holder.IsGuest {
		return ErrGuestMustUpgrade
	}
	if holder.ManagingPartyID.Valid && holder.ManagingPartyID.Int64 != partyID {
		return ErrHolderNotAdmissible
	}
	return nil
}

// ---------------------------------------------------------------------------
// invitation
// ---------------------------------------------------------------------------

// validateInvite checks the shape of in: a party, and exactly one of holder id
// (or, for the HTTP path, holder uuid) and holder email, the email being a valid
// address. It returns the canonical email.
func validateInvite(in InviteInput, holderUUID uuid.UUID) (canonEmail string, err error) {
	if in.PartyID <= 0 {
		return "", invalidField("party", "users.party_required", "a party is required")
	}
	named := 0
	if in.HolderUserID > 0 {
		named++
	}
	if holderUUID != uuid.Nil {
		named++
	}
	hasEmail := strings.TrimSpace(in.HolderEmail) != ""
	if hasEmail {
		named++
	}
	switch {
	case named > 1:
		return "", invalidField("holder", "users.holder_ambiguous", "give either holder_email or holder_user_uuid, not both")
	case named == 0:
		return "", invalidField("holder", "users.holder_required", "holder_email or holder_user_uuid is required")
	case !hasEmail:
		return "", nil
	}
	canon := CanonicalizeEmail(in.HolderEmail)
	if err := validateEmail(canon); err != nil {
		return "", invalidField("holder_email", "users.email_invalid", "holder_email is not a valid address")
	}
	return canon, nil
}

// InviteInTx invites a user to the party: it creates a mod-core profile with
// status `invited`, source `manual`, owned by the party, invited by the acting
// profile on ctx. The holder is named by HolderUserID or by HolderEmail (the
// canonical value of an email identifier; an unknown address is
// apiresp.ErrNotFound, and no account is created by an invitation).
//
// Authorization is AuthorizeTypeIn(create, profile, party) as the acting
// profile, before the holder is looked up, so a caller without it learns
// nothing about who exists. Errors: a 400 field error for a malformed input;
// ErrGuestMustUpgrade for a guest holder; ErrHolderNotAdmissible for a managed
// user of another party or a principal that is not a user; ErrProfileExists
// when the holder already has an open profile in the party; the
// policy-mismatch confirmation error of checkInvitePolicyMismatch.
//
// Observations: `create` `profile`; the lifecycle observers see a change from
// "" to `invited`.
func (s *ProfileService) InviteInTx(ctx context.Context, tx pgx.Tx, in InviteInput) (Profile, error) {
	return s.invite(ctx, tx, inviteRequest{InviteInput: in})
}

// inviteRequest is InviteInput plus the holder named by uuid, which the HTTP
// path resolves only after the caller is authorized.
type inviteRequest struct {
	InviteInput
	HolderUserUUID uuid.UUID
}

// invite is InviteInTx for either way of naming the holder.
func (s *ProfileService) invite(ctx context.Context, tx pgx.Tx, req inviteRequest) (Profile, error) {
	in := req.InviteInput
	canon, err := validateInvite(in, req.HolderUserUUID)
	if err != nil {
		return Profile{}, err
	}
	ac, err := adminCaller(ctx)
	if err != nil {
		return Profile{}, err
	}
	cq := s.coreQuerier(tx)
	profileType, err := cq.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return Profile{}, fmt.Errorf("users: resolve profile type: %w", err)
	}
	party := in.PartyID
	if err := coreAuthz.AuthorizeTypeIn(ctx, s.az, "create", profileType.ID, &party); err != nil {
		return Profile{}, err
	}
	if err := s.requireParty(ctx, cq, party); err != nil {
		return Profile{}, err
	}

	holder, err := s.resolveInvitee(ctx, tx, req, canon)
	if err != nil {
		return Profile{}, err
	}
	if err := checkAdmissible(holder, party); err != nil {
		return Profile{}, err
	}
	if err := s.checkInvitePolicyMismatch(ctx, tx, in, holder.EntityID); err != nil {
		return Profile{}, err
	}
	return s.createInTx(ctx, tx, newProfile{
		HolderID: holder.EntityID, Holder: holder, PartyID: party,
		Status: ProfileStatusInvited, Source: ProfileSourceManual, InvitedByProfileID: ac.ProfileID,
	})
}

// checkInvitePolicyMismatch is the seam for the party-policy mismatch
// confirmation of an invitation: refuse with 409
// users.policy_mismatch_confirmation_required when the holder's credentials
// cannot meet the party's authentication policy and in.ConfirmPolicyMismatch is
// false, and record the confirmation otherwise.
//
// It is a no-op returning nil here. Phase task 003 (outside collaborators and
// the policy-mismatch detection) owns it and fills it in; InviteInTx already
// carries ConfirmPolicyMismatch to this single call site.
func (s *ProfileService) checkInvitePolicyMismatch(ctx context.Context, tx pgx.Tx, in InviteInput, holderID int64) error {
	return nil
}

// requireParty reports apiresp.ErrNotFound unless partyID is a live legal
// entity.
func (s *ProfileService) requireParty(ctx context.Context, cq coredb.Querier, partyID int64) error {
	ent, err := cq.GetEntityByID(ctx, partyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apiresp.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("users: load party: %w", err)
	}
	if ent.ArchivedAt != nil {
		return apiresp.ErrNotFound
	}
	if _, err := cq.GetLegalEntityByEntityID(ctx, partyID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrNotFound
		}
		return fmt.Errorf("users: load party: %w", err)
	}
	return nil
}

// resolveInvitee loads the users row of the invited holder. An unknown email or
// entity is apiresp.ErrNotFound; an entity that is a principal but not a user
// (a service account) is ErrHolderNotAdmissible.
func (s *ProfileService) resolveInvitee(ctx context.Context, tx pgx.Tx, req inviteRequest, canonEmail string) (usersdb.User, error) {
	q := s.usersQ(tx)
	holderID := req.HolderUserID
	if holderID <= 0 && req.HolderUserUUID != uuid.Nil {
		ent, err := s.coreQuerier(tx).GetEntityByUUID(ctx, req.HolderUserUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			return usersdb.User{}, apiresp.ErrNotFound
		}
		if err != nil {
			return usersdb.User{}, fmt.Errorf("users: look up invitee: %w", err)
		}
		holderID = ent.ID
	}
	if holderID <= 0 {
		ident, err := q.GetIdentifierByKindValue(ctx, usersdb.GetIdentifierByKindValueParams{
			Kind: IdentifierKindEmail, ValueCanon: canonEmail,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return usersdb.User{}, apiresp.ErrNotFound
		}
		if err != nil {
			return usersdb.User{}, fmt.Errorf("users: look up invitee: %w", err)
		}
		holderID = ident.UserID
	}
	user, err := q.GetUserByEntityID(ctx, holderID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, perr := s.coreQuerier(tx).GetPrincipalByEntityID(ctx, holderID); perr == nil {
			return usersdb.User{}, ErrHolderNotAdmissible
		} else if !errors.Is(perr, pgx.ErrNoRows) {
			return usersdb.User{}, fmt.Errorf("users: look up invitee: %w", perr)
		}
		return usersdb.User{}, apiresp.ErrNotFound
	}
	if err != nil {
		return usersdb.User{}, fmt.Errorf("users: look up invitee: %w", err)
	}
	return user, nil
}

// ---------------------------------------------------------------------------
// status changes
// ---------------------------------------------------------------------------

// transitionLocked moves the locked profile p to status to: it checks the
// graph (ErrProfileTransition otherwise), updates the row, emits the `update`
// `profile` observation and runs the lifecycle observers. reason is the end
// reason, used only for `ended`. It performs no authorization and no departure
// cleanup (endLocked adds it); the trusted admission path reuses it.
func (s *ProfileService) transitionLocked(ctx context.Context, tx pgx.Tx, p coredb.Profile, to, reason string) (coredb.Profile, error) {
	if !p.PartyID.Valid {
		// Personal profiles have no lifecycle here (active or suspended only).
		return coredb.Profile{}, apiresp.ErrNotFound
	}
	if !transitionAllowed(p.Status, to) {
		return coredb.Profile{}, ErrProfileTransition
	}
	cq := s.coreQuerier(tx)
	updated, err := cq.UpdateProfileStatus(ctx, coredb.UpdateProfileStatusParams{
		EntityID:  p.EntityID,
		Status:    to,
		EndReason: pgtype.Text{String: reason, Valid: to == ProfileStatusEnded},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == profileInvariantSQLState {
			return coredb.Profile{}, ErrProfileTransition
		}
		return coredb.Profile{}, fmt.Errorf("users: update profile status: %w", err)
	}

	profileUUID, holderUUID, partyUUID, err := auditUUIDs(ctx, tx, p.EntityID, p.HolderID, p.PartyID.Int64)
	if err != nil {
		return coredb.Profile{}, err
	}
	if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceProfile, p.EntityID,
		profileAudit(profileUUID, holderUUID, partyUUID, p.Status, p.Source),
		profileAudit(profileUUID, holderUUID, partyUUID, updated.Status, updated.Source)); err != nil {
		return coredb.Profile{}, err
	}
	if err := s.notify(ctx, tx, ProfileStatusChange{
		ProfileID: p.EntityID, HolderPrincipalID: p.HolderID, PartyID: p.PartyID.Int64, From: p.Status, To: updated.Status,
	}); err != nil {
		return coredb.Profile{}, err
	}
	return updated, nil
}

// validateEndReason trims and checks an end reason: not blank, at most 500
// characters, no control characters.
func validateEndReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", invalidField("end_reason", CodeProfileEndReason, "end_reason is required")
	}
	if utf8.RuneCountInString(reason) > maxEndReasonRunes {
		return "", invalidField("end_reason", "users.end_reason_too_long", "end_reason is too long")
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", invalidField("end_reason", "users.end_reason_invalid", "end_reason contains invalid characters")
		}
	}
	return reason, nil
}

// EndInTx ends the profile with reason and runs the departure cleanup. It
// performs no authorization: the caller authorizes (the holder on their own
// profile, an administrator with `update` on it, or a trusted flow). reason is
// required (a 400 field error otherwise). Ending an ended profile changes
// nothing. A personal profile is apiresp.ErrNotFound.
//
// Departure cleanup, in the caller's transaction: the holder's
// credential_party_authorizations rows for the party are deleted
// (`delete` `credential_party_authorization` for each); the holder's
// credentials confined to the party are revoked with reason `departure`
// through CredentialService.RevokeInTx (`revoke` `credential`, and the sessions
// holding them end); and when the holder is a managed user with no profile
// left that is not ended, the principal is disabled and its sessions are
// revoked (`update` `user`). No trigger cascades to sessions: per-request
// liveness already rejects a token naming an ended profile.
//
// Observations: `update` `profile`; the lifecycle observers see the change to
// `ended`.
func (s *ProfileService) EndInTx(ctx context.Context, tx pgx.Tx, profileID int64, reason string) error {
	reason, err := validateEndReason(reason)
	if err != nil {
		return err
	}
	p, err := lockProfile(ctx, tx, profileID)
	if err != nil {
		return err
	}
	if p.Status == ProfileStatusEnded {
		if !p.PartyID.Valid {
			return apiresp.ErrNotFound
		}
		return nil
	}
	return s.endLocked(ctx, tx, p, reason)
}

// endLocked ends the locked, not yet ended profile p and runs the departure
// cleanup.
func (s *ProfileService) endLocked(ctx context.Context, tx pgx.Tx, p coredb.Profile, reason string) error {
	ended, err := s.transitionLocked(ctx, tx, p, ProfileStatusEnded, reason)
	if err != nil {
		return err
	}
	return s.departureCleanupInTx(ctx, tx, ended)
}

// departureCleanupInTx removes what a profile that just ended granted its
// holder (see EndInTx).
func (s *ProfileService) departureCleanupInTx(ctx context.Context, tx pgx.Tx, p coredb.Profile) error {
	holder, party := p.HolderID, p.PartyID.Int64
	q := s.usersQ(tx)

	// 1. The holder's credential authorizations for the party.
	auths, err := q.ListCredentialPartyAuthorizationsByParty(ctx, party)
	if err != nil {
		return fmt.Errorf("users: list credential authorizations: %w", err)
	}
	var mine []usersdb.ListCredentialPartyAuthorizationsByPartyRow
	for _, a := range auths {
		if a.PrincipalID == holder {
			mine = append(mine, a)
		}
	}
	if len(mine) > 0 {
		if _, err := q.DeleteCredentialAuthorizationsForHolderParty(ctx, usersdb.DeleteCredentialAuthorizationsForHolderPartyParams{
			PartyID: party, PrincipalID: holder,
		}); err != nil {
			return fmt.Errorf("users: delete credential authorizations: %w", err)
		}
		partyUUIDs, err := entityUUIDs(ctx, tx, []int64{party})
		if err != nil {
			return err
		}
		for _, a := range mine {
			if err := observeInTx(ctx, s.obs, tx, opDelete, resourceCredentialPartyAuthorization, party,
				map[string]any{"credential_uuid": a.CredentialUuid.String(), "party_uuid": partyUUIDs[party].String()}, nil); err != nil {
				return err
			}
		}
	}

	// 2. The holder's credentials confined to the party.
	creds, err := q.ListActiveCredentialsByPrincipal(ctx, holder)
	if err != nil {
		return fmt.Errorf("users: list credentials: %w", err)
	}
	for _, c := range creds {
		if c.ConfinedToPartyID.Valid && c.ConfinedToPartyID.Int64 == party {
			if err := s.creds.RevokeInTx(ctx, tx, c.ID, RevokeReasonDeparture); err != nil {
				return err
			}
		}
	}

	// 3. A managed user left with no open profile.
	user, err := q.GetUserByEntityID(ctx, holder)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !user.ManagingPartyID.Valid) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("users: load holder: %w", err)
	}
	var open int64
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM public.profiles WHERE holder_id = $1 AND status <> 'ended'`, holder).Scan(&open); err != nil {
		return fmt.Errorf("users: count open profiles: %w", err)
	}
	if open > 0 {
		return nil
	}
	sums, err := ReadPrincipalSummaries(ctx, tx, []int64{holder})
	if err != nil {
		return err
	}
	sum, ok := sums[holder]
	if !ok {
		return nil
	}
	return s.users.disablePrincipalInTx(ctx, tx, sum, "the last profile of the managed user ended")
}

// ---------------------------------------------------------------------------
// managed users
// ---------------------------------------------------------------------------

// ManagedUserInput is the input of ProvisionManagedUserInTx.
type ManagedUserInput struct {
	PartyID int64
	// Email is the user's sign-in address (an unverified email identifier,
	// primary).
	Email string
	// Password is the initial password of the user's password credential, bound
	// to the email identifier. The administrator conveys it out of band.
	Password string
}

// ProvisionedManagedUser is the result of ProvisionManagedUserInTx.
type ProvisionedManagedUser struct {
	User         CreatedUser
	Profile      Profile
	IdentifierID int64
	CredentialID int64
}

// ProvisionManagedUserInTx creates, in the caller's transaction, a managed
// user for the party: a `user` principal owned by the party with
// managing_party_id set, no personal profile and no person (decision U11), an
// unverified primary email identifier, an active party profile (source
// `manual`) and an initial password credential bound to the identifier whose
// landing profile is that party profile.
//
// The credential is a password, not an email code: an email_otp credential
// exists only for a verified address (decision U12), and an administrator
// cannot vouch for the address on the user's behalf. The user verifies the
// address later, which also creates their email_otp credential.
//
// Authorization is AuthorizeTypeIn(create, user, party) (UserService) and
// AuthorizeTypeIn(create, profile, party), both before any write. Errors: 400
// for a malformed email or password, ErrIdentifierTaken when the address is
// registered.
//
// Observations: `create` `user`, `create` `user_identifier`, `create`
// `profile` and `create` `credential`; the lifecycle observers see a change
// from "" to `active`.
func (s *ProfileService) ProvisionManagedUserInTx(ctx context.Context, tx pgx.Tx, in ManagedUserInput) (ProvisionedManagedUser, error) {
	if in.PartyID <= 0 {
		return ProvisionedManagedUser{}, invalidField("party", "users.party_required", "a party is required")
	}
	canon := CanonicalizeEmail(in.Email)
	if err := validateEmail(canon); err != nil {
		return ProvisionedManagedUser{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return ProvisionedManagedUser{}, err
	}
	if _, err := adminCaller(ctx); err != nil {
		return ProvisionedManagedUser{}, err
	}
	cq := s.coreQuerier(tx)
	profileType, err := cq.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return ProvisionedManagedUser{}, fmt.Errorf("users: resolve profile type: %w", err)
	}
	party := in.PartyID
	if err := coreAuthz.AuthorizeTypeIn(ctx, s.az, "create", profileType.ID, &party); err != nil {
		return ProvisionedManagedUser{}, err
	}
	if err := s.requireParty(ctx, cq, party); err != nil {
		return ProvisionedManagedUser{}, err
	}

	created, err := s.users.CreateManagedUserInTx(ctx, tx, CreateManagedUserInput{ManagingPartyID: party})
	if err != nil {
		return ProvisionedManagedUser{}, err
	}
	userID := created.User.EntityID
	ident, err := s.users.AddIdentifierInTx(ctx, tx, AddIdentifierInput{
		UserID: userID, Kind: IdentifierKindEmail, Value: canon, Primary: true,
	})
	if err != nil {
		return ProvisionedManagedUser{}, err
	}
	prof, err := s.createInTx(ctx, tx, newProfile{
		HolderID: userID, Holder: created.User, PartyID: party,
		Status: ProfileStatusActive, Source: ProfileSourceManual,
	})
	if err != nil {
		return ProvisionedManagedUser{}, err
	}
	cred, err := s.creds.CreatePasswordInTx(ctx, tx, CreatePasswordInput{
		PrincipalID: userID, IdentifierID: &ident.ID, Password: in.Password, DefaultProfileID: &prof.ID,
	})
	if err != nil {
		return ProvisionedManagedUser{}, err
	}
	return ProvisionedManagedUser{User: created, Profile: prof, IdentifierID: ident.ID, CredentialID: cred.ID}, nil
}
