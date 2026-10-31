package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Redirect success markers of the callback fragment.
const (
	fragHandoff   = "handoff"
	fragLinked    = "linked"
	fragSteppedUp = "stepped_up"
)

// Type slugs the managed just-in-time path instantiates (mod-core types).
const (
	userTypeSlug    = "user"
	profileTypeSlug = "profile"
)

// profileSourceIdPJIT is profiles.source of a profile an IdP sign-in created.
const profileSourceIdPJIT = "idp_jit"

// unknownFamilyName stands in for the family name of a just-in-time user whose
// IdP supplied none (a person record needs both names); the user edits it with
// PUT /v1/me.
const unknownFamilyName = "-"

// maxEmailAtLink bounds the email stored with an oidc credential (RFC 5321).
const maxEmailAtLink = 254

// callbackRefusal is returned from inside a callback transaction when the
// action cannot be completed; the callback redirects with code and the
// transaction rolls back, so a refused sign-in writes nothing.
type callbackRefusal struct{ code string }

func (e *callbackRefusal) Error() string { return "oidc callback: refused: " + e.code }

func refuse(code string) error { return &callbackRefusal{code: code} }

// finishCallbackError renders the failure of a callback transaction: a
// refusal and the expected business errors as a redirect with an error code,
// anything else as a 5xx that never echoes err.
func (h *OIDCHandler) finishCallbackError(w http.ResponseWriter, r *http.Request, providerID, step string, err error) {
	ctx := r.Context()
	var refusal *callbackRefusal
	var authErr *svc.AuthError
	switch {
	case errors.As(err, &refusal):
		slog.InfoContext(ctx, "oidc callback: refused", "provider", providerID, "step", step, "code", refusal.code)
		h.redirectError(w, r, refusal.code)
	case errors.Is(err, svc.ErrIdentifierTaken):
		// Lost a race with a registration of the same email.
		h.redirectError(w, r, localauth.RedirectErrAccountExists)
	case errors.Is(err, svc.ErrOIDCIdentityInUse):
		// Lost a race with another sign-in of the same external identity.
		slog.WarnContext(ctx, "oidc callback: external identity already linked", "provider", providerID, "step", step)
		h.redirectError(w, r, localauth.RedirectErrAuthFailed)
	case errors.As(err, &authErr):
		// A disabled principal, a dead session, an unavailable profile.
		slog.WarnContext(ctx, "oidc callback: sign-in refused", "provider", providerID, "step", step, "code", authErr.Code)
		h.redirectError(w, r, localauth.RedirectErrAuthFailed)
	default:
		slog.ErrorContext(ctx, "oidc callback: internal error", "provider", providerID, "step", step, "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("oidc callback %s: %w", step, err))
	}
}

// ---------------------------------------------------------------------------
// sign-in
// ---------------------------------------------------------------------------

// callbackLogin resolves the verified identity and signs it in: a known active
// oidc credential logs in; an unknown identity is created by the provider's
// jit_mode or refused. The session and a one-time login_handoff challenge are
// created in one transaction, and the redirect carries only the challenge code.
func (h *OIDCHandler) callbackLogin(w http.ResponseWriter, r *http.Request, providerID string, id localauth.ExternalIdentity, st localauth.StatePayload) {
	ctx := r.Context()
	policy, err := h.oauth.PolicyFor(providerID)
	if err != nil {
		h.finishCallbackError(w, r, providerID, "policy", err)
		return
	}
	res, err := h.resolver.Resolve(ctx, id, policy)
	if err != nil {
		h.finishCallbackError(w, r, providerID, "resolve", err)
		return
	}
	if res.Action == localauth.ActionRefuse {
		h.finishCallbackError(w, r, providerID, "resolve", refuse(res.Refusal))
		return
	}

	// A sign-in state always carries the browser's handoff challenge (/start
	// requires it); one without it is refused rather than minting an unbound code.
	if !localauth.ValidHandoffChallenge(st.HandoffChallenge) {
		h.finishCallbackError(w, r, providerID, "handoff", refuse(localauth.RedirectErrAuthFailed))
		return
	}

	var (
		code    string
		created *svc.CreatedUser
	)
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		code, created, err = h.signInInTx(ctx, tx, res, id, policy, st.HandoffChallenge)
		return err
	})
	if err != nil {
		h.finishCallbackError(w, r, providerID, "sign-in", err)
		return
	}
	// WHY after inTx: a just-in-time bring-your-own sign-in created a person
	// through mod-core's CreateInTx, which only observes inside the transaction;
	// the after-commit `create` natural_person belongs to the code that owns the
	// commit, and inTx has now committed. IMPLIES: a refused or rolled-back
	// sign-in dispatches nothing; created is nil for logins and managed
	// just-in-time users, which create no person.
	if created != nil {
		h.users.ObserveCreatedAfterCommit(ctx, *created)
	}
	slog.InfoContext(ctx, "oidc login succeeded", "provider", providerID)
	h.redirectOutcome(w, r, fragHandoff, code, st.ReturnPath)
}

