package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Principal kinds a PrincipalSummary reports.
const (
	PrincipalKindUser           = "user"
	PrincipalKindServiceAccount = "service_account"
	PrincipalKindSystemActor    = "system_actor"
	// principalKindOther is the kind of a principal that is none of the
	// above; it cannot occur with the current schema.
	principalKindOther = "principal"
)

// PrincipalSummary is the display-oriented read of one principal (API surface
// section 6.3, binding). DisplayName is a user's person name, else primary
// email, else "Guest"; a service account's label; a system actor's name.
// PrimaryEmail is empty for a principal without a primary email.
type PrincipalSummary struct {
	PrincipalID   int64
	PrincipalUUID uuid.UUID
	Kind          string
	DisplayName   string
	PrimaryEmail  string
	Disabled      bool
	IsGuest       bool
}

// ProfileSummary is the display-oriented read of one profile (API surface
// section 6.3, binding). PartyID and PartyUUID are zero for a personal
// profile. DisplayName is "<holder name> (Personal)" or
// "<holder name> @ <party name>" (decision U19).
type ProfileSummary struct {
	ProfileID           int64
	ProfileUUID         uuid.UUID
	HolderPrincipalID   int64
	HolderPrincipalUUID uuid.UUID
	PartyID             int64
	PartyUUID           uuid.UUID
	Status              string
	DisplayName         string
	HolderDisplayName   string
	HolderPrimaryEmail  string
}

// DirectoryService answers batch display lookups for other modules. It does no
// authorization: callers authorize first.
type DirectoryService struct {
	db usersdb.DBTX
}

// NewDirectoryService constructs a DirectoryService over the pool.
func NewDirectoryService(pool *pgxpool.Pool) *DirectoryService {
	return &DirectoryService{db: pool}
}

// PrincipalSummaries returns the summaries of principalIDs keyed by id. Unknown
// ids are absent from the map. It delegates to ReadPrincipalSummaries with the
// service's pool.
func (d *DirectoryService) PrincipalSummaries(ctx context.Context, principalIDs []int64) (map[int64]PrincipalSummary, error) {
	return ReadPrincipalSummaries(ctx, d.db, principalIDs)
}

// ProfileSummaries returns the summaries of profileIDs keyed by id. Unknown ids
// are absent from the map.
func (d *DirectoryService) ProfileSummaries(ctx context.Context, profileIDs []int64) (map[int64]ProfileSummary, error) {
	return readProfileSummaries(ctx, d.db, profileIDs)
}

// principalSummariesSQL reads the display fields of the principals in $1. The
// aliases follow userDisplayNameSQL: u, np and pe.
const principalSummariesSQL = `
SELECT e.id,
       e.uuid,
       CASE WHEN u.entity_id IS NOT NULL THEN '` + PrincipalKindUser + `'
            WHEN sa.entity_id IS NOT NULL THEN '` + PrincipalKindServiceAccount + `'
            WHEN sy.entity_id IS NOT NULL THEN '` + PrincipalKindSystemActor + `'
            ELSE '` + principalKindOther + `' END,
       ` + holderDisplayNameSQL + `,
       COALESCE(pe.value_canon, ''),
       pr.disabled_at IS NOT NULL,
       COALESCE(u.is_guest, false)
  FROM public.entities e
  JOIN public.principals pr ON pr.entity_id = e.id
  LEFT JOIN mod_users.users u ON u.entity_id = e.id
  LEFT JOIN public.natural_persons np ON np.entity_id = u.person_id
  LEFT JOIN mod_users.user_identifiers pe ON pe.user_id = u.entity_id AND pe.kind = 'email' AND pe.is_primary
  LEFT JOIN public.service_accounts sa ON sa.entity_id = e.id
  LEFT JOIN public.system_actors sy ON sy.entity_id = e.id
 WHERE e.id = ANY($1::bigint[])`

// holderDisplayNameSQL is the display name of any principal: the user name per
// userDisplayNameSQL, a service account's label, a system actor's name. It
// expects the aliases of principalSummariesSQL (u, np, pe, sa, sy).
const holderDisplayNameSQL = `COALESCE(CASE WHEN u.entity_id IS NOT NULL THEN ` + userDisplayNameSQL +
	` END, sa.label, sy.name, '')`

