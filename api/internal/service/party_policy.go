package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// PartyPolicyService administers how a party gates its profiles: its
// authentication policy (mod_users.party_authn_policies), the credentials
// authorized for it (mod_users.credential_party_authorizations) and the
// credentials confined to it (mod_users.credentials.confined_to_party_id).
// The availability function that consumes these (profile_availability) is
// owned by the baseline migration; this service is the write side.
//
// Authority. The policy and every write use the same two doors:
//
//   - A party administrator: `update` on the party entity itself
//     (Authorize(update, party)). No policy-specific entity type is registered,
//     so the party entity is the resource: whoever may update the party may
//     change how it gates its profiles. GET of the policy and the party-scoped
//     read gate need only AuthorizeTypeIn(read, profile, party), the gate of
//     the party's profile routes, because the policy is the party's published
//     gate; listing the credential authorizations needs `update`, as it
//     exposes members' credential labels.
//   - For an authorization or a confinement only: the credential's own holder
//     acting through a session that already satisfies the party's gate
//     through another factor (holderSatisfiesGate).
//
// Every write adds the freshness gate (409 users.step_up_required) with the
// setting the principal disable route uses. The order is 401, 400, 404, 403,
// 409. A caller refused by both doors gets 404 when it cannot read the party
// and 403 when it can.
type PartyPolicyService struct {
	coreQ coredb.Querier
	az    coreAuthz.Authorizer
	obs   *observer.ObserverGroup
	pool  *pgxpool.Pool
	users *UserService

	newQ func(pgx.Tx) usersdb.Querier
}

// NewPartyPolicyService constructs a PartyPolicyService. users must have been
// wired with UserService.WithSelfService (it supplies the freshness gate).
func NewPartyPolicyService(
	pool *pgxpool.Pool,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
) *PartyPolicyService {
	return &PartyPolicyService{coreQ: coreQ, az: az, obs: obs, pool: pool, users: users}
}