// signInInTx performs the writes of one sign-in decision and returns the
// handoff code, bound to handoffChallenge. The principal is the credential's
// (login) or the user just created (just-in-time).
func (h *OIDCHandler) signInInTx(ctx context.Context, tx pgx.Tx, res localauth.Resolution, id localauth.ExternalIdentity, policy localauth.ProviderPolicy, handoffChallenge string) (string, *svc.CreatedUser, error) {
	var (
		principalID    int64
		credentialID   int64
		defaultProfile int64
		// created is set only for a bring-your-own just-in-time sign-in; the
		// committing caller dispatches its after-commit observation.
		created *svc.CreatedUser
	)
	switch res.Action {
	case localauth.ActionLogin:
		cred := res.Credential
		principalID, credentialID, defaultProfile = cred.PrincipalID, cred.ID, cred.DefaultProfileID.Int64
		if err := usersdb.New(tx).TouchCredentialLastUsed(ctx, cred.ID); err != nil {
			return "", nil, fmt.Errorf("touch credential: %w", err)
		}
	case localauth.ActionCreateBYO:
		var (
			err error
			c   svc.CreatedUser
		)
		if principalID, credentialID, err = h.createBYOUser(ctx, tx, id, policy, &c); err != nil {
			return "", nil, err
		}
		created = &c
	case localauth.ActionCreateManaged:
		var err error
		if principalID, credentialID, err = h.createManagedUser(ctx, tx, id, policy); err != nil {
			return "", nil, err
		}
	default:
		return "", nil, fmt.Errorf("unexpected resolver action %d", res.Action)
	}

	// CreateSessionInTx writes the `login` observation with the unauthenticated
	// flow's AuthContext (API surface section 10.1).
	ref, err := h.sessions.CreateSessionInTx(ctx, tx, svc.StartSessionInput{
		PrincipalID:                principalID,
		Factors:                    []svc.Factor{{CredentialID: credentialID, AssertedACR: policy.AssertedACR(id)}},
		CredentialDefaultProfileID: defaultProfile,
	})
	if err != nil {
		return "", nil, err
	}
	issued, err := h.challenges.IssueTokenInTx(ctx, tx, svc.IssueTokenInput{
		Kind:    svc.ChallengeLoginHandoff,
		Subject: svc.Subject{PrincipalID: principalID, SessionID: ref.ID},
		// The code is redeemable only with the verifier this challenge commits to.
		BindingHash: handoffChallenge,
	})
	if err != nil {
		return "", nil, err
	}
	return issued.Code, created, nil
}

