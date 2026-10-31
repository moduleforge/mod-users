package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Profile kinds of a ProfileView.
const (
	ProfileKindPersonal = "personal"
	ProfileKindParty    = "party"
)

// Lock remedy kinds a ProfileView may suggest.
const (
	RemedySignInWithProvider             = "sign_in_with_provider"
	RemedyStepUp                         = "step_up"
	RemedySignInWithAuthorizedCredential = "sign_in_with_authorized_credential"
	RemedyAcceptInvitation               = "accept_invitation"
)

// availabilityAvailable is profile_availability's success value.
const availabilityAvailable = "available"

// PartyView is the party of a party profile in a ProfileView.
type PartyView struct {
	UUID        uuid.UUID `json:"uuid"`
	Kind        string    `json:"kind"`
	DisplayName string    `json:"display_name"`
}

// LockRemedy tells the client how a locked profile can be unlocked.
type LockRemedy struct {
	Kind       string `json:"kind"`
	ProviderID string `json:"provider_id,omitempty"`
}

// ProfileLock is why a profile is not available to the session, with an
// optional remedy.
type ProfileLock struct {
	Reason string      `json:"reason"`
	Remedy *LockRemedy `json:"remedy"`
}

// ProfileView is the client view of a profile (API surface section 7, binding
// for GUIs): its uuid, kind, party, status, display name and whether the
// session (or credential) may act as it, with the lock reason and remedy when
// it may not. Internal ids are unexported and never serialized.
type ProfileView struct {
	UUID        uuid.UUID    `json:"uuid"`
	Kind        string       `json:"kind"`
	Party       *PartyView   `json:"party"`
	Status      string       `json:"status"`
	DisplayName string       `json:"display_name"`
	Available   bool         `json:"available"`
	Lock        *ProfileLock `json:"lock"`

	id        int64
	partyID   int64
	createdAt time.Time
}

// ID returns the profile's internal entity id (never serialized).
func (v ProfileView) ID() int64 { return v.id }

// PartyID returns the party's internal entity id, 0 for a personal profile.
func (v ProfileView) PartyID() int64 { return v.partyID }

// profileViewsSQL lists the non-ended profiles of a principal, personal first
// and then oldest first, with everything a ProfileView needs and the
// availability of each profile in one round trip. Availability is
// profile_availability over the session ($2) when $2 > 0, otherwise
// credential_profile_availability over the credential ($3). $4 > 0 restricts
// the list to that profile.
//
// The holder's display name is the name of the user's person record, else the
// primary email, else "Guest" for a guest; a service account's is its label
// (client credentials). A party's display name is the
// organization's legal name or the person party's name.
const profileViewsSQL = `
WITH holder AS (
  SELECT COALESCE(
           NULLIF(btrim(concat_ws(' ', np.given_name, np.family_name)), ''),
           (SELECT i.value_canon FROM mod_users.user_identifiers i
             WHERE i.user_id = u.entity_id AND i.kind = 'email' AND i.is_primary
             LIMIT 1),
           CASE WHEN u.is_guest THEN 'Guest' END,
           '') AS name
    FROM mod_users.users u
    LEFT JOIN public.natural_persons np ON np.entity_id = u.person_id
   WHERE u.entity_id = $1
)
SELECT p.entity_id,
       pe.uuid,
       p.status,
       p.created_at,
       p.party_id,
       party_e.uuid,
       COALESCE(party_t.slug, ''),
       COALESCE(org.legal_name,
                NULLIF(btrim(concat_ws(' ', pnp.given_name, pnp.family_name)), ''),
                ''),
       COALESCE((SELECT h.name FROM holder h),
                (SELECT sa.label FROM public.service_accounts sa WHERE sa.entity_id = $1),
                ''),
       CASE WHEN $2::bigint > 0
            THEN mod_users.profile_availability($2::bigint, p.entity_id)
            ELSE mod_users.credential_profile_availability($3::bigint, p.entity_id)
       END,
       COALESCE(pol.required_oidc_provider_id, '')
  FROM public.profiles p
  JOIN public.entities pe ON pe.id = p.entity_id
  LEFT JOIN public.entities party_e ON party_e.id = p.party_id
  LEFT JOIN public.types party_t ON party_t.id = party_e.fundamental_type_id
  LEFT JOIN public.organizations org ON org.entity_id = p.party_id
  LEFT JOIN public.natural_persons pnp ON pnp.entity_id = p.party_id
  LEFT JOIN mod_users.party_authn_policies pol ON pol.party_id = p.party_id
 WHERE p.holder_id = $1
   AND p.status <> 'ended'
   AND ($4::bigint = 0 OR p.entity_id = $4::bigint)
 ORDER BY (p.party_id IS NOT NULL), p.created_at, p.entity_id`

