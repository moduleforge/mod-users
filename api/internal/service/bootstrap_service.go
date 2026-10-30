package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Operator party kinds accepted by Bootstrap.
const (
	BootstrapOperatorOrganization  = "organization"
	BootstrapOperatorNaturalPerson = "natural_person"
)

// Error codes owned by the bootstrap flow.
const (
	CodeAlreadyBootstrapped = "users.already_bootstrapped"
	CodeSetupTokenInvalid   = "users.setup_token_invalid"
)

// Observer resources and ops the bootstrap flow emits beyond the shared ones
// (API surface section 10.1).
const (
	resourceBootstrap = "bootstrap"
	resourceGrant     = "grant"
	opGrant           = "grant"
)

// manageOperationSlug is the mod-authz operation the operator administrator
// holds as a wildcard grant.
const manageOperationSlug = "manage"

// ErrAlreadyBootstrapped is returned when the deployment already has an
// operator party (users.already_bootstrapped, 409). Nothing the losing
// bootstrap wrote survives: its transaction is rolled back.
var ErrAlreadyBootstrapped = apiresp.Conflict(apiresp.FieldError{
	Field: "operator", Code: CodeAlreadyBootstrapped, Message: "the deployment is already bootstrapped",
})

// ErrSetupTokenInvalid is returned for a missing or wrong X-Setup-Token on the
// bootstrap route (users.setup_token_invalid, 401).
var ErrSetupTokenInvalid = &AuthError{
	Status: http.StatusUnauthorized, Code: CodeSetupTokenInvalid, Message: "the setup token is missing or invalid",
}

// BootstrapOperator names the operator party Bootstrap creates or reuses.
// Kind "organization" creates an organization (LegalName and LegalForm
// required, Jurisdiction optional, no owner); kind "natural_person" makes the
// first user's own person record the operator party (single-person self-host).
type BootstrapOperator struct {
	Kind         string
	LegalName    string
	LegalForm    string
	Jurisdiction string
}

// BootstrapAdmin is the first user Bootstrap creates.
type BootstrapAdmin struct {
	Email      string
	Password   string
	GivenName  string
	FamilyName string
}

// BootstrapInput is the input of Bootstrap.
type BootstrapInput struct {
	Operator BootstrapOperator
	Admin    BootstrapAdmin
}

// BootstrapResult is the outcome of a successful Bootstrap. Ids are internal;
// they must not reach an HTTP response (Token carries the public view).
type BootstrapResult struct {
	Token             TokenResponse
	UserID            int64
	OperatorPartyID   int64
	OperatorPartyUUID uuid.UUID
	AdminProfileID    int64
	AdminProfileUUID  uuid.UUID
	PasswordID        int64
	GrantID           int64
	SessionID         int64
}

// BootstrapService runs the first-run bootstrap of a deployment (decisions U9
// and U10): on presentation of the setup token (checked by the HTTP layer) it
// creates, in one transaction, the first user, the operator party, the admin
// party profile with its wildcard `manage` grant, the operator party's
// authentication policy, and the session the response lands on.
// mod-core's core_claim_operator_party is the single winner: there is no
// "is this the first user" check anywhere.
//
// It also answers the ambient operator-administrator check of the OIDC
// configuration routes (IsOperatorAdmin).
type BootstrapService struct {
	pool     *pgxpool.Pool
	az       coreAuthz.Authorizer
	obs      *observer.ObserverGroup
	users    *UserService
	creds    *CredentialService
	sessions *SessionService
	profiles *ProfileService
	// orgs creates the operator organization (CreateInTx, the trusted
	// non-authorizing variant). It is held privately and used only by the
	// bootstrap, never by a handler-facing path.
	orgs coreservice.OrganizationServicer
}