// createBYOUser creates the account of a bring-your-own just-in-time sign-in:
// the user with its person and personal profile, the email identifier (verified
// only when the IdP asserted email_verified) and the oidc credential, confined
// to the provider's party when the provider is bound to one. Every
// observation runs as the new user's personal profile. createdOut receives the
// creation result so the committing caller can dispatch the after-commit
// observation of the new person (UserService.ObserveCreatedAfterCommit).
func (h *OIDCHandler) createBYOUser(ctx context.Context, tx pgx.Tx, id localauth.ExternalIdentity, policy localauth.ProviderPolicy, createdOut *svc.CreatedUser) (principalID, credentialID int64, err error) {
	settings, err := coredb.New(tx).GetPlatformSettings(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("load platform settings: %w", err)
	}
	if !settings.AllowByoIdentity {
		return 0, 0, refuse(localauth.RedirectErrNoAccount)
	}
	email := svc.CanonicalizeEmail(id.Email)
	if svc.ValidateEmail(email) != nil {
		return 0, 0, refuse(localauth.RedirectErrMissingEmail)
	}

	given, family := jitNames(id, email)
	created, err := h.users.CreateBYOUserInTx(ctx, tx, svc.CreateBYOUserInput{GivenName: given, FamilyName: family})
	if err != nil {
		return 0, 0, err
	}
	// WHY createdOut: the person's after-commit observation must wait for the
	// commit this function does not own. IMPLIES: the signInInTx caller
	// dispatches it only after inTx succeeds; a later failure here discards it.
	*createdOut = created
	actCtx := created.ActingContext(ctx)
	if _, err := h.users.AddIdentifierInTx(actCtx, tx, svc.AddIdentifierInput{
		UserID:   created.User.EntityID,
		Kind:     svc.IdentifierKindEmail,
		Value:    email,
		Primary:  true,
		Verified: id.EmailVerified,
	}); err != nil {
		return 0, 0, err
	}
	// A provider bound to a party confines the credential to it, exactly as
	// the link flow and the managed path do.
	var confinedTo *int64
	if policy.PartyID > 0 {
		party := policy.PartyID
		confinedTo = &party
	}
	linked, err := h.creds.LinkOIDCInTx(actCtx, tx, svc.LinkOIDCInput{
		PrincipalID:       created.User.EntityID,
		ProviderID:        policy.ProviderID,
		Issuer:            id.Issuer,
		Subject:           id.Subject,
		EmailAtLink:       emailAtLink(id),
		Label:             policy.ProviderID,
		ConfinedToPartyID: confinedTo,
	})
	if err != nil {
		return 0, 0, err
	}
	return created.User.EntityID, linked.CredentialID, nil
}