func (s *PartyPolicyService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (s *PartyPolicyService) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if s.pool == nil {
		return errors.New("users: no database pool configured for party policy administration")
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

// Credential rules of a party authn policy (party_authn_policies.credential_rule).
const (
	CredentialRuleAny        = "any"
	CredentialRuleAuthorized = "authorized"
	CredentialRuleProvider   = "provider"

	// DefaultMinACR is the min_acr of a party with no policy row.
	DefaultMinACR = 1
	maxMinACR     = 3

	maxProviderIDLen = 100
)

// Observer resource of a party authn policy.
const resourcePartyAuthnPolicy = "party_authn_policy"

// Error codes owned by party gating administration.
const (
	CodeCredentialConfined = "users.credential_confined"
	CodeProviderUnknown    = "users.oidc_provider_unknown"
)

// ErrCredentialConfinedElsewhere is returned when a confinement is set or
// cleared on a credential that is confined to a different party
// (users.credential_confined, 409).
var ErrCredentialConfinedElsewhere = apiresp.Conflict(apiresp.FieldError{
	Field: "credential", Code: CodeCredentialConfined, Message: "the credential is confined to another party",
})

// ---------------------------------------------------------------------------
// views
// ---------------------------------------------------------------------------

// PartyAuthnPolicyView is a party's effective authentication policy. A party
// with no policy row has IsDefault set, credential_rule `any` and min_acr 1.
type PartyAuthnPolicyView struct {
	PartyUUID              uuid.UUID  `json:"party_uuid"`
	CredentialRule         string     `json:"credential_rule"`
	RequiredOIDCProviderID *string    `json:"required_oidc_provider_id"`
	MinACR                 int16      `json:"min_acr"`
	IsDefault              bool       `json:"is_default"`
	UpdatedAt              *time.Time `json:"updated_at"`
}

// PutPartyAuthnPolicyInput is the input of PutAuthnPolicy. MinACR 0 is
// invalid; the HTTP layer defaults an omitted value to DefaultMinACR.
type PutPartyAuthnPolicyInput struct {
	CredentialRule         string
	RequiredOIDCProviderID string
	MinACR                 int
}

// CredentialAuthorizationView is one credential authorized for a party.
type CredentialAuthorizationView struct {
	CredentialUUID uuid.UUID `json:"credential_uuid"`
	Kind           string    `json:"kind"`
	Label          string    `json:"label"`
	// HolderUUID is the uuid of the principal holding the credential.
	HolderUUID uuid.UUID `json:"holder_uuid"`
	// AuthorizedByProfileUUID is the profile that authorized the credential.
	AuthorizedByProfileUUID uuid.UUID `json:"authorized_by_profile_uuid"`
	AuthorizedAt            time.Time `json:"authorized_at"`
}

// CredentialAuthorizationList is the credentials authorized for a party.
type CredentialAuthorizationList struct {
	Authorizations []CredentialAuthorizationView `json:"authorizations"`
}

// CredentialConfinementView is the result of setting a confinement.
type CredentialConfinementView struct {
	CredentialUUID uuid.UUID `json:"credential_uuid"`
	PartyUUID      uuid.UUID `json:"party_uuid"`
}

func policyView(partyUUID uuid.UUID, p usersdb.PartyAuthnPolicy) PartyAuthnPolicyView {
	v := PartyAuthnPolicyView{
		PartyUUID: partyUUID, CredentialRule: p.CredentialRule, MinACR: p.MinAcr,
		RequiredOIDCProviderID: optionalText(p.RequiredOidcProviderID),
	}
	if p.UpdatedAt.Valid {
		t := p.UpdatedAt.Time
		v.UpdatedAt = &t
	}
	return v
}

func defaultPolicyView(partyUUID uuid.UUID) PartyAuthnPolicyView {
	return PartyAuthnPolicyView{PartyUUID: partyUUID, CredentialRule: CredentialRuleAny, MinACR: DefaultMinACR, IsDefault: true}
}

func policyAudit(partyUUID uuid.UUID, p usersdb.PartyAuthnPolicy) map[string]any {
	m := map[string]any{
		"party_uuid": partyUUID.String(), "credential_rule": p.CredentialRule, "min_acr": p.MinAcr,
	}
	if p.RequiredOidcProviderID.Valid {
		m["required_oidc_provider_id"] = p.RequiredOidcProviderID.String
	}
	return m
}

// ---------------------------------------------------------------------------
// authority
// ---------------------------------------------------------------------------

// isPartyAdmin reports whether the caller may update the party entity. A
// refusal is (false, nil); an absent authorizer fails closed; any other
// failure (an action-required refusal, an engine error) is returned.
func (s *PartyPolicyService) isPartyAdmin(ctx context.Context, partyID int64) (bool, error) {
	if s.az == nil {
		return false, nil
	}
	err := s.az.Authorize(ctx, opSlugUpdate, &partyID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, apiresp.ErrForbidden):
		return false, nil
	}
	return false, err
}

// refuse is the error of a caller refused by every door: 404 when it cannot
// read the party, 403 when it can.
func (s *PartyPolicyService) refuse(ctx context.Context, partyID int64) error {
	readable, err := partyReadable(ctx, s.coreQ, s.az, partyID)
	if err != nil {
		return err
	}
	if !readable {
		return apiresp.ErrNotFound
	}
	return apiresp.ErrForbidden
}

// requireAdmin authorizes the party-admin door alone.
func (s *PartyPolicyService) requireAdmin(ctx context.Context, partyID int64) error {
	admin, err := s.isPartyAdmin(ctx, partyID)
	if err != nil {
		return err
	}
	if !admin {
		return s.refuse(ctx, partyID)
	}
	return nil
}

// writeCaller returns the caller's AuthContext for a write: authenticated and
// not impersonating (an administrator acting as someone else never changes how
// a party gates).
func writeCaller(ctx context.Context) (opctx.AuthContext, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return opctx.AuthContext{}, err
	}
	if ac.ImpersonatorPrincipalID != 0 {
		return opctx.AuthContext{}, apiresp.ErrForbidden
	}
	return ac, nil
}

