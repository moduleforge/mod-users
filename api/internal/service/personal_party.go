package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Error codes owned by the personal party flow.
const (
	CodePersonalProfileRequired = "users.personal_profile_required"
	CodePersonNotOwned          = "users.person_not_owned"
)

// resourceNaturalPerson is the observer resource of a person record the
// personal party flow creates (API surface section 10.1).
const resourceNaturalPerson = "natural_person"

// naturalPersonTypeSlug is the mod-core type slug of a person party.
const naturalPersonTypeSlug = "natural_person"

// ErrPersonalProfileRequired is returned when the acting profile is not a
// user's personal profile: a party profile, a managed user's profile, or a
// profile held by a principal that is not a user (users.personal_profile_required,
// 403).
var ErrPersonalProfileRequired = &AuthError{
	Status: http.StatusForbidden, Code: CodePersonalProfileRequired,
	Message: "the acting profile must be a user's personal profile",
}

// ErrPersonalPartyNotOwned is returned when users.person_id links a person
// record the user does not own (users.person_not_owned, 409). The flow never
// grants authority over a person it did not create for the user.
var ErrPersonalPartyNotOwned = apiresp.Conflict(apiresp.FieldError{
	Field: "person", Code: CodePersonNotOwned, Message: "the linked person record is not owned by the user",
})

// PersonalParty is the outcome of EnsurePersonalParty. Ids are internal; they
// must not reach an HTTP response.
type PersonalParty struct {
	// PartyID and PartyUUID identify the user's natural_person party.
	PartyID   int64
	PartyUUID uuid.UUID
	// GrantID is the owned-scope `manage` grant held by the personal profile.
	GrantID int64
	// Created is true only when this call created the person or the grant.
	Created bool
}

// PersonalPartyService gives a user a party of their own for personal team
// plans (resolution (b), decisions U1 and U2): the user's own natural_person
// and the owned-scope grant that lets the personal profile administer
// everything that person owns.
type PersonalPartyService struct {
	obs      *observer.ObserverGroup
	newQ     func(pgx.Tx) usersdb.Querier
	newCoreQ func(pgx.Tx) coredb.Querier
	newAuthz func(pgx.Tx) authzdb.Querier
}

// NewPersonalPartyService constructs a PersonalPartyService. obs receives the
// in-transaction observations; nil disables them (unit tests).
func NewPersonalPartyService(obs *observer.ObserverGroup) *PersonalPartyService {
	return &PersonalPartyService{obs: obs}
}