// createManagedUser creates the account of a managed just-in-time sign-in: a
// managed user of the provider's party, an active idp_jit party profile it acts
// as, and an oidc credential confined to the party. The IdP is the only
// authority here, so the creation is a trusted path that does not authorize a
// caller (there is none); the user, its profile and the credential are
// observed as the new profile, which is the only actor the section 10.1 rule
// leaves a managed user.
func (h *OIDCHandler) createManagedUser(ctx context.Context, tx pgx.Tx, id localauth.ExternalIdentity, policy localauth.ProviderPolicy) (principalID, credentialID int64, err error) {
	party := policy.PartyID
	cq := coredb.New(tx)
	if _, err := cq.GetLegalEntityByEntityID(ctx, party); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.ErrorContext(ctx, "oidc callback: provider party is not a legal entity", "provider", policy.ProviderID)
			return 0, 0, refuse(localauth.RedirectErrAuthFailed)
		}
		return 0, 0, fmt.Errorf("load provider party: %w", err)
	}
	userType, err := cq.GetTypeBySlug(ctx, userTypeSlug)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve user type: %w", err)
	}
	profileType, err := cq.GetTypeBySlug(ctx, profileTypeSlug)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve profile type: %w", err)
	}

	// The user and its profile are owned by the party; no actor exists yet to
	// stamp as creator.
	owner := pgtype.Int8{Int64: party, Valid: true}
	userEnt, err := cq.CreateEntity(ctx, coredb.CreateEntityParams{FundamentalTypeID: userType.ID, OwnerID: owner})
	if err != nil {
		return 0, 0, fmt.Errorf("create user entity: %w", err)
	}
	if _, err := cq.CreatePrincipal(ctx, userEnt.ID); err != nil {
		return 0, 0, fmt.Errorf("create principal: %w", err)
	}
	user, err := usersdb.New(tx).CreateUser(ctx, usersdb.CreateUserParams{EntityID: userEnt.ID, ManagingPartyID: owner})
	if err != nil {
		return 0, 0, fmt.Errorf("create managed user: %w", err)
	}
	profileEnt, err := cq.CreateEntity(ctx, coredb.CreateEntityParams{FundamentalTypeID: profileType.ID, OwnerID: owner})
	if err != nil {
		return 0, 0, fmt.Errorf("create profile entity: %w", err)
	}
	startedAt := profileEnt.CreatedAt.Time
	if _, err := cq.CreateProfile(ctx, coredb.CreateProfileParams{
		EntityID:  profileEnt.ID,
		HolderID:  userEnt.ID,
		PartyID:   owner,
		Status:    "active",
		Source:    profileSourceIdPJIT,
		StartedAt: &startedAt,
	}); err != nil {
		return 0, 0, fmt.Errorf("create idp_jit profile: %w", err)
	}

	// WHY no system-actor marker: the idp_jit profile just created is a real
	// (managed) user's party profile, and the IdP's assertion is not trusted
	// platform code acting as a system actor, so the context is built with
	// opctx.WithAuthContext and carries no session or credential yet.
	// IMPLIES: it is for this creation's observations and the credential link
	// only; the engine would gate it and refuse it (ACR 0, and the party's own
	// policy cannot be met without factors) if it reached an Authorize entry
	// point.
	actCtx := opctx.WithAuthContext(ctx, opctx.AuthContext{PrincipalID: userEnt.ID, ProfileID: profileEnt.ID})
	if h.obs != nil {
		userID := userEnt.ID
		if err := h.obs.Observe(actCtx, tx, "create", "user", &userID, nil, map[string]any{
			"uuid": userEnt.Uuid.String(), "is_guest": user.IsGuest, "managed": true,
		}); err != nil {
			return 0, 0, fmt.Errorf("observe user: %w", err)
		}
	}
	linked, err := h.creds.LinkOIDCInTx(actCtx, tx, svc.LinkOIDCInput{
		PrincipalID:       userEnt.ID,
		ProviderID:        policy.ProviderID,
		Issuer:            id.Issuer,
		Subject:           id.Subject,
		EmailAtLink:       emailAtLink(id),
		Label:             policy.ProviderID,
		ConfinedToPartyID: &party,
	})
	if err != nil {
		return 0, 0, err
	}
	return userEnt.ID, linked.CredentialID, nil
}

// jitNames chooses the given and family names of a just-in-time person record:
// the IdP's name claims, falling back to the email's local part and a
// placeholder family name.
func jitNames(id localauth.ExternalIdentity, canonEmail string) (given, family string) {
	given, family = id.GivenName, id.FamilyName
	if given == "" {
		given, _, _ = strings.Cut(canonEmail, "@")
	}
	if family == "" {
		family = unknownFamilyName
	}
	return given, family
}

// emailAtLink is the email recorded with a new oidc credential: the IdP's
// asserted email, canonicalized, or "" when it is missing or implausible.
func emailAtLink(id localauth.ExternalIdentity) string {
	email := svc.CanonicalizeEmail(id.Email)
	if len(email) > maxEmailAtLink {
		return ""
	}
	return email
}

// ---------------------------------------------------------------------------
// link and step-up callbacks
// ---------------------------------------------------------------------------

