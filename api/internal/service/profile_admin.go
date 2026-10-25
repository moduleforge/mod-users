package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// This file holds the party-administration methods behind
// /v1/parties/{party_uuid}/profiles, /v1/parties/{party_uuid}/managed-users and
// PATCH /v1/profiles/{uuid}.
//
// Authority. A party-scoped route first checks that the caller may see the
// party's profiles at all: AuthorizeTypeIn(read, profile, party). A caller that
// may not gets apiresp.ErrNotFound, indistinguishable from an unknown party
// (masking). It is one check for the whole party-scoped group, deliberately the
// type-in-party form and not a read of the party entity itself: an owned-scope
// grant never confers authority over the owner's own record. The operation then
// needs its own authority, returned as a 403: AuthorizeTypeIn(create, profile,
// party) for an invitation (and create on the user type for a managed user),
// `update` on the profile entity for a status change. The status change adds the
// freshness gate (409 users.step_up_required) with the same mechanism and
// setting as the principal disable route. The order is 401, 400, 404, 403,
// 409.

// opSlugCreate is the create operation.
const opSlugCreate = "create"

// ---------------------------------------------------------------------------
// masking
// ---------------------------------------------------------------------------

// maskForbidden reports a plain refusal as apiresp.ErrNotFound, so a resource
// the caller may not read is indistinguishable from one that does not exist.
// An action-required refusal (a step-up) and every other error are returned as
// is.
func maskForbidden(err error) error {
	if err == nil {
		return nil
	}
	var action *apiresp.ActionRequiredError
	if errors.As(err, &action) {
		return err
	}
	if errors.Is(err, apiresp.ErrForbidden) {
		return apiresp.ErrNotFound
	}
	return err
}

// authorizeMaskedEntity authorizes op on the entity and reports a plain refusal
// as apiresp.ErrNotFound. An absent authorizer fails closed (apiresp.ErrForbidden).
func authorizeMaskedEntity(ctx context.Context, az coreAuthz.Authorizer, op string, target int64) error {
	if az == nil {
		return apiresp.ErrForbidden
	}
	return maskForbidden(az.Authorize(ctx, op, &target))
}

// ---------------------------------------------------------------------------
// views
// ---------------------------------------------------------------------------

// ProfileHolderView is the holder of a party profile in a PartyProfileView.
type ProfileHolderView struct {
	// UUID is the holder principal's uuid.
	UUID        uuid.UUID `json:"uuid"`
	DisplayName string    `json:"display_name"`
	// Managed is true for a managed user of the party.
	Managed bool `json:"managed"`
}

// PartyProfileView is the administration view of a party profile (the party
// profile routes and PATCH /v1/profiles/{uuid}). It has the identity fields of
// the section 7 ProfileView (uuid, kind, party, status, display_name) and
// leaves out `available` and `lock`, which describe a profile relative to one
// holder session and mean nothing to an administrator. Internal ids are never
// serialized.
type PartyProfileView struct {
	UUID        uuid.UUID         `json:"uuid"`
	Kind        string            `json:"kind"`
	Party       PartyView         `json:"party"`
	Status      string            `json:"status"`
	DisplayName string            `json:"display_name"`
	Holder      ProfileHolderView `json:"holder"`
	Source      string            `json:"source"`
	// InvitedByProfileUUID is the profile that invited the holder, null when
	// none did.
	InvitedByProfileUUID *uuid.UUID `json:"invited_by_profile_uuid"`
	StartedAt            *time.Time `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at"`
	EndReason            *string    `json:"end_reason"`
	CreatedAt            time.Time  `json:"created_at"`

	id int64
}

// PartyProfilePage is one page of a party's profiles.
type PartyProfilePage struct {
	Profiles []PartyProfileView `json:"profiles"`
	Total    int64              `json:"total"`
	Limit    int32              `json:"limit"`
	Offset   int32              `json:"offset"`
}

// partyProfileFilter selects the rows of partyProfilesSQL.
type partyProfileFilter struct {
	partyID   int64
	profileID int64  // 0: every profile of the party
	status    string // "": every status except ended; statusAny: every status
}