// NewBootstrapService constructs a BootstrapService. users, creds, sessions and
// profiles supply the shared creation primitives (registration, credentials,
// session start, and ProfileService's internal profile creation so lifecycle
// observers fire); orgs supplies mod-core's OrganizationService.CreateInTx for
// the operator organization; az serves IsOperatorAdmin only. pool serves
// Bootstrap's transaction and IsOperatorAdmin's operator-profile lookup.
func NewBootstrapService(
	pool *pgxpool.Pool,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
	creds *CredentialService,
	sessions *SessionService,
	profiles *ProfileService,
	orgs coreservice.OrganizationServicer,
) *BootstrapService {
	return &BootstrapService{
		pool: pool, az: az, obs: obs,
		users: users, creds: creds, sessions: sessions, profiles: profiles, orgs: orgs,
	}
}

// ValidateBootstrapInput runs the stateless shape checks of a bootstrap
// request and returns every failing field at once (400).
func ValidateBootstrapInput(in BootstrapInput) error {
	var details []apiresp.FieldError
	add := func(field, code, msg string) {
		details = append(details, apiresp.FieldError{Field: field, Code: code, Message: msg})
	}
	switch strings.TrimSpace(in.Operator.Kind) {
	case BootstrapOperatorOrganization:
		if strings.TrimSpace(in.Operator.LegalName) == "" {
			add("operator.legal_name", "users.legal_name_required", "operator.legal_name is required")
		}
		if !coreservice.ValidOrganizationLegalForm(strings.TrimSpace(in.Operator.LegalForm)) {
			add("operator.legal_form", "users.legal_form_invalid",
				"operator.legal_form must be one of corporation, llc, partnership, nonprofit, public_body, trust")
		}
	case BootstrapOperatorNaturalPerson:
	default:
		add("operator.kind", "users.operator_kind_invalid", "operator.kind must be organization or natural_person")
	}
	if strings.TrimSpace(in.Admin.Email) == "" {
		add("admin.email", "users.email_required", "admin.email is required")
	} else if validateEmail(CanonicalizeEmail(in.Admin.Email)) != nil {
		add("admin.email", "users.email_invalid", "admin.email is not a valid email address")
	}
	if in.Admin.Password == "" {
		add("admin.password", "users.password_required", "admin.password is required")
	} else if ValidatePassword(in.Admin.Password) != nil {
		add("admin.password", "users.password_invalid", "admin.password must be 12 to 1024 bytes")
	}
	if strings.TrimSpace(in.Admin.GivenName) == "" {
		add("admin.given_name", "users.given_name_required", "admin.given_name is required")
	}
	if strings.TrimSpace(in.Admin.FamilyName) == "" {
		add("admin.family_name", "users.family_name_required", "admin.family_name is required")
	}
	if len(details) > 0 {
		return apiresp.InvalidInput(details...)
	}
	return nil
}

// lockPlatformSettingsSQL locks the platform_settings singleton for the
// bootstrap transaction. Concurrent bootstraps queue on this lock; the one
// that waited re-reads the committed row and sees the operator party, so it
// answers 409 before writing anything rather than tripping over the winner's
// identifier. core_claim_operator_party remains the arbiter.
const lockPlatformSettingsSQL = `SELECT operator_party_id FROM public.platform_settings WHERE id = 1 FOR UPDATE`

// Bootstrap creates, in one transaction and in this order (decision U10):
//
//  1. the first user per the registration path: a bring-your-own user with its
//     own natural_person (users.person_id) and personal profile, a verified
//     primary email identifier with its email_otp credential, and a password
//     credential bound to that identifier;
//  2. the operator party: an organization (owner NULL) or the user's own
//     person record;
//  3. core_claim_operator_party (false: ErrAlreadyBootstrapped, rolled back);
//  4. the admin party profile (holder the user, party the operator party,
//     active, source manual) through ProfileService's internal creation, so
//     lifecycle observers fire;
//  5. a wildcard `manage` grant held by the admin profile (accepted by
//     mod-authz's operator-actor trigger only because step 3 already claimed
//     the party);
//  6. party_authn_policies(operator party, credential_rule 'authorized');
//  7. credential_party_authorizations(password, operator party, authorized by
//     the admin profile);
//  8. credentials.default_profile_id = the admin profile on the password;
//
// then starts the session (password factor) that lands on the admin profile,
// and writes the `create` `bootstrap` observation.
//
// It runs as a trusted system flow: no grant is consulted. Observations: the
// writes of step 1 to 4 are observed as the first user's personal profile
// (the shared creation primitives already act as it); the grant and the
// bootstrap event as the admin profile, the bootstrap event with the new
// session (API surface section 10.1).
func (s *BootstrapService) Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	if err := ValidateBootstrapInput(in); err != nil {
		return BootstrapResult{}, err
	}
	if s.pool == nil {
		return BootstrapResult{}, errors.New("users: no database pool configured for bootstrap")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := s.bootstrapInTx(ctx, tx, in)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap commit: %w", err)
	}
	return res, nil
}