// ReadPrincipalSummaries returns the summaries of principalIDs keyed by id,
// reading through db: the caller's connection or transaction, never a pool of
// its own (binding, decision U23). A caller that holds a transaction (the
// mod-tokens mint path) passes it and needs no second connection.
//
// It does no authorization. Unknown ids are absent from the map.
func ReadPrincipalSummaries(ctx context.Context, db usersdb.DBTX, principalIDs []int64) (map[int64]PrincipalSummary, error) {
	out := make(map[int64]PrincipalSummary, len(principalIDs))
	if len(principalIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, principalSummariesSQL, principalIDs)
	if err != nil {
		return nil, fmt.Errorf("directory: read principal summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p PrincipalSummary
		if err := rows.Scan(&p.PrincipalID, &p.PrincipalUUID, &p.Kind, &p.DisplayName,
			&p.PrimaryEmail, &p.Disabled, &p.IsGuest); err != nil {
			return nil, fmt.Errorf("directory: scan principal summary: %w", err)
		}
		out[p.PrincipalID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("directory: read principal summaries: %w", err)
	}
	return out, nil
}

// profileSummariesSQL reads the display fields of the profiles in $1.
const profileSummariesSQL = `
SELECT p.entity_id,
       prof_e.uuid,
       p.holder_id,
       holder_e.uuid,
       COALESCE(p.party_id, 0),
       party_e.uuid,
       p.status,
       ` + holderDisplayNameSQL + `,
       COALESCE(pe.value_canon, ''),
       COALESCE(org.legal_name,
                NULLIF(btrim(concat_ws(' ', pnp.given_name, pnp.family_name)), ''),
                '')
  FROM public.profiles p
  JOIN public.entities prof_e ON prof_e.id = p.entity_id
  JOIN public.entities holder_e ON holder_e.id = p.holder_id
  LEFT JOIN public.entities party_e ON party_e.id = p.party_id
  LEFT JOIN public.organizations org ON org.entity_id = p.party_id
  LEFT JOIN public.natural_persons pnp ON pnp.entity_id = p.party_id
  LEFT JOIN mod_users.users u ON u.entity_id = p.holder_id
  LEFT JOIN public.natural_persons np ON np.entity_id = u.person_id
  LEFT JOIN mod_users.user_identifiers pe ON pe.user_id = u.entity_id AND pe.kind = 'email' AND pe.is_primary
  LEFT JOIN public.service_accounts sa ON sa.entity_id = p.holder_id
  LEFT JOIN public.system_actors sy ON sy.entity_id = p.holder_id
 WHERE p.entity_id = ANY($1::bigint[])`

// readProfileSummaries is the query behind DirectoryService.ProfileSummaries
// and the profile display renderer.
func readProfileSummaries(ctx context.Context, db usersdb.DBTX, profileIDs []int64) (map[int64]ProfileSummary, error) {
	out := make(map[int64]ProfileSummary, len(profileIDs))
	if len(profileIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, profileSummariesSQL, profileIDs)
	if err != nil {
		return nil, fmt.Errorf("directory: read profile summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			s         ProfileSummary
			partyUUID *uuid.UUID
			partyName string
		)
		if err := rows.Scan(&s.ProfileID, &s.ProfileUUID, &s.HolderPrincipalID, &s.HolderPrincipalUUID,
			&s.PartyID, &partyUUID, &s.Status, &s.HolderDisplayName, &s.HolderPrimaryEmail, &partyName); err != nil {
			return nil, fmt.Errorf("directory: scan profile summary: %w", err)
		}
		if partyUUID != nil {
			s.PartyUUID = *partyUUID
		}
		s.DisplayName = profileDisplayName(s.HolderDisplayName, s.PartyID != 0, partyName)
		out[s.ProfileID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("directory: read profile summaries: %w", err)
	}
	return out, nil
}

// profileDisplayName renders the profile display string of decision U19.
func profileDisplayName(holderName string, isParty bool, partyName string) string {
	if isParty {
		return holderName + " @ " + partyName
	}
	return holderName + " (Personal)"
}