// statusAny is the partyProfileFilter status that matches every status.
const statusAny = "*"

// partyProfilesFromSQL is the shared FROM/WHERE of the party profile list and
// count. $1 party, $2 profile (0 for all), $3 status (” for the profiles that
// are not ended, '*' for every status).
// The aliases u, np and pe are the ones userDisplayNameSQL expects.
const partyProfilesFromSQL = `
  FROM public.profiles p
  JOIN public.entities pre ON pre.id = p.entity_id
  JOIN public.entities he ON he.id = p.holder_id
  JOIN public.entities party_e ON party_e.id = p.party_id
  JOIN public.types party_t ON party_t.id = party_e.fundamental_type_id
  LEFT JOIN public.organizations org ON org.entity_id = p.party_id
  LEFT JOIN public.natural_persons pnp ON pnp.entity_id = p.party_id
  LEFT JOIN mod_users.users u ON u.entity_id = p.holder_id
  LEFT JOIN public.natural_persons np ON np.entity_id = u.person_id
  LEFT JOIN mod_users.user_identifiers pe ON pe.user_id = u.entity_id AND pe.kind = 'email' AND pe.is_primary
  LEFT JOIN public.entities ibe ON ibe.id = p.invited_by_profile_id
 WHERE p.party_id = $1
   AND ($2::bigint = 0 OR p.entity_id = $2::bigint)
   AND (($3::text = '' AND p.status <> 'ended') OR $3::text = '*' OR p.status = $3::text)`

const partyProfilesSQL = `
SELECT p.entity_id, pre.uuid, p.status, p.source, p.started_at, p.ended_at, p.end_reason, p.created_at,
       he.uuid,
       ` + userDisplayNameSQL + `,
       u.managing_party_id IS NOT NULL,
       party_e.uuid, party_t.slug,
       COALESCE(org.legal_name, NULLIF(btrim(concat_ws(' ', pnp.given_name, pnp.family_name)), ''), ''),
       ibe.uuid` + partyProfilesFromSQL + `
 ORDER BY p.created_at, p.entity_id
 LIMIT $4 OFFSET $5`

const partyProfilesCountSQL = `SELECT count(*)` + partyProfilesFromSQL

// loadPartyProfileViews reads the views selected by f, oldest first.
func loadPartyProfileViews(ctx context.Context, db usersdb.DBTX, f partyProfileFilter, limit, offset int32) ([]PartyProfileView, error) {
	rows, err := db.Query(ctx, partyProfilesSQL, f.partyID, f.profileID, f.status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("users: list party profiles: %w", err)
	}
	defer rows.Close()
	out := make([]PartyProfileView, 0, limit)
	for rows.Next() {
		var (
			v                                PartyProfileView
			createdAt                        pgtype.Timestamptz
			endReason                        pgtype.Text
			holderName, partyKind, partyName string
			holderManaged                    bool
			holderUUID, partyUUID            uuid.UUID
			invitedBy                        pgtype.UUID
		)
		if err := rows.Scan(&v.id, &v.UUID, &v.Status, &v.Source, &v.StartedAt, &v.EndedAt, &endReason, &createdAt,
			&holderUUID, &holderName, &holderManaged, &partyUUID, &partyKind, &partyName, &invitedBy); err != nil {
			return nil, fmt.Errorf("users: scan party profile: %w", err)
		}
		v.Kind = ProfileKindParty
		v.Party = PartyView{UUID: partyUUID, Kind: partyKind, DisplayName: partyName}
		v.DisplayName = profileDisplayName(holderName, true, partyName)
		v.Holder = ProfileHolderView{UUID: holderUUID, DisplayName: holderName, Managed: holderManaged}
		v.EndReason = optionalText(endReason)
		v.InvitedByProfileUUID = optionalUUID(invitedBy)
		v.CreatedAt = createdAt.Time
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("users: list party profiles: %w", err)
	}
	return out, nil
}