func (s *PersonalPartyService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

func (s *PersonalPartyService) coreQuerier(tx pgx.Tx) coredb.Querier {
	if s.newCoreQ != nil {
		return s.newCoreQ(tx)
	}
	return coredb.New(tx)
}

func (s *PersonalPartyService) authzQ(tx pgx.Tx) authzdb.Querier {
	if s.newAuthz != nil {
		return s.newAuthz(tx)
	}
	return authzdb.New(tx)
}

// EnsurePersonalParty idempotently gives the acting user a person party and
// the grant over it, entirely in the caller's transaction tx (mod-billing
// calls it for personal team plans).
//
// The acting profile (opctx.Auth(ctx).ProfileID) must be a user's personal
// profile. Refusals: ErrGuestMustUpgrade (409 users.guest_upgrade_required) for
// a guest; ErrPersonalProfileRequired (403) for a managed user, a party profile
// or a non-user principal; ErrPersonalPartyNotOwned (409 users.person_not_owned)
// when users.person_id links a person the user does not own; apiresp.
// ErrUnauthenticated without an acting profile.
//
// It does the following, in order:
//
//   - When users.person_id is NULL, creates the user's natural_person through
//     mod-core's CreateEntity, owned by the user (decision U1), and links it.
//     An existing linked person the user owns is reused.
//   - Ensures the owned-scope `manage` grant (decision U2): held by the
//     personal profile, scope 'owned', target the person, no target type, no
//     subtypes. An equal existing grant is reused. It is a trusted insert: no
//     grant is consulted.
//
// Created is true only when the person or the grant was created. Observations
// (API surface section 10.1, decision U21): `create` `natural_person` for a new
// person, `update` `user` for the person_id link, `grant` `grant` (target: the
// person) for a new grant. A reused person or grant emits nothing.
//
// Concurrency: the user's mod_users.users row is locked FOR UPDATE for the rest
// of tx, so two concurrent calls for one user serialize; the second re-reads
// the committed person link and grant and reuses them. The lock is the same
// holder lock AdmitInTx and InviteInTx take.
//
// Visibility (decision U28): the production Authorizer reads grants through
// its own pool, so the grant this call inserts is invisible to it until tx
// commits. A caller that must authorize against the grant commits this call's
// transaction first, then authorizes in a fresh context, as mod-billing's B6
// does. The method is idempotent, so a later failure leaves only a person and
// a grant that the next attempt reuses.
func (s *PersonalPartyService) EnsurePersonalParty(ctx context.Context, tx pgx.Tx) (PersonalParty, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return PersonalParty{}, err
	}
	cq := s.coreQuerier(tx)
	uq := s.usersQ(tx)

	// The acting profile must be held by the acting principal and be a
	// personal profile (no party). Checked before the lock: both facts are
	// immutable for a profile.
	profile, err := cq.GetProfileByEntityID(ctx, ac.ProfileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalParty{}, ErrPersonalProfileRequired
	}
	if err != nil {
		return PersonalParty{}, fmt.Errorf("users: personal party: load acting profile: %w", err)
	}
	if profile.HolderID != ac.PrincipalID || profile.PartyID.Valid {
		return PersonalParty{}, ErrPersonalProfileRequired
	}

	// Serialize per user. The row is re-read under the lock, so a call that
	// waited sees the winner's person link.
	user, err := uq.GetUserByEntityIDForUpdate(ctx, ac.PrincipalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalParty{}, ErrPersonalProfileRequired
	}
	if err != nil {
		return PersonalParty{}, fmt.Errorf("users: personal party: lock user: %w", err)
	}
	if user.IsGuest {
		return PersonalParty{}, ErrGuestMustUpgrade
	}
	if user.ManagingPartyID.Valid {
		return PersonalParty{}, ErrPersonalProfileRequired
	}

	created := false
	var personID int64
	var personUUID uuid.UUID
	if user.PersonID.Valid {
		personID = user.PersonID.Int64
		ent, err := cq.GetEntityByID(ctx, personID)
		if errors.Is(err, pgx.ErrNoRows) {
			return PersonalParty{}, ErrPersonalPartyNotOwned
		}
		if err != nil {
			return PersonalParty{}, fmt.Errorf("users: personal party: load person: %w", err)
		}
		if !ent.OwnerID.Valid || ent.OwnerID.Int64 != user.EntityID {
			return PersonalParty{}, ErrPersonalPartyNotOwned
		}
		personUUID = ent.Uuid
	} else {
		personID, personUUID, err = s.createPerson(ctx, tx, cq, user.EntityID, ac.ProfileID)
		if err != nil {
			return PersonalParty{}, err
		}
		created = true
		linked, err := uq.SetUserPerson(ctx, usersdb.SetUserPersonParams{
			EntityID: user.EntityID,
			PersonID: pgtype.Int8{Int64: personID, Valid: true},
		})
		if err != nil {
			return PersonalParty{}, fmt.Errorf("users: personal party: link person: %w", err)
		}
		userEnt, err := cq.GetEntityByID(ctx, user.EntityID)
		if err != nil {
			return PersonalParty{}, fmt.Errorf("users: personal party: load user entity: %w", err)
		}
		after := userAudit(linked, userEnt.Uuid)
		after["person_uuid"] = personUUID.String()
		if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceUser, user.EntityID,
			userAudit(user, userEnt.Uuid), after); err != nil {
			return PersonalParty{}, err
		}
	}

	grantID, grantCreated, err := s.ensureGrant(ctx, tx, ac.ProfileID, personID, personUUID)
	if err != nil {
		return PersonalParty{}, err
	}
	return PersonalParty{
		PartyID: personID, PartyUUID: personUUID, GrantID: grantID, Created: created || grantCreated,
	}, nil
}

