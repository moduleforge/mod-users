// Package auth provides the HTTP handlers of the public /v1/auth route group:
// registration, password, email-code and guest sign-in, password reset, the
// token endpoint (refresh and client credentials), the OIDC handoff exchange
// and the provider list. Every sign-in answers with the token response of
// service.TokenResponse; no handler reads or sets an authentication cookie.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Sender is the email-sending interface the password-reset flow uses. It
// matches email.Sender without importing it.
type Sender interface {
	Send(ctx context.Context, to, subject, textBody string) error
}

// Handler serves the /v1/auth routes. Construct it with New.
type Handler struct {
	pool       *pgxpool.Pool
	q          usersdb.Querier
	coreQ      coredb.Querier
	users      *svc.UserService
	creds      *svc.CredentialService
	challenges *svc.ChallengeService
	sessions   *svc.SessionService
	oauth      *localauth.OAuth
	sender     Sender
	guiBase    string

	// Bound on detached email work (codes, reset links); see dispatch. The
	// zero value selects the default concurrency.
	emailConcurrency int
	emailSlotsOnce   sync.Once
	emailSlots       chan struct{}
}

// New constructs the /v1/auth Handler. pool, q and coreQ are the module's
// connection pool and its queriers over it; the services are the phase-6
// identity services. oauth lists the enabled OIDC providers for GET
// /providers and may be nil (no providers). sender delivers password-reset
// links; guiBase is the GUI origin those links point to.
func New(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	users *svc.UserService,
	creds *svc.CredentialService,
	challenges *svc.ChallengeService,
	sessions *svc.SessionService,
	oauth *localauth.OAuth,
	sender Sender,
	guiBase string,
) *Handler {
	return &Handler{
		pool: pool, q: q, coreQ: coreQ, users: users, creds: creds,
		challenges: challenges, sessions: sessions, oauth: oauth,
		sender: sender, guiBase: guiBase,
	}
}

// writeError renders err: a service.AuthError with its own code and status,
// anything else through apiresp.WriteError (sentinels, field errors and the
// action-required envelope; a 5xx never echoes err).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *svc.AuthError
	if errors.As(err, &ae) {
		apiresp.WriteJSON(w, ae.Status, ae.Envelope())
		return
	}
	apiresp.WriteError(w, r, err)
}

// parseOptionalUUID parses an optional uuid field; "" is uuid.Nil.
func parseOptionalUUID(field, value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, apiresp.InvalidInput(apiresp.FieldError{
			Field: field, Code: "users." + field + "_invalid", Message: field + " must be a uuid",
		})
	}
	return id, nil
}

// required returns one field error per empty value, in order.
func required(fields ...[2]string) []apiresp.FieldError {
	var details []apiresp.FieldError
	for _, f := range fields {
		if f[1] == "" {
			details = append(details, apiresp.FieldError{
				Field: f[0], Code: "users." + f[0] + "_required", Message: f[0] + " is required",
			})
		}
	}
	return details
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (h *Handler) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if h.pool == nil {
		return errors.New("auth: no database pool")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: commit: %w", err)
	}
	return nil
}

// emailWorkTimeout bounds one detached background email job (lookup, code
// or token minting, and delivery).
const emailWorkTimeout = 20 * time.Second

// defaultEmailConcurrency bounds concurrent detached email jobs per Handler.
const defaultEmailConcurrency = 16

// emailSlotChan returns the per-Handler semaphore, creating it on first use.
func (h *Handler) emailSlotChan() chan struct{} {
	h.emailSlotsOnce.Do(func() {
		n := h.emailConcurrency
		if n <= 0 {
			n = defaultEmailConcurrency
		}
		h.emailSlots = make(chan struct{}, n)
	})
	return h.emailSlots
}

// dispatch runs work in the background on a context detached from the
// request's cancellation, bounded in time and in concurrency. When every
// slot is busy the job is dropped (logged) so the response is unaffected;
// callers use it for anti-enumeration endpoints whose answer must not depend
// on the work.
func (h *Handler) dispatch(r *http.Request, name string, work func(ctx context.Context)) {
	slots := h.emailSlotChan()
	select {
	case slots <- struct{}{}:
	default:
		slog.WarnContext(r.Context(), "auth: background email capacity saturated, dropping job", "job", name)
		return
	}
	parent := context.WithoutCancel(r.Context())
	go func() {
		defer func() { <-slots }()
		runBounded(parent, emailWorkTimeout, name, work)
	}()
}

// runBounded runs work under a deadline and recovers from a panic, logging
// only the panic type (never its value, which could carry a code or address).
func runBounded(parent context.Context, timeout time.Duration, name string, work func(context.Context)) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			slog.ErrorContext(ctx, "auth: recovered panic in background job", "job", name, "panic_type", fmt.Sprintf("%T", rec))
		}
	}()
	work(ctx)
}

// antiEnumerationFloor is the minimum latency of the endpoints that must not
// reveal whether an email is registered.
const antiEnumerationFloor = 200 * time.Millisecond

// answerAfterFloor writes 204 once the anti-enumeration floor has passed.
func answerAfterFloor(w http.ResponseWriter, r *http.Request, start time.Time) {
	timer := time.NewTimer(time.Until(start.Add(antiEnumerationFloor)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// providerEntry is the browser-safe view of an OIDC provider.
type providerEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// Providers handles GET /v1/auth/providers: the enabled OIDC providers, for
// the sign-in page's buttons. Only successfully initialized providers are
// listed, and only their id and display name.
func (h *Handler) Providers(w http.ResponseWriter, _ *http.Request) {
	out := []providerEntry{}
	if h.oauth != nil {
		for _, s := range h.oauth.EnabledProviders() {
			out = append(out, providerEntry{ID: s.Provider.ID, DisplayName: s.Provider.DisplayName})
		}
	}
	apiresp.WriteJSON(w, http.StatusOK, out)
}