// sessionActingContext verifies the session a link or step-up state names and
// returns it with the context the flow's observations run under. The state is
// trusted only as far as it proves the browser started the flow: the session
// must still be live (unrevoked, unexpired, not an impersonation) and its
// principal must be the one the state recorded. The context is the section 10.1
// unauthenticated-flow AuthContext (personal profile, else oldest non-ended
// profile), with the affected session.
func (h *OIDCHandler) sessionActingContext(ctx context.Context, tx pgx.Tx, st localauth.StatePayload) (usersdb.Session, context.Context, error) {
	sessionUUID, err := uuid.Parse(st.SessionUUID)
	if err != nil {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}
	principalUUID, err := uuid.Parse(st.PrincipalUUID)
	if err != nil {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}
	sess, err := usersdb.New(tx).GetSessionByUUID(ctx, sessionUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}
	if err != nil {
		return usersdb.Session{}, ctx, fmt.Errorf("load session: %w", err)
	}
	if sess.RevokedAt != nil || !sess.ExpiresAt.Time.After(time.Now()) || sess.ImpersonatorSessionID.Valid {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}
	principal, err := coredb.New(tx).GetEntityByID(ctx, sess.PrincipalID)
	if err != nil {
		return usersdb.Session{}, ctx, fmt.Errorf("load session principal: %w", err)
	}
	if principal.Uuid != principalUUID {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}

	actCtx, ok, err := svc.UnauthenticatedActingContext(ctx, tx, sess.PrincipalID)
	if err != nil {
		return usersdb.Session{}, ctx, err
	}
	if !ok {
		return usersdb.Session{}, ctx, refuse(localauth.RedirectErrAuthFailed)
	}
	// WHY no system-actor marker: the link and step-up callbacks act for the
	// session's real principal, so the unauthenticated-flow context
	// (UnauthenticatedActingContext, never marked) only gains the affected
	// session id here.
	// IMPLIES: the engine gates it like any session context; it carries no ACR
	// or AuthTime, so it would be refused if it reached an Authorize entry
	// point, and it is used for the callback's observations only.
	if ac, found := opctx.Auth(actCtx); found {
		ac.SessionID = sess.ID
		actCtx = opctx.WithAuthContext(actCtx, ac)
	}
	return sess, actCtx, nil
}

// callbackLink adds the verified identity to the authenticated session's
// principal as an oidc credential. It never creates an account and never
// matches by email: the identity is bound to the principal that started the
// flow, and only a bring-your-own user can be that principal. Re-linking an
// identity the principal already holds succeeds without a write. A provider
// bound to a party confines the linked credential to that party, exactly as
// createManagedUser does; a provider with no party links unconfined.
func (h *OIDCHandler) callbackLink(w http.ResponseWriter, r *http.Request, providerID string, id localauth.ExternalIdentity, st localauth.StatePayload) {
	ctx := r.Context()
	policy, err := h.oauth.PolicyFor(providerID)
	if err != nil {
		h.finishCallbackError(w, r, providerID, "policy", err)
		return
	}
	var confinedTo *int64
	if policy.PartyID > 0 {
		party := policy.PartyID
		confinedTo = &party
	}
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		sess, actCtx, err := h.sessionActingContext(ctx, tx, st)
		if err != nil {
			return err
		}
		q := usersdb.New(tx)
		user, err := q.GetUserByEntityID(ctx, sess.PrincipalID)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(localauth.RedirectErrAuthFailed) // a service account
		}
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.IsGuest || user.ManagingPartyID.Valid {
			return refuse(localauth.RedirectErrAuthFailed)
		}

		existing, err := q.GetActiveOIDCByIssuerSubject(ctx, usersdb.GetActiveOIDCByIssuerSubjectParams{
			Issuer: id.Issuer, Subject: id.Subject,
		})
		switch {
		case err == nil:
			if existing.Credential.PrincipalID == sess.PrincipalID {
				return nil // already linked to this principal
			}
			return refuse(localauth.RedirectErrIdentityInUse)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("look up oidc credential: %w", err)
		}

		if _, err := h.creds.LinkOIDCInTx(actCtx, tx, svc.LinkOIDCInput{
			PrincipalID:       sess.PrincipalID,
			ProviderID:        providerID,
			Issuer:            id.Issuer,
			Subject:           id.Subject,
			EmailAtLink:       emailAtLink(id),
			Label:             providerID,
			ConfinedToPartyID: confinedTo,
		}); err != nil {
			if errors.Is(err, svc.ErrOIDCIdentityInUse) {
				return refuse(localauth.RedirectErrIdentityInUse)
			}
			return err
		}
		return nil
	})
	if err != nil {
		h.finishCallbackError(w, r, providerID, "link", err)
		return
	}
	slog.InfoContext(ctx, "oidc link succeeded", "provider", providerID)
	h.redirectOutcome(w, r, fragLinked, "1", st.ReturnPath)
}