// createPerson inserts the user's own natural_person (entity, legal entity and
// natural person rows; no name is known here, so the names stay NULL) owned by
// the user, created by the acting profile, and observes it as `create`
// `natural_person`. mod-core's CreateEntity does not observe.
func (s *PersonalPartyService) createPerson(ctx context.Context, tx pgx.Tx, cq coredb.Querier, userID, profileID int64) (int64, uuid.UUID, error) {
	typ, err := cq.GetTypeBySlug(ctx, naturalPersonTypeSlug)
	if err != nil {
		return 0, uuid.Nil, fmt.Errorf("users: personal party: resolve natural_person type: %w", err)
	}
	ent, err := coreservice.CreateEntity(ctx, cq, typ.ID, coreservice.WithOwner(userID), coreservice.WithCreatedBy(profileID))
	if err != nil {
		return 0, uuid.Nil, fmt.Errorf("users: personal party: create person entity: %w", err)
	}
	if _, err := cq.CreateLegalEntity(ctx, ent.ID); err != nil {
		return 0, uuid.Nil, fmt.Errorf("users: personal party: create legal entity: %w", err)
	}
	if _, err := cq.CreateNaturalPerson(ctx, coredb.CreateNaturalPersonParams{EntityID: ent.ID}); err != nil {
		return 0, uuid.Nil, fmt.Errorf("users: personal party: create natural person: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceNaturalPerson, ent.ID, nil, map[string]any{
		"uuid": ent.Uuid.String(),
	}); err != nil {
		return 0, uuid.Nil, err
	}
	return ent.ID, ent.Uuid, nil
}

// ensureGrant returns the personal profile's owned-scope `manage` grant on the
// person, inserting it (and observing `grant` `grant`) when none equal exists.
func (s *PersonalPartyService) ensureGrant(ctx context.Context, tx pgx.Tx, profileID, personID int64, personUUID uuid.UUID) (int64, bool, error) {
	aq := s.authzQ(tx)
	op, err := aq.GetOperationBySlug(ctx, manageOperationSlug)
	if err != nil {
		return 0, false, fmt.Errorf("users: personal party: resolve manage operation: %w", err)
	}
	found, err := aq.SearchGrants(ctx, authzdb.SearchGrantsParams{
		ActorID:     pgtype.Int8{Int64: profileID, Valid: true},
		TargetID:    pgtype.Int8{Int64: personID, Valid: true},
		OperationID: pgtype.Int4{Int32: op.ID, Valid: true},
		Scope:       pgtype.Text{String: ownedScope, Valid: true},
		Limit:       searchGrantsLimit,
	})
	if err != nil {
		return 0, false, fmt.Errorf("users: personal party: look up grant: %w", err)
	}
	for _, g := range found {
		if !g.TargetTypeID.Valid && !g.IncludeSubtypes {
			return g.ID, false, nil
		}
	}

	grant, err := aq.CreateGrant(ctx, authzdb.CreateGrantParams{
		ActorID:         profileID,
		OperationID:     op.ID,
		TargetID:        pgtype.Int8{Int64: personID, Valid: true},
		GrantedBy:       pgtype.Int8{Int64: profileID, Valid: true},
		Scope:           pgtype.Text{String: ownedScope, Valid: true},
		TargetTypeID:    pgtype.Int8{},
		IncludeSubtypes: false,
	})
	if err != nil {
		return 0, false, fmt.Errorf("users: personal party: create owned manage grant: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opGrant, resourceGrant, personID, nil, map[string]any{
		"id":          grant.ID,
		"operation":   manageOperationSlug,
		"scope":       ownedScope,
		"target_uuid": personUUID.String(),
		"wildcard":    false,
	}); err != nil {
		return 0, false, err
	}
	return grant.ID, true, nil
}

const (
	// ownedScope is mod-authz's grants.scope value for an owned-scope grant.
	ownedScope = "owned"
	// searchGrantsLimit bounds the equal-grant lookup; a personal profile
	// holds a handful of owned grants on its own person at most.
	searchGrantsLimit = 1000
)