// holderSatisfiesGate reports whether the caller is the credential's own
// holder acting through a session that already satisfies the party's gate
// through another factor:
//
//   - the session principal holds the (live) credential;
//   - the session's current profile is an active profile of the party;
//   - profile_availability(session, profile) is `available`, and still is when
//     the credential is excluded from the session's factors
//     (profile_availability_excluding, the one place the exclusion is
//     evaluated), so a credential never authorizes itself.
func (s *PartyPolicyService) holderSatisfiesGate(ctx context.Context, q usersdb.Querier, tx pgx.Tx, ac opctx.AuthContext, partyID int64, cred usersdb.Credential) (bool, error) {
	if ac.SessionID <= 0 || cred.PrincipalID != ac.PrincipalID || cred.RevokedAt != nil {
		return false, nil
	}
	var profileParty pgtype.Int8
	var status string
	err := tx.QueryRow(ctx, `SELECT party_id, status FROM public.profiles WHERE entity_id = $1 AND holder_id = $2`,
		ac.ProfileID, ac.PrincipalID).Scan(&profileParty, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("users: load acting profile: %w", err)
	}
	if !profileParty.Valid || profileParty.Int64 != partyID || status != ProfileStatusActive {
		return false, nil
	}
	avail, err := q.ProfileAvailability(ctx, usersdb.ProfileAvailabilityParams{SessionID: ac.SessionID, ProfileID: ac.ProfileID})
	if err != nil {
		return false, fmt.Errorf("users: profile availability: %w", err)
	}
	if avail != availabilityAvailable {
		return false, nil
	}
	avail, err = q.ProfileAvailabilityExcluding(ctx, usersdb.ProfileAvailabilityExcludingParams{
		SessionID: ac.SessionID, ProfileID: ac.ProfileID, ExcludeCredentialID: cred.ID,
	})
	if err != nil {
		return false, fmt.Errorf("users: profile availability excluding credential: %w", err)
	}
	return avail == availabilityAvailable, nil
}

// ---------------------------------------------------------------------------
// authn policy
// ---------------------------------------------------------------------------

// GetAuthnPolicy returns the party's effective authentication policy (GET
// /v1/parties/{party_uuid}/authn-policy). It needs the party-scoped read gate
// (404 otherwise).
func (s *PartyPolicyService) GetAuthnPolicy(ctx context.Context, partyUUID uuid.UUID) (PartyAuthnPolicyView, error) {
	if _, err := adminCaller(ctx); err != nil {
		return PartyAuthnPolicyView{}, err
	}
	partyID, err := readablePartyID(ctx, s.coreQ, s.az, partyUUID)
	if err != nil {
		return PartyAuthnPolicyView{}, err
	}
	row, err := usersdb.New(s.pool).GetPartyAuthnPolicy(ctx, partyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultPolicyView(partyUUID), nil
	}
	if err != nil {
		return PartyAuthnPolicyView{}, fmt.Errorf("users: load party authn policy: %w", err)
	}
	return policyView(partyUUID, row), nil
}

// validatePolicyInput checks the shape of a policy: a known rule, a provider
// exactly when the rule is `provider`, and a min_acr from 1 to 3. It returns
// the provider as a nullable text.
func validatePolicyInput(in PutPartyAuthnPolicyInput) (pgtype.Text, error) {
	switch in.CredentialRule {
	case CredentialRuleAny, CredentialRuleAuthorized, CredentialRuleProvider:
	case "":
		return pgtype.Text{}, invalidField("credential_rule", "users.credential_rule_required", "credential_rule is required")
	default:
		return pgtype.Text{}, invalidField("credential_rule", "users.credential_rule_invalid", "credential_rule must be any, authorized or provider")
	}
	if in.MinACR < DefaultMinACR || in.MinACR > maxMinACR {
		return pgtype.Text{}, invalidField("min_acr", "users.min_acr_invalid", "min_acr must be 1, 2 or 3")
	}
	switch {
	case in.CredentialRule == CredentialRuleProvider && in.RequiredOIDCProviderID == "":
		return pgtype.Text{}, invalidField("required_oidc_provider_id", "users.provider_required", "required_oidc_provider_id is required with credential_rule provider")
	case in.CredentialRule != CredentialRuleProvider && in.RequiredOIDCProviderID != "":
		return pgtype.Text{}, invalidField("required_oidc_provider_id", "users.provider_unexpected", "required_oidc_provider_id applies only with credential_rule provider")
	case len(in.RequiredOIDCProviderID) > maxProviderIDLen:
		return pgtype.Text{}, invalidField("required_oidc_provider_id", "users.provider_invalid", "required_oidc_provider_id is too long")
	}
	return pgtype.Text{String: in.RequiredOIDCProviderID, Valid: in.RequiredOIDCProviderID != ""}, nil
}