// loadPartyProfileView reads one profile of the party, whatever its status; a
// missing one is apiresp.ErrNotFound.
func loadPartyProfileView(ctx context.Context, db usersdb.DBTX, partyID, profileID int64) (PartyProfileView, error) {
	views, err := loadPartyProfileViews(ctx, db, partyProfileFilter{partyID: partyID, profileID: profileID, status: statusAny}, 1, 0)
	if err != nil {
		return PartyProfileView{}, err
	}
	if len(views) == 0 {
		return PartyProfileView{}, apiresp.ErrNotFound
	}
	return views[0], nil
}

// ---------------------------------------------------------------------------
// the party-scoped gate
// ---------------------------------------------------------------------------

// readableParty resolves a party uuid for a party-scoped route. An unknown
// uuid, an archived entity, an entity that is not a legal entity and a party
// whose profiles the caller may not read are all apiresp.ErrNotFound.
func (s *ProfileService) readableParty(ctx context.Context, partyUUID uuid.UUID) (int64, error) {
	return readablePartyID(ctx, s.coreQ, s.az, partyUUID)
}

// lookupParty resolves a party uuid to the id of a live legal entity, with no
// authorization. An unknown uuid, an archived entity and an entity that is not
// a legal entity are apiresp.ErrNotFound.
func lookupParty(ctx context.Context, coreQ coredb.Querier, partyUUID uuid.UUID) (int64, error) {
	ent, err := coreQ.GetEntityByUUID(ctx, partyUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, apiresp.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("users: load party: %w", err)
	}
	if ent.ArchivedAt != nil {
		return 0, apiresp.ErrNotFound
	}
	if _, err := coreQ.GetLegalEntityByEntityID(ctx, ent.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, apiresp.ErrNotFound
		}
		return 0, fmt.Errorf("users: load party: %w", err)
	}
	return ent.ID, nil
}

// partyReadable reports whether the caller may read the profiles of the party
// (AuthorizeTypeIn(read, profile, party)). A refusal is (false, nil); any other
// failure is returned.
func partyReadable(ctx context.Context, coreQ coredb.Querier, az coreAuthz.Authorizer, partyID int64) (bool, error) {
	profileType, err := coreQ.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return false, fmt.Errorf("users: resolve profile type: %w", err)
	}
	err = coreAuthz.AuthorizeTypeIn(ctx, az, opSlugRead, profileType.ID, &partyID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, apiresp.ErrForbidden):
		return false, nil
	}
	return false, err
}

// readablePartyID is lookupParty followed by the party-scoped read gate: a
// party whose profiles the caller may not read is apiresp.ErrNotFound.
func readablePartyID(ctx context.Context, coreQ coredb.Querier, az coreAuthz.Authorizer, partyUUID uuid.UUID) (int64, error) {
	party, err := lookupParty(ctx, coreQ, partyUUID)
	if err != nil {
		return 0, err
	}
	profileType, err := coreQ.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return 0, fmt.Errorf("users: resolve profile type: %w", err)
	}
	if err := maskForbidden(coreAuthz.AuthorizeTypeIn(ctx, az, opSlugRead, profileType.ID, &party)); err != nil {
		return 0, err
	}
	return party, nil
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

// ListPartyProfilesInput is the input of ListPartyProfiles.
type ListPartyProfilesInput struct {
	// Status restricts the list to one status; empty lists the profiles that
	// are not ended.
	Status string
	Limit  int32
	Offset int32
}

var profileStatuses = map[string]bool{
	ProfileStatusInvited: true, ProfileStatusActive: true, ProfileStatusSuspended: true, ProfileStatusEnded: true,
}

// ListPartyProfiles lists the profiles of a party, oldest first
// (GET /v1/parties/{party_uuid}/profiles). It needs
// AuthorizeTypeIn(read, profile, party); a caller without it gets
// apiresp.ErrNotFound, like an unknown party.
func (s *ProfileService) ListPartyProfiles(ctx context.Context, partyUUID uuid.UUID, in ListPartyProfilesInput) (PartyProfilePage, error) {
	if _, err := adminCaller(ctx); err != nil {
		return PartyProfilePage{}, err
	}
	if in.Status != "" && !profileStatuses[in.Status] {
		return PartyProfilePage{}, invalidField("status", "users.profile_status_invalid", "status must be invited, active, suspended or ended")
	}
	partyID, err := s.readableParty(ctx, partyUUID)
	if err != nil {
		return PartyProfilePage{}, err
	}
	limit, offset := coreservice.Pagination{Limit: in.Limit, Offset: in.Offset}.Normalize()
	f := partyProfileFilter{partyID: partyID, status: in.Status}
	var total int64
	if err := s.pool.QueryRow(ctx, partyProfilesCountSQL, partyID, int64(0), in.Status).Scan(&total); err != nil {
		return PartyProfilePage{}, fmt.Errorf("users: count party profiles: %w", err)
	}
	views, err := loadPartyProfileViews(ctx, s.pool, f, limit, offset)
	if err != nil {
		return PartyProfilePage{}, err
	}
	return PartyProfilePage{Profiles: views, Total: total, Limit: limit, Offset: offset}, nil
}