func (s *BootstrapService) bootstrapInTx(ctx context.Context, tx pgx.Tx, in BootstrapInput) (BootstrapResult, error) {
	var current pgtype.Int8
	if err := tx.QueryRow(ctx, lockPlatformSettingsSQL).Scan(&current); err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: lock platform settings: %w", err)
	}
	if current.Valid {
		return BootstrapResult{}, ErrAlreadyBootstrapped
	}

	// 1. The first user.
	user, err := s.users.CreateBYOUserInTx(ctx, tx, CreateBYOUserInput{
		GivenName: in.Admin.GivenName, FamilyName: in.Admin.FamilyName,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	userID := user.User.EntityID
	personalCtx := user.ActingContext(ctx)
	ident, err := s.users.AddIdentifierInTx(personalCtx, tx, AddIdentifierInput{
		UserID: userID, Kind: IdentifierKindEmail, Value: in.Admin.Email, Primary: true, Verified: true,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	pw, err := s.creds.CreatePasswordInTx(personalCtx, tx, CreatePasswordInput{
		PrincipalID: userID, IdentifierID: &ident.ID, Password: in.Admin.Password,
	})
	if err != nil {
		return BootstrapResult{}, err
	}

	// 2. The operator party.
	var (
		partyID   int64
		partyUUID uuid.UUID
	)
	switch strings.TrimSpace(in.Operator.Kind) {
	case BootstrapOperatorOrganization:
		partyID, partyUUID, err = s.createOrganization(personalCtx, tx, in.Operator)
		if err != nil {
			return BootstrapResult{}, err
		}
	default: // natural_person, validated above
		partyID, partyUUID = user.PersonID, user.PersonUUID
	}

	// 3. The race-free single winner.
	cq := coredb.New(tx)
	claimed, err := cq.ClaimOperatorParty(ctx, partyID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: claim operator party: %w", err)
	}
	if !claimed {
		return BootstrapResult{}, ErrAlreadyBootstrapped
	}

	// 4. The admin party profile.
	admin, err := s.profiles.createInTx(personalCtx, tx, newProfile{
		HolderID: userID, Holder: user.User, PartyID: partyID,
		Status: ProfileStatusActive, Source: ProfileSourceManual,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	// WHY no system-actor marker: the bootstrap is a trusted flow, but adminCtx
	// acts as the first user's operator-party profile, a real user's party
	// profile, and the marker is never an operator signal. The trusted grant
	// write below does not authorize, so it needs no marker.
	// IMPLIES: adminCtx (like personalCtx above) is for the bootstrap's
	// observations and unauthorized writes only; it would be gated, and
	// refused (ACR 0), if it reached an Authorize entry point. The bootstrap's
	// own session context, sessCtx, is built below once the session exists.
	adminCtx := withActingProfile(ctx, userID, admin.ID)

	// 5. The wildcard manage grant, in this transaction.
	grantID, err := s.createWildcardManageGrant(adminCtx, tx, admin)
	if err != nil {
		return BootstrapResult{}, err
	}

	// 6 to 8. The operator party's gate: only authorized credentials, the
	// bootstrap password authorized, landing on the admin profile.
	uq := usersdb.New(tx)
	if _, err := uq.UpsertPartyAuthnPolicy(ctx, usersdb.UpsertPartyAuthnPolicyParams{
		PartyID: partyID, CredentialRule: CredentialRuleAuthorized, MinAcr: 1,
	}); err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: party authn policy: %w", err)
	}
	if _, err := uq.AuthorizeCredentialForParty(ctx, usersdb.AuthorizeCredentialForPartyParams{
		CredentialID: pw.ID, PartyID: partyID, AuthorizedByProfileID: admin.ID,
	}); err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: authorize password: %w", err)
	}
	if _, err := uq.UpdateCredentialLabelAndDefault(ctx, usersdb.UpdateCredentialLabelAndDefaultParams{
		ID: pw.ID, Label: pw.Label, DefaultProfileID: pgtype.Int8{Int64: admin.ID, Valid: true},
	}); err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: password default profile: %w", err)
	}

	// The session, landing on the admin profile through the password's
	// default profile.
	tok, err := s.sessions.StartSessionInTx(ctx, tx, StartSessionInput{
		PrincipalID:                userID,
		Factors:                    []Factor{{CredentialID: pw.ID}},
		CredentialDefaultProfileID: admin.ID,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	if tok.Session == nil {
		return BootstrapResult{}, errors.New("users: bootstrap: session start returned no session")
	}
	sess, err := uq.GetSessionByUUID(ctx, tok.Session.UUID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("users: bootstrap: load session: %w", err)
	}

	// WHY no system-actor marker: sessCtx is the first administrator's real
	// session on the operator profile. IMPLIES: it is gated like that session;
	// here it attributes the bootstrap observation only.
	sessCtx := opctx.WithAuthContext(ctx, opctx.AuthContext{
		PrincipalID: userID, ProfileID: admin.ID, SessionID: sess.ID,
		ACR: sess.Acr, AuthTime: sess.AuthTime.Time,
	})
	if err := observeInTx(sessCtx, s.obs, tx, opCreate, resourceBootstrap, partyID, nil, map[string]any{
		"operator_party_uuid": partyUUID.String(),
		"operator_kind":       strings.TrimSpace(in.Operator.Kind),
		"admin_user_uuid":     user.UserUUID.String(),
		"admin_profile_uuid":  admin.UUID.String(),
		"credential_rule":     CredentialRuleAuthorized,
	}); err != nil {
		return BootstrapResult{}, err
	}

	return BootstrapResult{
		Token:             tok,
		UserID:            userID,
		OperatorPartyID:   partyID,
		OperatorPartyUUID: partyUUID,
		AdminProfileID:    admin.ID,
		AdminProfileUUID:  admin.UUID,
		PasswordID:        pw.ID,
		GrantID:           grantID,
		SessionID:         sess.ID,
	}, nil
}

// createOrganization creates the operator organization (owner NULL) through
// mod-core's trusted OrganizationService.CreateInTx, in the bootstrap's
// transaction and acting as the profile on ctx (the first user's personal
// profile, which mod-audit needs as the actor). CreateInTx runs no
// authorization (the bootstrap is a trusted flow gated by the setup token and
// core_claim_operator_party), applies mod-core's input normalization and
// tax_id encryption path, and emits the in-transaction `create` `organization`
// observation.
func (s *BootstrapService) createOrganization(ctx context.Context, tx pgx.Tx, op BootstrapOperator) (int64, uuid.UUID, error) {
	if s.orgs == nil {
		// WHY: the dependency is injected, and a nil one would otherwise panic
		// mid-transaction. IMPLIES: a mis-wired composition root fails the
		// bootstrap with a clear error (rolled back) instead of crashing.
		return 0, uuid.Nil, errors.New("users: no organization service configured for bootstrap")
	}
	// WHY WithoutOwner: the operator organization is the root of the party
	// tree and has no owner (decision U10); CreateEntity's default would own it
	// by the acting profile's ownership subject. IMPLIES: the entity row is
	// written with owner NULL.
	// CreateInTx re-validates legal_name and legal_form with the same
	// predicates ValidateBootstrapInput already applied, so its validation
	// errors cannot occur here and the bootstrap's own 400s keep precedence.
	_, entityUUID, entityID, err := s.orgs.CreateInTx(ctx, tx, coreservice.CreateOrganizationInput{
		LegalName:    op.LegalName,
		LegalForm:    op.LegalForm,
		Jurisdiction: op.Jurisdiction,
	}, coreservice.WithoutOwner())
	if err != nil {
		return 0, uuid.Nil, fmt.Errorf("users: bootstrap: create organization: %w", err)
	}
	// NOTE: CreateInTx documents that its caller dispatches ObserveAfterCommit
	// (create, organization, entityID) after the commit. This module does not
	// dispatch post-commit observations for any in-transaction creation it
	// composes (NaturalPersonService.CreateInTx in user_service.go and
	// user_self_service.go is the same), so the bootstrap follows that
	// convention; the gap is recorded as a finding.
	return entityID, entityUUID, nil
}

// createWildcardManageGrant inserts the admin profile's wildcard `manage`
// grant (entity scope: Scope NULL is coalesced to 'entity', target NULL)
// through mod-authz's generated query and observes it as `grant` `grant` with
// no target (API surface section 10.1).
func (s *BootstrapService) createWildcardManageGrant(ctx context.Context, tx pgx.Tx, admin Profile) (int64, error) {
	aq := authzdb.New(tx)
	op, err := aq.GetOperationBySlug(ctx, manageOperationSlug)
	if err != nil {
		return 0, fmt.Errorf("users: bootstrap: resolve manage operation: %w", err)
	}
	grant, err := aq.CreateGrant(ctx, authzdb.CreateGrantParams{
		ActorID:     admin.ID,
		OperationID: op.ID,
		GrantedBy:   pgtype.Int8{Int64: admin.ID, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("users: bootstrap: create wildcard manage grant: %w", err)
	}
	if s.obs != nil {
		if err := s.obs.Observe(ctx, tx, opGrant, resourceGrant, nil, nil, map[string]any{
			"id":         grant.ID,
			"actor_uuid": admin.UUID.String(),
			"operation":  manageOperationSlug,
			"scope":      grant.Scope,
			"wildcard":   true,
		}); err != nil {
			return 0, err
		}
	}
	return grant.ID, nil
}

// isOperatorProfileSQL asks mod-core whether a profile is an operator profile.
const isOperatorProfileSQL = `SELECT public.core_is_operator_profile($1)`

// IsOperatorAdmin reports whether the AuthContext on ctx is an operator
// administrator: an authenticated, non-impersonating session whose acting
// profile is an operator profile (core_is_operator_profile) and holds a
// wildcard `manage` grant. A context without such a session is (false, nil);
// a database fault is an error.
func (s *BootstrapService) IsOperatorAdmin(ctx context.Context) (bool, error) {
	ac, ok := opctx.Auth(ctx)
	if !ok || ac.Anonymous || ac.SessionID == 0 || ac.ProfileID == 0 || ac.ImpersonatorPrincipalID != 0 {
		return false, nil
	}
	if s.pool == nil || s.az == nil {
		return false, errors.New("users: operator admin check is not configured")
	}
	var isOperator bool
	if err := s.pool.QueryRow(ctx, isOperatorProfileSQL, ac.ProfileID).Scan(&isOperator); err != nil {
		return false, fmt.Errorf("users: operator profile check: %w", err)
	}
	if !isOperator {
		return false, nil
	}
	err := s.az.Authorize(ctx, manageOperationSlug, nil)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, apiresp.ErrForbidden), errors.Is(err, apiresp.ErrUnauthenticated):
		return false, nil
	default:
		return false, fmt.Errorf("users: operator manage check: %w", err)
	}
}

// Bootstrapped reports whether the deployment has an operator party.
func (s *BootstrapService) Bootstrapped(ctx context.Context) (bool, error) {
	if s.pool == nil {
		return false, errors.New("users: no database pool configured for bootstrap")
	}
	settings, err := coredb.New(s.pool).GetPlatformSettings(ctx)
	if err != nil {
		return false, fmt.Errorf("users: load platform settings: %w", err)
	}
	return settings.OperatorPartyID.Valid, nil
}