// PutAuthnPolicy creates or replaces the party's authentication policy (PUT
// /v1/parties/{party_uuid}/authn-policy). It needs `update` on the party (404
// when the caller cannot read the party, 403 otherwise) and a fresh
// authentication (409 users.step_up_required). A required provider that does
// not exist is a 400 field error.
//
// Observations: `create` or `update` `party_authn_policy` on the party.
func (s *PartyPolicyService) PutAuthnPolicy(ctx context.Context, partyUUID uuid.UUID, in PutPartyAuthnPolicyInput) (PartyAuthnPolicyView, error) {
	ac, err := writeCaller(ctx)
	if err != nil {
		return PartyAuthnPolicyView{}, err
	}
	provider, err := validatePolicyInput(in)
	if err != nil {
		return PartyAuthnPolicyView{}, err
	}
	partyID, err := lookupParty(ctx, s.coreQ, partyUUID)
	if err != nil {
		return PartyAuthnPolicyView{}, err
	}
	if err := s.requireAdmin(ctx, partyID); err != nil {
		return PartyAuthnPolicyView{}, err
	}
	if err := s.users.requireFresh(ac); err != nil {
		return PartyAuthnPolicyView{}, err
	}

	var view PartyAuthnPolicyView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		q := s.usersQ(tx)
		if provider.Valid {
			if _, err := q.GetOIDCProvider(ctx, provider.String); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return invalidField("required_oidc_provider_id", CodeProviderUnknown, "no such OIDC provider")
				}
				return fmt.Errorf("users: load oidc provider: %w", err)
			}
		}
		before, err := q.GetPartyAuthnPolicy(ctx, partyID)
		existed := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("users: load party authn policy: %w", err)
		}
		row, err := q.UpsertPartyAuthnPolicy(ctx, usersdb.UpsertPartyAuthnPolicyParams{
			PartyID: partyID, CredentialRule: in.CredentialRule, RequiredOidcProviderID: provider, MinAcr: int16(in.MinACR),
		})
		if err != nil {
			return fmt.Errorf("users: upsert party authn policy: %w", err)
		}
		if existed && samePolicy(before, row) {
			view = policyView(partyUUID, row)
			return nil
		}
		op, beforeAudit := opCreate, any(nil)
		if existed {
			op, beforeAudit = opUpdate, policyAudit(partyUUID, before)
		}
		if err := observeInTx(ctx, s.obs, tx, op, resourcePartyAuthnPolicy, partyID, beforeAudit, policyAudit(partyUUID, row)); err != nil {
			return err
		}
		view = policyView(partyUUID, row)
		return nil
	})
	if err != nil {
		return PartyAuthnPolicyView{}, err
	}
	return view, nil
}

func samePolicy(a, b usersdb.PartyAuthnPolicy) bool {
	return a.CredentialRule == b.CredentialRule && a.MinAcr == b.MinAcr &&
		a.RequiredOidcProviderID == b.RequiredOidcProviderID
}

// ---------------------------------------------------------------------------
// credential authorizations
// ---------------------------------------------------------------------------

// ListCredentialAuthorizations lists the credentials authorized for the party,
// oldest first (GET /v1/parties/{party_uuid}/credential-authorizations). It
// needs `update` on the party (404 when the caller cannot read it, 403
// otherwise).
func (s *PartyPolicyService) ListCredentialAuthorizations(ctx context.Context, partyUUID uuid.UUID) (CredentialAuthorizationList, error) {
	if _, err := adminCaller(ctx); err != nil {
		return CredentialAuthorizationList{}, err
	}
	partyID, err := lookupParty(ctx, s.coreQ, partyUUID)
	if err != nil {
		return CredentialAuthorizationList{}, err
	}
	if err := s.requireAdmin(ctx, partyID); err != nil {
		return CredentialAuthorizationList{}, err
	}
	rows, err := usersdb.New(s.pool).ListCredentialPartyAuthorizationsByParty(ctx, partyID)
	if err != nil {
		return CredentialAuthorizationList{}, fmt.Errorf("users: list credential authorizations: %w", err)
	}
	ids := make([]int64, 0, 2*len(rows))
	for _, r := range rows {
		ids = append(ids, r.PrincipalID, r.CredentialPartyAuthorization.AuthorizedByProfileID)
	}
	uuids, err := entityUUIDs(ctx, s.pool, ids)
	if err != nil {
		return CredentialAuthorizationList{}, err
	}
	out := CredentialAuthorizationList{Authorizations: make([]CredentialAuthorizationView, 0, len(rows))}
	for _, r := range rows {
		a := r.CredentialPartyAuthorization
		out.Authorizations = append(out.Authorizations, CredentialAuthorizationView{
			CredentialUUID: r.CredentialUuid, Kind: r.Kind, Label: r.Label,
			HolderUUID: uuids[r.PrincipalID], AuthorizedByProfileUUID: uuids[a.AuthorizedByProfileID],
			AuthorizedAt: a.AuthorizedAt.Time,
		})
	}
	return out, nil
}