// ---------------------------------------------------------------------------
// invite
// ---------------------------------------------------------------------------

// InviteProfileInput is the input of InviteProfile. Exactly one of HolderEmail
// and HolderUserUUID names the holder.
type InviteProfileInput struct {
	HolderEmail           string
	HolderUserUUID        uuid.UUID
	ConfirmPolicyMismatch bool
}

// InviteProfile invites a user to the party (POST
// /v1/parties/{party_uuid}/profiles) and returns the new `invited` profile.
// See InviteInTx for the rules and errors; the caller's party-profile read
// check (404) precedes the create check (403).
func (s *ProfileService) InviteProfile(ctx context.Context, partyUUID uuid.UUID, in InviteProfileInput) (PartyProfileView, error) {
	if _, err := adminCaller(ctx); err != nil {
		return PartyProfileView{}, err
	}
	if _, err := validateInvite(InviteInput{PartyID: 1, HolderEmail: in.HolderEmail}, in.HolderUserUUID); err != nil {
		return PartyProfileView{}, err
	}
	partyID, err := s.readableParty(ctx, partyUUID)
	if err != nil {
		return PartyProfileView{}, err
	}
	var view PartyProfileView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		prof, err := s.invite(ctx, tx, inviteRequest{
			InviteInput: InviteInput{
				PartyID: partyID, HolderEmail: in.HolderEmail, ConfirmPolicyMismatch: in.ConfirmPolicyMismatch,
			},
			HolderUserUUID: in.HolderUserUUID,
		})
		if err != nil {
			return err
		}
		view, err = loadPartyProfileView(ctx, tx, partyID, prof.ID)
		return err
	})
	if err != nil {
		return PartyProfileView{}, err
	}
	return view, nil
}

// ---------------------------------------------------------------------------
// managed users
// ---------------------------------------------------------------------------

// ManagedUserView is the result of creating a managed user: the user and its
// active party profile.
type ManagedUserView struct {
	User    PrincipalView    `json:"user"`
	Profile PartyProfileView `json:"profile"`
}

// CreateManagedUser creates a managed user of the party (POST
// /v1/parties/{party_uuid}/managed-users). See ProvisionManagedUserInTx for the
// rules, credential and errors.
func (s *ProfileService) CreateManagedUser(ctx context.Context, partyUUID uuid.UUID, email, password string) (ManagedUserView, error) {
	if _, err := adminCaller(ctx); err != nil {
		return ManagedUserView{}, err
	}
	if err := validateEmail(CanonicalizeEmail(email)); err != nil {
		return ManagedUserView{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return ManagedUserView{}, err
	}
	partyID, err := s.readableParty(ctx, partyUUID)
	if err != nil {
		return ManagedUserView{}, err
	}
	var view ManagedUserView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		out, err := s.ProvisionManagedUserInTx(ctx, tx, ManagedUserInput{PartyID: partyID, Email: email, Password: password})
		if err != nil {
			return err
		}
		user, err := s.users.principalView(ctx, tx, out.User.User.EntityID)
		if err != nil {
			return err
		}
		profile, err := loadPartyProfileView(ctx, tx, partyID, out.Profile.ID)
		if err != nil {
			return err
		}
		view = ManagedUserView{User: user, Profile: profile}
		return nil
	})
	if err != nil {
		return ManagedUserView{}, err
	}
	return view, nil
}