// viewScope selects how loadProfileViews evaluates availability: over a
// session's factors (sessionID) or over one credential (credentialID).
type viewScope struct {
	principalID  int64
	sessionID    int64
	credentialID int64
	onlyProfile  int64
}

// appGate reports whether profileID may sign in to the session's app. A nil
// gate admits every profile.
type appGate func(ctx context.Context, profileID int64) (bool, error)

// loadProfileViews runs profileViewsSQL on db and builds the views. An
// available profile the app gate refuses is reported unavailable with lock
// reason app_login_not_granted.
func loadProfileViews(ctx context.Context, db usersdb.DBTX, scope viewScope, gate appGate) ([]ProfileView, error) {
	rows, err := db.Query(ctx, profileViewsSQL, scope.principalID, scope.sessionID, scope.credentialID, scope.onlyProfile)
	if err != nil {
		return nil, fmt.Errorf("sessions: list profiles: %w", err)
	}
	defer rows.Close()

	var views []ProfileView
	for rows.Next() {
		var (
			v                          ProfileView
			createdAt                  pgtype.Timestamptz
			partyID                    pgtype.Int8
			partyUUID                  pgtype.UUID
			partyKind, partyName, name string
			availability, provider     string
		)
		if err := rows.Scan(&v.id, &v.UUID, &v.Status, &createdAt, &partyID, &partyUUID,
			&partyKind, &partyName, &name, &availability, &provider); err != nil {
			return nil, fmt.Errorf("sessions: scan profile: %w", err)
		}
		v.createdAt = createdAt.Time
		if partyID.Valid {
			v.partyID = partyID.Int64
			v.Kind = ProfileKindParty
			v.Party = &PartyView{UUID: uuid.UUID(partyUUID.Bytes), Kind: partyKind, DisplayName: partyName}
			v.DisplayName = name + " @ " + partyName
		} else {
			v.Kind = ProfileKindPersonal
			v.DisplayName = name + " (Personal)"
		}
		v.Available = availability == availabilityAvailable
		if !v.Available {
			v.Lock = lockFor(availability, v.Status, provider)
		}
		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessions: list profiles: %w", err)
	}

	if gate != nil {
		for i := range views {
			if !views[i].Available {
				continue
			}
			ok, err := gate(ctx, views[i].id)
			if err != nil {
				return nil, err
			}
			if !ok {
				views[i].Available = false
				views[i].Lock = &ProfileLock{Reason: LockAppLoginNotGranted}
			}
		}
	}
	if views == nil {
		views = []ProfileView{}
	}
	return views, nil
}

// lockFor builds the lock of an unavailable profile from its availability
// reason, adding the remedy the client can act on.
func lockFor(reason, status, providerID string) *ProfileLock {
	lock := &ProfileLock{Reason: reason}
	switch {
	case reason == "provider_required" && providerID != "":
		lock.Remedy = &LockRemedy{Kind: RemedySignInWithProvider, ProviderID: providerID}
	case reason == "acr_too_low":
		lock.Remedy = &LockRemedy{Kind: RemedyStepUp}
	case reason == "credential_not_authorized":
		lock.Remedy = &LockRemedy{Kind: RemedySignInWithAuthorizedCredential}
	case reason == "inactive" && status == "invited":
		lock.Remedy = &LockRemedy{Kind: RemedyAcceptInvitation}
	}
	return lock
}

// findView returns the view with the given uuid.
func findView(views []ProfileView, id uuid.UUID) (ProfileView, bool) {
	for _, v := range views {
		if v.UUID == id {
			return v, true
		}
	}
	return ProfileView{}, false
}

// findViewByID returns the view of the profile with the given entity id.
func findViewByID(views []ProfileView, id int64) (ProfileView, bool) {
	if id == 0 {
		return ProfileView{}, false
	}
	for _, v := range views {
		if v.id == id {
			return v, true
		}
	}
	return ProfileView{}, false
}

// unavailableError is the refusal for acting as v: the app gate's 403, or the
// 401 users.profile_unavailable with v's lock reason.
func unavailableError(v ProfileView) error {
	if v.Lock != nil && v.Lock.Reason == LockAppLoginNotGranted {
		return ErrAppLoginNotGranted
	}
	reason := "not_held"
	if v.Lock != nil {
		reason = v.Lock.Reason
	}
	return ProfileUnavailable(reason)
}