// credentialWriteGate is the authorization shared by the four credential
// writes (authorize, deauthorize, confine, unconfine).
type credentialWriteGate struct {
	ac      opctx.AuthContext
	partyID int64
	cred    usersdb.Credential
	// admin is true when the caller is a party administrator; holder is true
	// when the caller is the credential's own holder acting through a session
	// that satisfies the party's gate through another factor. Both can hold.
	admin, holder bool
}

// gateCredentialWrite authorizes a write about a credential and a party and
// returns the credential, locked. The caller must be a party administrator or
// the credential's own holder acting through a session that satisfies the
// party's gate through another factor; every other caller is refused (404 when
// it cannot read the party, 403 otherwise). The credential must be a live one
// of a principal holding an active profile in the party (404; an invited or
// ended profile does not qualify), and the caller's authentication must be
// fresh (409). The holder's profile is read FOR SHARE after the credential
// lock, so the write serializes with an ending. The returned gate records
// which doors the caller satisfies, for the writes that restrict one of them
// (see ConfineCredential and UnconfineCredential).
func (s *PartyPolicyService) gateCredentialWrite(ctx context.Context, tx pgx.Tx, partyUUID, credUUID uuid.UUID) (credentialWriteGate, error) {
	ac, err := writeCaller(ctx)
	if err != nil {
		return credentialWriteGate{}, err
	}
	partyID, err := lookupParty(ctx, s.coreQ, partyUUID)
	if err != nil {
		return credentialWriteGate{}, err
	}
	admin, err := s.isPartyAdmin(ctx, partyID)
	if err != nil {
		return credentialWriteGate{}, err
	}
	q := s.usersQ(tx)
	cred, err := q.GetCredentialByUUID(ctx, credUUID)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return credentialWriteGate{}, fmt.Errorf("users: load credential: %w", err)
	}
	holder := false
	if !missing {
		if holder, err = s.holderSatisfiesGate(ctx, q, tx, ac, partyID, cred); err != nil {
			return credentialWriteGate{}, err
		}
	}
	if !admin && !holder {
		// A caller outside both doors learns nothing about the credential: an
		// unknown one is refused like one it may not touch.
		return credentialWriteGate{}, s.refuse(ctx, partyID)
	}
	if missing || cred.RevokedAt != nil {
		return credentialWriteGate{}, apiresp.ErrNotFound
	}
	if err := s.users.requireFresh(ac); err != nil {
		return credentialWriteGate{}, err
	}
	// Serialize writes on the credential, then re-read the state under the lock.
	cred, err = q.GetCredentialByUUIDForUpdate(ctx, credUUID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cred.RevokedAt != nil) {
		return credentialWriteGate{}, apiresp.ErrNotFound
	}
	if err != nil {
		return credentialWriteGate{}, fmt.Errorf("users: lock credential: %w", err)
	}
	// Read the holder's active profile under a row lock, after the credential
	// lock. EndInTx and the departure cleanup lock that profile FOR UPDATE, so
	// the share lock makes this write wait for a concurrent ending: either the
	// ending commits first and the profile reads as ended here (404), or this
	// write commits first and the cleanup, which runs after, removes what it
	// wrote. A plain read would let the write land after the cleanup ran.
	var heldID int64
	if err := tx.QueryRow(ctx,
		`SELECT entity_id FROM public.profiles WHERE holder_id = $1 AND party_id = $2 AND status = 'active' FOR SHARE`,
		cred.PrincipalID, partyID).Scan(&heldID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialWriteGate{}, apiresp.ErrNotFound
		}
		return credentialWriteGate{}, fmt.Errorf("users: check holder profile: %w", err)
	}
	return credentialWriteGate{ac: ac, partyID: partyID, cred: cred, admin: admin, holder: holder}, nil
}