// callbackStepUp adds the verified identity's oidc credential as a factor of
// the session that started the flow (decision U8): acr is recomputed (the IdP's
// asserted acr counts only with trust_acr), auth_time is set to now and an
// `update` `session` observation is written. The credential must be an active
// one of the session's own principal for this provider. The redirect carries no
// token; the tab takes a fresh access token from POST /v1/session/switch.
func (h *OIDCHandler) callbackStepUp(w http.ResponseWriter, r *http.Request, providerID string, id localauth.ExternalIdentity, st localauth.StatePayload) {
	ctx := r.Context()
	policy, err := h.oauth.PolicyFor(providerID)
	if err != nil {
		h.finishCallbackError(w, r, providerID, "policy", err)
		return
	}
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		sess, actCtx, err := h.sessionActingContext(ctx, tx, st)
		if err != nil {
			return err
		}
		q := usersdb.New(tx)
		row, err := q.GetActiveOIDCByIssuerSubject(ctx, usersdb.GetActiveOIDCByIssuerSubjectParams{
			Issuer: id.Issuer, Subject: id.Subject,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse(localauth.RedirectErrAuthFailed)
		}
		if err != nil {
			return fmt.Errorf("look up oidc credential: %w", err)
		}
		if row.Credential.PrincipalID != sess.PrincipalID || row.ProviderID != providerID {
			return refuse(localauth.RedirectErrAuthFailed)
		}
		if _, err := h.sessions.AddFactorInTx(actCtx, tx, sess.ID, svc.Factor{
			CredentialID: row.Credential.ID,
			AssertedACR:  policy.AssertedACR(id),
		}); err != nil {
			return err
		}
		if err := q.TouchCredentialLastUsed(ctx, row.Credential.ID); err != nil {
			return fmt.Errorf("touch credential: %w", err)
		}
		return nil
	})
	if err != nil {
		h.finishCallbackError(w, r, providerID, "step-up", err)
		return
	}
	slog.InfoContext(ctx, "oidc step-up succeeded", "provider", providerID)
	h.redirectOutcome(w, r, fragSteppedUp, "1", st.ReturnPath)
}

// ---------------------------------------------------------------------------
// authenticated starts (link and step-up)
// ---------------------------------------------------------------------------

// LinkStart handles POST /v1/me/credentials/oidc/{provider}/start: it mints
// the authorize URL of a link flow for the caller's session and answers
// {"authorize_url"} with the oidc_state cookie. Optional query `return` is a
// site-relative path the callback returns to.
//
// Error order: 400 for a malformed return path, 403 when the caller cannot link
// (a service account, a managed user, an impersonation), 409
// users.guest_upgrade_required for a guest, 404 for an unknown provider, then
// 409 users.step_up_required when the session's auth_time is older than
// AUTH_SELF_SERVICE_MAX_AUTH_AGE (decision U8).
func (h *OIDCHandler) LinkStart(w http.ResponseWriter, r *http.Request) {
	h.sessionStart(w, r, localauth.FlowLink)
}