// ---------------------------------------------------------------------------
// status change
// ---------------------------------------------------------------------------

// UpdateProfileStatusInput is the input of UpdateProfileStatus.
type UpdateProfileStatusInput struct {
	// Status is the target: active (resume), suspended or ended.
	Status string
	// EndReason is required with ended and refused otherwise.
	EndReason string
}

// validateStatusUpdate checks the shape of a status change and returns the
// target and the trimmed end reason.
func validateStatusUpdate(in UpdateProfileStatusInput) (to, reason string, err error) {
	switch in.Status {
	case ProfileStatusActive, ProfileStatusSuspended:
		if in.EndReason != "" {
			return "", "", invalidField("end_reason", "users.end_reason_unexpected", "end_reason applies only when ending a profile")
		}
		return in.Status, "", nil
	case ProfileStatusEnded:
		reason, err := validateEndReason(in.EndReason)
		if err != nil {
			return "", "", err
		}
		return in.Status, reason, nil
	case "":
		return "", "", invalidField("status", "users.status_required", "status is required")
	}
	return "", "", invalidField("status", "users.profile_status_invalid", "status must be active, suspended or ended")
}

// adminMayChange reports whether an administrator may move a profile from one
// status to another: the graph's edges except accepting an invitation, which is
// the holder's alone.
func adminMayChange(from, to string) bool {
	return transitionAllowed(from, to) && !(from == ProfileStatusInvited && to == ProfileStatusActive)
}

// partyProfileByUUID loads a party profile by uuid; an unknown uuid and a
// personal profile are apiresp.ErrNotFound.
func (s *ProfileService) partyProfileByUUID(ctx context.Context, id uuid.UUID) (coredb.Profile, error) {
	p, err := s.coreQ.GetProfileByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return coredb.Profile{}, apiresp.ErrNotFound
	}
	if err != nil {
		return coredb.Profile{}, fmt.Errorf("users: load profile: %w", err)
	}
	if !p.PartyID.Valid {
		return coredb.Profile{}, apiresp.ErrNotFound
	}
	return p, nil
}

// UpdateProfileStatus suspends, resumes or ends a party profile (PATCH
// /v1/profiles/{uuid}) and returns the updated profile. Ending runs the
// departure cleanup of EndInTx.
//
// Authority: `read` on the profile (a refusal is apiresp.ErrNotFound), then
// `update` (403), then the freshness gate (409 users.step_up_required). A
// status the profile cannot move to from its current one, including accepting
// an invitation on the holder's behalf, is ErrProfileTransition (409).
func (s *ProfileService) UpdateProfileStatus(ctx context.Context, profileUUID uuid.UUID, in UpdateProfileStatusInput) (PartyProfileView, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return PartyProfileView{}, err
	}
	to, reason, err := validateStatusUpdate(in)
	if err != nil {
		return PartyProfileView{}, err
	}
	p, err := s.partyProfileByUUID(ctx, profileUUID)
	if err != nil {
		return PartyProfileView{}, err
	}
	if err := authorizeMaskedEntity(ctx, s.az, opSlugRead, p.EntityID); err != nil {
		return PartyProfileView{}, err
	}
	if s.az == nil {
		return PartyProfileView{}, apiresp.ErrForbidden
	}
	if err := s.az.Authorize(ctx, opSlugUpdate, &p.EntityID); err != nil {
		return PartyProfileView{}, err
	}
	if err := s.users.requireFresh(ac); err != nil {
		return PartyProfileView{}, err
	}

	var view PartyProfileView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		locked, err := lockProfile(ctx, tx, p.EntityID)
		if err != nil {
			return err
		}
		if !adminMayChange(locked.Status, to) {
			return ErrProfileTransition
		}
		if to == ProfileStatusEnded {
			err = s.endLocked(ctx, tx, locked, reason)
		} else {
			_, err = s.transitionLocked(ctx, tx, locked, to, "")
		}
		if err != nil {
			return err
		}
		view, err = loadPartyProfileView(ctx, tx, locked.PartyID.Int64, locked.EntityID)
		return err
	})
	if err != nil {
		return PartyProfileView{}, err
	}
	return view, nil
}