// partyControlled reports whether the party controls the credential: its
// principal is a managed user of the party, or it is an OIDC credential of a
// provider bound to the party. Any other credential, such as an outside
// collaborator's personal one, is not the party's to confine. It reads inside
// the transaction after the credential lock, so the answer is stable for the
// write. No issuer column (such as who created the credential) is an input.
func partyControlled(ctx context.Context, tx pgx.Tx, partyID int64, cred usersdb.Credential) (bool, error) {
	var controlled bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM mod_users.users WHERE entity_id = $1 AND managing_party_id = $2)
		     OR EXISTS (SELECT 1 FROM mod_users.cred_oidc o
		                  JOIN mod_users.oidc_providers p ON p.id = o.provider_id
		                 WHERE o.credential_id = $3 AND p.party_id = $2)`,
		cred.PrincipalID, partyID, cred.ID).Scan(&controlled)
	if err != nil {
		return false, fmt.Errorf("users: classify credential: %w", err)
	}
	return controlled, nil
}

// authorizationAudit is the observer payload of a credential authorization.
func authorizationAudit(credUUID, partyUUID uuid.UUID) map[string]any {
	return map[string]any{"credential_uuid": credUUID.String(), "party_uuid": partyUUID.String()}
}

func (s *PartyPolicyService) authorizationView(ctx context.Context, tx pgx.Tx, cred usersdb.Credential, a usersdb.CredentialPartyAuthorization) (CredentialAuthorizationView, error) {
	uuids, err := entityUUIDs(ctx, tx, []int64{cred.PrincipalID, a.AuthorizedByProfileID})
	if err != nil {
		return CredentialAuthorizationView{}, err
	}
	return CredentialAuthorizationView{
		CredentialUUID: cred.Uuid, Kind: cred.Kind, Label: cred.Label,
		HolderUUID: uuids[cred.PrincipalID], AuthorizedByProfileUUID: uuids[a.AuthorizedByProfileID],
		AuthorizedAt: a.AuthorizedAt.Time,
	}, nil
}

// AuthorizeCredential authorizes the credential for the party (PUT
// /v1/parties/{party_uuid}/credential-authorizations/{credential_uuid}); it is
// idempotent, and an existing authorization is returned unchanged. See
// gateCredentialWrite for who may write it. The authorizing profile is the
// acting profile.
//
// Observations: `create` `credential_party_authorization` on the party, only
// when a row is created.
func (s *PartyPolicyService) AuthorizeCredential(ctx context.Context, partyUUID, credUUID uuid.UUID) (CredentialAuthorizationView, error) {
	var view CredentialAuthorizationView
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		g, err := s.gateCredentialWrite(ctx, tx, partyUUID, credUUID)
		if err != nil {
			return err
		}
		q := s.usersQ(tx)
		row, err := q.GetCredentialPartyAuthorization(ctx, usersdb.GetCredentialPartyAuthorizationParams{
			CredentialID: g.cred.ID, PartyID: g.partyID,
		})
		switch {
		case err == nil:
			view, err = s.authorizationView(ctx, tx, g.cred, row)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("users: load credential authorization: %w", err)
		}
		row, err = q.AuthorizeCredentialForParty(ctx, usersdb.AuthorizeCredentialForPartyParams{
			CredentialID: g.cred.ID, PartyID: g.partyID, AuthorizedByProfileID: g.ac.ProfileID,
		})
		if err != nil {
			return fmt.Errorf("users: authorize credential: %w", err)
		}
		if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredentialPartyAuthorization, g.partyID,
			nil, authorizationAudit(g.cred.Uuid, partyUUID)); err != nil {
			return err
		}
		view, err = s.authorizationView(ctx, tx, g.cred, row)
		return err
	})
	if err != nil {
		return CredentialAuthorizationView{}, err
	}
	return view, nil
}

// DeauthorizeCredential removes the credential's authorization for the party
// (DELETE /v1/parties/{party_uuid}/credential-authorizations/{credential_uuid});
// a credential with no authorization is apiresp.ErrNotFound. See
// gateCredentialWrite for who may write it.
//
// Observations: `delete` `credential_party_authorization` on the party.
func (s *PartyPolicyService) DeauthorizeCredential(ctx context.Context, partyUUID, credUUID uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		g, err := s.gateCredentialWrite(ctx, tx, partyUUID, credUUID)
		if err != nil {
			return err
		}
		n, err := s.usersQ(tx).DeleteCredentialPartyAuthorization(ctx, usersdb.DeleteCredentialPartyAuthorizationParams{
			CredentialID: g.cred.ID, PartyID: g.partyID,
		})
		if err != nil {
			return fmt.Errorf("users: delete credential authorization: %w", err)
		}
		if n == 0 {
			return apiresp.ErrNotFound
		}
		return observeInTx(ctx, s.obs, tx, opDelete, resourceCredentialPartyAuthorization, g.partyID,
			authorizationAudit(g.cred.Uuid, partyUUID), nil)
	})
}

// ---------------------------------------------------------------------------
// confinement
// ---------------------------------------------------------------------------

// ConfineCredential confines the credential to the party (PUT
// /v1/parties/{party_uuid}/confinements/{credential_uuid}); confining a
// credential already confined to the party changes nothing. A credential
// confined to another party is ErrCredentialConfinedElsewhere (409). See
// gateCredentialWrite for who may write it. The administrator door confines
// only party-controlled credentials (those of a managed user of the party, and
// OIDC credentials of a provider bound to the party); any other credential,
// such as an outside collaborator's personal one, is apiresp.ErrNotFound there
// and needs its holder, who is served by the holder door.
//
// Observations: `update` `credential` (the principal), only on a change.
func (s *PartyPolicyService) ConfineCredential(ctx context.Context, partyUUID, credUUID uuid.UUID) (CredentialConfinementView, error) {
	view := CredentialConfinementView{CredentialUUID: credUUID, PartyUUID: partyUUID}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		g, err := s.gateCredentialWrite(ctx, tx, partyUUID, credUUID)
		if err != nil {
			return err
		}
		if !g.holder {
			// Only the administrator door is left: it reaches only the
			// credentials the party controls, and hides the rest.
			controlled, err := partyControlled(ctx, tx, g.partyID, g.cred)
			if err != nil {
				return err
			}
			if !controlled {
				return apiresp.ErrNotFound
			}
		}
		if g.cred.ConfinedToPartyID.Valid {
			if g.cred.ConfinedToPartyID.Int64 == g.partyID {
				return nil
			}
			return ErrCredentialConfinedElsewhere
		}
		return s.setConfinement(ctx, tx, g, pgtype.Int8{Int64: g.partyID, Valid: true})
	})
	if err != nil {
		return CredentialConfinementView{}, err
	}
	return view, nil
}

// UnconfineCredential clears the credential's confinement (DELETE
// /v1/parties/{party_uuid}/confinements/{credential_uuid}), only when it
// currently names the party. A credential confined to another party is
// ErrCredentialConfinedElsewhere (409); one that is not confined is
// apiresp.ErrNotFound. See gateCredentialWrite for who may write it. The holder
// door is refused for a party-controlled credential (see ConfineCredential), so
// a party-issued credential cannot escape departure revocation; a holder may
// still clear the confinement of their own personal credential. The
// administrator door is unrestricted.
//
// Observations: `update` `credential` (the principal).
func (s *PartyPolicyService) UnconfineCredential(ctx context.Context, partyUUID, credUUID uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		g, err := s.gateCredentialWrite(ctx, tx, partyUUID, credUUID)
		if err != nil {
			return err
		}
		if !g.admin {
			// The holder door: it cannot clear what the party controls, or a
			// party-issued credential could escape departure revocation.
			controlled, err := partyControlled(ctx, tx, g.partyID, g.cred)
			if err != nil {
				return err
			}
			if controlled {
				return s.refuse(ctx, g.partyID)
			}
		}
		switch {
		case !g.cred.ConfinedToPartyID.Valid:
			return apiresp.ErrNotFound
		case g.cred.ConfinedToPartyID.Int64 != g.partyID:
			return ErrCredentialConfinedElsewhere
		}
		return s.setConfinement(ctx, tx, g, pgtype.Int8{})
	})
}

// setConfinement writes the credential's confinement and observes the change.
func (s *PartyPolicyService) setConfinement(ctx context.Context, tx pgx.Tx, g credentialWriteGate, party pgtype.Int8) error {
	updated, err := s.usersQ(tx).SetCredentialConfinement(ctx, usersdb.SetCredentialConfinementParams{
		ID: g.cred.ID, ConfinedToPartyID: party,
	})
	if err != nil {
		return fmt.Errorf("users: set credential confinement: %w", err)
	}
	return observeInTx(ctx, s.obs, tx, opUpdate, resourceCredential, g.cred.PrincipalID,
		credentialAudit(g.cred, nil), credentialAudit(updated, nil))
}