// StepUpStart handles POST /v1/session/step-up/oidc/{provider}/start: it mints
// the authorize URL of a step-up flow for the caller's session. The session's
// principal must hold an active oidc credential for the provider; otherwise
// (and for an impersonation) the refusal is 409 users.step_up_required.
func (h *OIDCHandler) StepUpStart(w http.ResponseWriter, r *http.Request) {
	h.sessionStart(w, r, localauth.FlowStepUp)
}

func (h *OIDCHandler) sessionStart(w http.ResponseWriter, r *http.Request, flow localauth.Flow) {
	ctx := r.Context()
	id, ok := localauth.IdentityFromContext(ctx)
	if !ok {
		apiresp.WriteError(w, r, errors.New("oidc session start mounted outside RequireAuth"))
		return
	}
	if id.SessionID == 0 {
		// A client-credentials caller has no session to link or step up.
		apiresp.WriteError(w, r, apiresp.ErrForbidden)
		return
	}
	providerID := normalizeProviderID(r)

	returnPath := r.URL.Query().Get("return")
	if err := localauth.ValidateReturnPath(returnPath); err != nil {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "return", Code: "users.return_path_invalid", Message: "return must be a site-relative path",
		}))
		return
	}

	if id.ImpersonatorPrincipalID != 0 {
		if flow == localauth.FlowStepUp {
			writeError(w, r, svc.ErrStepUpRequired) // an impersonation cannot step up
			return
		}
		apiresp.WriteError(w, r, apiresp.ErrForbidden)
		return
	}
	if flow == localauth.FlowLink {
		if err := h.requireLinkable(ctx, id); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if !h.oauth.ProviderAvailable(providerID) {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	if err := h.checkStartGate(ctx, id, flow, providerID); err != nil {
		writeError(w, r, err)
		return
	}

	authURL, state, err := h.oauth.SessionAuthorizeURL(providerID, flow, id.SessionUUID.String(), id.PrincipalUUID.String(), returnPath)
	if err != nil {
		h.writeStartError(w, r, providerID, err)
		return
	}
	h.writeAuthorizeURL(w, r, authURL, state)
}

// requireLinkable refuses a caller that cannot hold a linked oidc credential:
// a service account or a managed user (403), a guest (409
// users.guest_upgrade_required).
func (h *OIDCHandler) requireLinkable(ctx context.Context, id *localauth.Identity) error {
	if id.PrincipalKind != localauth.PrincipalKindUser {
		return apiresp.ErrForbidden
	}
	if id.IsGuest {
		return svc.ErrGuestMustUpgrade
	}
	user, err := h.q.GetUserByEntityID(ctx, id.PrincipalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrForbidden
		}
		return fmt.Errorf("oidc link start: load user: %w", err)
	}
	if user.ManagingPartyID.Valid {
		return apiresp.ErrForbidden
	}
	return nil
}

// checkStartGate runs the state-dependent gate of a session flow: a link needs
// a session authenticated within AUTH_SELF_SERVICE_MAX_AUTH_AGE; a step-up
// needs an active oidc credential for the provider.
func (h *OIDCHandler) checkStartGate(ctx context.Context, id *localauth.Identity, flow localauth.Flow, providerID string) error {
	switch flow {
	case localauth.FlowLink:
		if h.cfg != nil {
			if maxAge := h.cfg.Auth.SelfServiceMaxAuthAge; maxAge > 0 && time.Since(id.AuthTime) > maxAge {
				return svc.ErrStepUpRequired
			}
		}
	case localauth.FlowStepUp:
		held, err := h.q.ListActiveOIDCByPrincipal(ctx, id.PrincipalID)
		if err != nil {
			return fmt.Errorf("oidc step-up start: list oidc credentials: %w", err)
		}
		for _, c := range held {
			if c.ProviderID == providerID {
				return nil
			}
		}
		return svc.ErrStepUpRequired
	}
	return nil
}
