// Package handlers — oidc_providers.go adds per-provider CRUD on top of
// the /oidc-config/confirm onboarding flow (phase 9.11a). The DB table
// oidc_providers is an override layer: NULL fields mean "use env /
// well-known default", non-NULL fields mean "operator edited via the
// admin GUI". Every successful write rebuilds OAuth and refreshes the
// cached boot state so /status and /v1/* see the new config live.
//
// Auth: admin session OR valid setup token (same seam as /confirm).
// client_secret is NEVER returned in any response — has_client_secret
// is surfaced instead. PUT with the field absent keeps the existing
// secret; PUT with empty string clears it.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/server"
	db "github.com/moduleforge/mod-users/model/db"
)

// providerIDPattern matches the slug form required by both the env-var
// convention and the migration CHECK constraint. Must stay in sync with
// model/migrations/0108_oidc_providers.sql.
var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// OIDCProvidersQuerier is the narrow subset of db.Querier the provider
// CRUD path uses. Kept separate from OIDCConfigQuerier so tests for each
// flow can supply their own fake without bringing in the other's methods.
type OIDCProvidersQuerier interface {
	GetOIDCProvider(ctx context.Context, id string) (db.OidcProvider, error)
	ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error)
	UpsertOIDCProvider(ctx context.Context, arg db.UpsertOIDCProviderParams) (db.OidcProvider, error)
	DeleteOIDCProvider(ctx context.Context, id string) (int64, error)
}

// ProvidersDeps bundles the collaborators the provider handlers need.
// Kept separate from OIDCConfigDeps so the two flows can be exercised in
// isolation by tests.
type ProvidersDeps struct {
	Queries      OIDCProvidersQuerier
	EnvRegistry  config.ProviderRegistry
	OAuth        *auth.OAuth
	RedirectBase string
	// Confirmer wires the existing OIDCConfigHandler so provider writes
	// can reuse its authorize + refresh-state plumbing. Required.
	Confirmer *OIDCConfigHandler
}

// ProvidersHandler serves GET/PUT/POST/DELETE under
// /v1/oidc-config/providers/*. All methods are safe for concurrent use.
type ProvidersHandler struct {
	deps ProvidersDeps
}

// NewProvidersHandler wires a handler. Confirmer is required (provider
// writes are paired with a boot-state refresh so the GUI sees the new
// init_ok + error without a process restart).
func NewProvidersHandler(deps ProvidersDeps) *ProvidersHandler {
	return &ProvidersHandler{deps: deps}
}

// ----- Response shapes -------------------------------------------------

// providerView is the GET-shape. Each field surfaces the DB override
// (nullable), a *_default companion holding the env-or-well-known
// fallback (for grey placeholders when the override is cleared), and
// a *_source label so the GUI can tell the operator exactly where the
// currently-effective value came from. Source values:
//
//	"db"         — DB override is the effective value
//	"env"        — env var provides the effective value
//	"well_known" — hardcoded well-known default (e.g. google/microsoft)
//	"fallback"   — last-resort (e.g. title-cased id for display_name)
//	"none"       — no value available from any layer (e.g. missing client_id)
type providerView struct {
	ID string `json:"id"`

	DisplayName        *string `json:"display_name"`
	DisplayNameDefault *string `json:"display_name_default"`
	DisplayNameSource  string  `json:"display_name_source"`

	IssuerURL        *string `json:"issuer_url"`
	IssuerURLDefault *string `json:"issuer_url_default"`
	IssuerURLSource  string  `json:"issuer_url_source"`

	ClientID        *string `json:"client_id"`
	ClientIDDefault *string `json:"client_id_default"`
	ClientIDSource  string  `json:"client_id_source"`

	HasClientSecret    bool   `json:"has_client_secret"`
	ClientSecretSource string `json:"client_secret_source"`

	ClaimStyle        *string `json:"claim_style"`
	ClaimStyleDefault *string `json:"claim_style_default"`
	ClaimStyleSource  string  `json:"claim_style_source"`

	Scopes        []string `json:"scopes"`
	ScopesDefault []string `json:"scopes_default"`
	ScopesSource  string   `json:"scopes_source"`

	Enabled bool `json:"enabled"`

	InitOK bool   `json:"init_ok"`
	Error  string `json:"error,omitempty"`

	CallbackURL string `json:"callback_url"`
	WellKnown   bool   `json:"well_known"`
}

// ----- Handlers --------------------------------------------------------

// Get handles GET /v1/oidc-config/providers/:id. Requires admin or the
// X-Setup-Token header (the body-less Get borrows the Confirmer's credential
// helper). Order: credential (401), then id pattern (400), then lookup (404).
func (h *ProvidersHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Confirmer.authorizeBodyless(w, r, "oidc provider get") {
		return
	}
	id := strings.ToLower(chi.URLParam(r, "id"))
	if !providerIDPattern.MatchString(id) {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	view, ok, err := h.buildView(r.Context(), id)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider get %q: %w", id, err))
		return
	}
	if !ok {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	server.JSON(w, http.StatusOK, view)
}

// Update handles PUT /v1/oidc-config/providers/:id.
// Semantics for each field:
//   - field absent  → keep existing DB value (or NULL if never set)
//   - field null    → clear override (fall through to env / default)
//   - empty string  → for client_secret, clear; for other strings, clear
//   - non-empty     → store as override
//
// Scopes: absent → keep; null → clear (env); empty array → clear; non-empty → store.
// Enabled: absent → keep existing or default true; present → store.
func (h *ProvidersHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Parse body into a map so we can distinguish "field absent" from
	// "field present with null". strict JSON decode into a typed struct
	// would collapse both cases.
	//
	// Error order: see OIDCConfigHandler.authenticateWithBody (credential in
	// the body; documented exception to 401-before-400), then the id pattern.
	var raw map[string]json.RawMessage
	if !h.authenticateWithBody(w, r, "oidc provider update", &raw) {
		return
	}
	id := strings.ToLower(chi.URLParam(r, "id"))
	if !providerIDPattern.MatchString(id) {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	h.upsertAndRespond(w, r, id, raw, false)
}

// createRequest carries the fields for POST. ID is mandatory here; the
// rest share the PUT semantics.
type createRequest struct {
	ID string `json:"id"`
}

// Create handles POST /v1/oidc-config/providers. Body must include a
// unique, slug-validated id plus any editable fields (same shape as PUT).
// 409 if the id already exists in the DB; the caller can then PUT.
func (h *ProvidersHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Error order: see OIDCConfigHandler.authenticateWithBody, then the
	// stateless id checks, then the state-dependent 409.
	var raw map[string]json.RawMessage
	if !h.authenticateWithBody(w, r, "oidc provider create", &raw) {
		return
	}

	rawID := strings.TrimSpace(stringField(raw, "id"))
	if rawID == "" {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	// Slug validation runs on the raw input so uppercase or underscores
	// are rejected rather than silently normalized. The environment-var
	// convention (AUTH_PROVIDER_{ID}_*) is lowercased when loaded, but
	// operator-typed ids must already match the canonical shape.
	if !providerIDPattern.MatchString(rawID) {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	id := rawID

	// 409 if a DB override row already exists — the correct follow-up
	// is a PUT. We don't block on env-only ids (the operator may want
	// to override an env-declared provider for the first time); a PUT
	// handles that path.
	if _, err := h.deps.Queries.GetOIDCProvider(r.Context(), id); err == nil {
		apiresp.WriteError(w, r, apiresp.Conflict(apiresp.FieldError{
			Field:   "id",
			Code:    "users.oidc_provider_exists",
			Message: "provider id already exists; use PUT to update",
		}))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider create preflight %q: %w", id, err))
		return
	}

	h.upsertAndRespond(w, r, id, raw, true)
}

// Revert handles DELETE /v1/oidc-config/providers/:id. The DB row is
// removed; env + well-known defaults take over on the next merge.
// Returns 204 regardless of whether a row existed (idempotent) — the GUI
// will refetch the list to reflect the post-revert state.
func (h *ProvidersHandler) Revert(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Confirmer.authorizeBodyless(w, r, "oidc provider delete") {
		return
	}
	id := strings.ToLower(chi.URLParam(r, "id"))
	if !providerIDPattern.MatchString(id) {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

	if _, err := h.deps.Queries.DeleteOIDCProvider(r.Context(), id); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider delete %q: %w", id, err))
		return
	}

	if err := h.rebuildAndRefresh(r.Context()); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider delete: rebuild: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ----- Internal helpers -----------------------------------------------

// upsertAndRespond handles the shared PUT/POST body: merge incoming
// fields with the existing row, write, rebuild, refresh, respond with
// the GET-shape.
func (h *ProvidersHandler) upsertAndRespond(w http.ResponseWriter, r *http.Request, id string, raw map[string]json.RawMessage, creating bool) {
	// Load existing row (if any) so "field absent" means "keep existing"
	// and client_secret is preserved unless explicitly overwritten.
	var existing db.OidcProvider
	existingLoaded := false
	if row, err := h.deps.Queries.GetOIDCProvider(r.Context(), id); err == nil {
		existing = row
		existingLoaded = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider upsert preflight %q: %w", id, err))
		return
	}

	params := db.UpsertOIDCProviderParams{ID: id}

	params.DisplayName = mergeTextField(raw, "display_name", existing.DisplayName, existingLoaded)
	params.IssuerUrl = mergeTextField(raw, "issuer_url", existing.IssuerUrl, existingLoaded)
	params.ClientID = mergeTextField(raw, "client_id", existing.ClientID, existingLoaded)
	params.ClientSecret = mergeSecretField(raw, "client_secret", existing.ClientSecret, existingLoaded)
	params.ClaimStyle = mergeTextField(raw, "claim_style", existing.ClaimStyle, existingLoaded)
	params.Scopes = mergeScopes(raw, existing.Scopes, existingLoaded)

	enabled, enabledSet := boolField(raw, "enabled")
	switch {
	case enabledSet:
		params.Enabled = enabled
	case existingLoaded:
		params.Enabled = existing.Enabled
	default:
		// New row with no explicit enabled → default ON (DB default).
		params.Enabled = true
	}

	if _, err := h.deps.Queries.UpsertOIDCProvider(r.Context(), params); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider upsert %q: %w", id, err))
		return
	}

	if err := h.rebuildAndRefresh(r.Context()); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider upsert %q: rebuild: %w", id, err))
		return
	}

	// Respond with the merged view so the GUI sees init_ok + error live.
	view, ok, err := h.buildView(r.Context(), id)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider upsert %q: post-write view: %w", id, err))
		return
	}
	if !ok {
		// Paranoia: we just wrote it.
		apiresp.WriteError(w, r, fmt.Errorf("oidc provider upsert %q: provider disappeared after write", id))
		return
	}
	status := http.StatusOK
	if creating {
		status = http.StatusCreated
	}
	server.JSON(w, status, view)
}

// buildView loads the merged view for one provider. Returns (view, true,
// nil) on success, (_, false, nil) if the id is not known anywhere
// (unknown env, no DB row, not well-known), (_, _, err) on DB fault.
func (h *ProvidersHandler) buildView(ctx context.Context, id string) (providerView, bool, error) {
	merged, err := config.LoadMergedProviders(ctx, h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		return providerView{}, false, err
	}
	m, ok := merged[id]
	if !ok {
		return providerView{}, false, nil
	}

	view := providerView{
		ID:              id,
		HasClientSecret: m.HasClientSecret,
		Scopes:          overrideScopes(m.DBOverride),
		ScopesDefault:   scopesDefault(m),
		Enabled:         m.MergedEnabled(),
		CallbackURL:     buildCallbackURL(h.deps.RedirectBase, id),
		WellKnown:       m.WellKnownDefaults != nil,
	}
	view.DisplayName = overrideTextPointer(m.DBOverride, func(r *config.DBProviderRow) pgtype.Text { return r.DisplayName })
	view.DisplayNameDefault = nonEmptyPointer(defaultDisplayName(m))
	view.DisplayNameSource = displayNameSource(m)

	view.IssuerURL = overrideTextPointer(m.DBOverride, func(r *config.DBProviderRow) pgtype.Text { return r.IssuerURL })
	view.IssuerURLDefault = nonEmptyPointer(defaultIssuerURL(m))
	view.IssuerURLSource = issuerURLSource(m)

	view.ClientID = overrideTextPointer(m.DBOverride, func(r *config.DBProviderRow) pgtype.Text { return r.ClientID })
	view.ClientIDDefault = nonEmptyPointer(defaultClientID(m))
	view.ClientIDSource = clientIDSource(m)

	view.ClientSecretSource = clientSecretSource(m)

	view.ClaimStyle = overrideTextPointer(m.DBOverride, func(r *config.DBProviderRow) pgtype.Text { return r.ClaimStyle })
	view.ClaimStyleDefault = nonEmptyPointer(defaultClaimStyle(m))
	view.ClaimStyleSource = claimStyleSource(m)

	view.ScopesSource = scopesSource(m)

	// Per-provider InitOK + Err from OAuth registry. A provider not
	// present in OAuth (e.g. new DB row not yet rebuilt) reads InitOK
	// false with empty error — the GUI treats that as "pending reload".
	for _, s := range h.deps.OAuth.AllProviders() {
		if s.ID == id {
			view.InitOK = s.InitOK
			if s.Err != nil {
				view.Error = s.Err.Error()
			}
			break
		}
	}
	return view, true, nil
}

// rebuildAndRefresh re-merges env + DB, rebuilds the OAuth registry, and
// refreshes the onboarding handler's cached state. Shared between PUT,
// POST, and DELETE.
func (h *ProvidersHandler) rebuildAndRefresh(ctx context.Context) error {
	merged, err := config.LoadMergedProviders(ctx, h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		return err
	}
	registry := config.MergedRegistry(merged)
	if err := h.deps.OAuth.Rebuild(ctx, registry); err != nil {
		return err
	}
	return h.deps.Confirmer.RefreshState(ctx)
}

// authenticateWithBody runs the shared credential-in-body gate for provider
// Create and Update (see OIDCConfigHandler.authenticateWithBody for the
// order and the documented 400-before-401 exception). On success *raw holds
// the decoded body. It returns false when it has already written the
// response.
func (h *ProvidersHandler) authenticateWithBody(w http.ResponseWriter, r *http.Request, op string, raw *map[string]json.RawMessage) bool {
	return h.deps.Confirmer.authenticateWithBody(w, r, op,
		func() bool {
			body, ok := parseBody(w, r)
			if ok {
				*raw = body
			}
			return ok
		},
		func() string { return stringField(*raw, "setup_token") },
	)
}

// ----- Body parsing helpers ------------------------------------------

// providerBodyKeys is the allowlist of top-level keys the provider handlers
// read: the credential (setup_token), the path/slug (id; Update ignores it in
// favor of the path id, as before), and the fields upsertAndRespond merges
// (mergeTextField/mergeSecretField/mergeScopes/boolField). Any other key is a
// 400, matching DecodeJSON's unknown-field rejection for typed bodies.
var providerBodyKeys = map[string]struct{}{
	"setup_token":   {},
	"id":            {},
	"display_name":  {},
	"issuer_url":    {},
	"client_id":     {},
	"client_secret": {},
	"claim_style":   {},
	"scopes":        {},
	"enabled":       {},
}

// parseBody reads the request body once into a map so caller helpers can
// distinguish absent / null / value per field. A zero-byte body is an empty
// map (so a bodyless PUT stays accepted); anything else must be a single JSON
// object within apiresp.DefaultMaxBodyBytes whose top-level keys are all in
// providerBodyKeys. On failure parseBody has already written the 400
// response and returns ok == false; callers must just return.
//
// The emptiness check peeks one byte (bounded by the cap) and restores it, so
// the body is decoded in a single pass by server.Decode, which rejects
// trailing data and over-cap bodies with DecodeJSON's detail codes.
func parseBody(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	if r.Body == nil {
		return map[string]json.RawMessage{}, true
	}
	orig := r.Body
	defer orig.Close()
	capped := http.MaxBytesReader(w, orig, apiresp.DefaultMaxBodyBytes)
	var first [1]byte
	n, err := io.ReadFull(capped, first[:])
	if n == 0 && errors.Is(err, io.EOF) {
		return map[string]json.RawMessage{}, true
	}
	// Restore the peeked byte (and any read error) in front of the rest.
	var head io.Reader = bytes.NewReader(first[:n])
	if err != nil {
		head = io.MultiReader(head, errReader{err})
	}
	r.Body = io.NopCloser(io.MultiReader(head, capped))

	var raw map[string]json.RawMessage
	if !server.Decode(w, r, &raw) {
		return nil, false
	}
	// Report the lexicographically first unknown key so the response is
	// deterministic and bounded (one detail, as DecodeJSON reports).
	unknown := ""
	found := false
	for k := range raw {
		if _, ok := providerBodyKeys[k]; ok {
			continue
		}
		if !found || k < unknown {
			unknown, found = k, true
		}
	}
	if found {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field:   unknown,
			Code:    "invalid_json_body",
			Message: "request body contains an unknown field",
		}))
		return nil, false
	}
	return raw, true
}

// errReader returns its error on every Read; it replays a read error seen
// while peeking so the decoder still observes it.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// stringField returns the string at key, or "" if absent / null / wrong type.
func stringField(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

// boolField returns (value, true) if the key is present and is a bool;
// otherwise (false, false).
func boolField(raw map[string]json.RawMessage, key string) (bool, bool) {
	v, ok := raw[key]
	if !ok {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(v, &b); err != nil {
		return false, false
	}
	return b, true
}

// mergeTextField implements the PUT/POST merge semantics for a non-secret
// nullable text field:
//   - key absent        → keep existing (or NULL if never set)
//   - key null          → clear (NULL)
//   - string value ""   → clear (NULL)
//   - non-empty string  → store
func mergeTextField(raw map[string]json.RawMessage, key string, existing pgtype.Text, existingLoaded bool) pgtype.Text {
	v, present := raw[key]
	if !present {
		if existingLoaded {
			return existing
		}
		return pgtype.Text{}
	}
	// Present: decode to see whether it's null or a string.
	if isJSONNull(v) {
		return pgtype.Text{}
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		// Unknown shape → keep existing as a safe fallback.
		if existingLoaded {
			return existing
		}
		return pgtype.Text{}
	}
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// mergeSecretField implements the client_secret merge semantics — same
// as mergeTextField but called out so the intent is explicit where it
// matters most:
//   - absent       → KEEP existing secret (never overwrite)
//   - null / ""    → CLEAR the secret
//   - non-empty    → replace
func mergeSecretField(raw map[string]json.RawMessage, key string, existing pgtype.Text, existingLoaded bool) pgtype.Text {
	return mergeTextField(raw, key, existing, existingLoaded)
}

// mergeScopes implements the same precedence for the scopes array:
//   - key absent         → keep existing (or NULL)
//   - key null           → NULL (clear override)
//   - empty array        → NULL (clear override — matches operator intent)
//   - non-empty array    → store as-is
//
// Returning nil ([]string(nil)) makes sqlc/pgx store SQL NULL, which is
// what the merge layer treats as "no opinion". An empty slice would be
// serialized as an empty array which pgx stores as NULL as well; we
// normalize to nil for consistency.
func mergeScopes(raw map[string]json.RawMessage, existing []string, existingLoaded bool) []string {
	v, present := raw["scopes"]
	if !present {
		if existingLoaded {
			return existing
		}
		return nil
	}
	if isJSONNull(v) {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err != nil {
		if existingLoaded {
			return existing
		}
		return nil
	}
	if len(arr) == 0 {
		return nil
	}
	// Trim and drop empty entries to match the env-scope parser.
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isJSONNull reports whether the raw JSON value is the literal null.
func isJSONNull(v json.RawMessage) bool {
	return string(v) == "null"
}

// ----- View helpers ---------------------------------------------------

// overrideTextPointer pulls the DB override value (if set) for one of
// the text fields. Returns nil when no DB row exists or the field is
// NULL, which the JSON encoder renders as JSON null.
func overrideTextPointer(row *config.DBProviderRow, pick func(*config.DBProviderRow) pgtype.Text) *string {
	if row == nil {
		return nil
	}
	t := pick(row)
	if !t.Valid || t.String == "" {
		return nil
	}
	s := t.String
	return &s
}

// overrideScopes returns the DB-override scopes (or nil).
func overrideScopes(row *config.DBProviderRow) []string {
	if row == nil || !row.ScopesSet {
		return nil
	}
	if len(row.Scopes) == 0 {
		return nil
	}
	out := make([]string, len(row.Scopes))
	copy(out, row.Scopes)
	return out
}

// scopesDefault computes the effective-if-override-cleared scope set.
// Precedence: env scopes → default OIDC scopes. We intentionally do NOT
// include DB-override scopes here — the *_default fields exist to show
// the operator what "clearing" would reveal.
func scopesDefault(m *config.MergedProvider) []string {
	if m.EnvValues != nil && len(m.EnvValues.Scopes) > 0 {
		out := make([]string, len(m.EnvValues.Scopes))
		copy(out, m.EnvValues.Scopes)
		return out
	}
	// defaultScopes is a package-internal in config; we reproduce the
	// canonical set here to avoid exporting mutable state.
	return []string{"openid", "email", "profile"}
}

// defaultDisplayName / defaultIssuerURL / defaultClientID / defaultClaimStyle
// compute the effective-if-override-cleared value for one field. They
// intentionally skip the DB layer so the GUI shows the operator what
// "clearing" would reveal.
func defaultDisplayName(m *config.MergedProvider) string {
	if m.EnvValues != nil && m.EnvValues.DisplayName != "" {
		return m.EnvValues.DisplayName
	}
	if m.WellKnownDefaults != nil && m.WellKnownDefaults.DisplayName != "" {
		return m.WellKnownDefaults.DisplayName
	}
	// Last resort: title-cased id.
	return defaultTitleCase(m.ID)
}

func defaultIssuerURL(m *config.MergedProvider) string {
	if m.EnvValues != nil && m.EnvValues.IssuerURL != "" {
		return m.EnvValues.IssuerURL
	}
	if m.WellKnownDefaults != nil {
		return m.WellKnownDefaults.IssuerURL
	}
	return ""
}

func defaultClientID(m *config.MergedProvider) string {
	if m.EnvValues != nil {
		return m.EnvValues.ClientID
	}
	return ""
}

func defaultClaimStyle(m *config.MergedProvider) string {
	if m.EnvValues != nil && m.EnvValues.ClaimStyle != "" {
		return m.EnvValues.ClaimStyle
	}
	if m.WellKnownDefaults != nil {
		return m.WellKnownDefaults.ClaimStyle
	}
	return ""
}

// defaultTitleCase mirrors the config.titleCase helper (which is
// package-private). Kept trivial so exported behavior stays in one place
// and this handler doesn't need to import an unexported symbol.
func defaultTitleCase(id string) string {
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// nonEmptyPointer returns a pointer to s when non-empty; nil otherwise,
// rendered as JSON null.
func nonEmptyPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Source-attribution helpers. Each reports which configuration layer
// provides the currently-effective value for a field, using the same
// priority cascade the merge layer applies (DB → env → well-known →
// fallback/none). The strings are a closed vocabulary consumed by the
// GUI; see providerView.*Source documentation.

const (
	sourceDB        = "db"
	sourceEnv       = "env"
	sourceWellKnown = "well_known"
	sourceFallback  = "fallback"
	sourceNone      = "none"
)

func dbTextSet(t pgtype.Text) bool {
	return t.Valid && t.String != ""
}

func displayNameSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && dbTextSet(m.DBOverride.DisplayName) {
		return sourceDB
	}
	if m.EnvValues != nil && m.EnvValues.DisplayName != "" {
		return sourceEnv
	}
	if m.WellKnownDefaults != nil && m.WellKnownDefaults.DisplayName != "" {
		return sourceWellKnown
	}
	// defaultDisplayName falls back to the title-cased id, which we
	// label "fallback" rather than pretending no value exists.
	return sourceFallback
}

func issuerURLSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && dbTextSet(m.DBOverride.IssuerURL) {
		return sourceDB
	}
	if m.EnvValues != nil && m.EnvValues.IssuerURL != "" {
		return sourceEnv
	}
	if m.WellKnownDefaults != nil && m.WellKnownDefaults.IssuerURL != "" {
		return sourceWellKnown
	}
	return sourceNone
}

func clientIDSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && dbTextSet(m.DBOverride.ClientID) {
		return sourceDB
	}
	if m.EnvValues != nil && m.EnvValues.ClientID != "" {
		return sourceEnv
	}
	return sourceNone
}

func clientSecretSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && dbTextSet(m.DBOverride.ClientSecret) {
		return sourceDB
	}
	if m.EnvValues != nil && m.EnvValues.ClientSecret != "" {
		return sourceEnv
	}
	return sourceNone
}

func claimStyleSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && dbTextSet(m.DBOverride.ClaimStyle) {
		return sourceDB
	}
	if m.EnvValues != nil && m.EnvValues.ClaimStyle != "" {
		return sourceEnv
	}
	if m.WellKnownDefaults != nil && m.WellKnownDefaults.ClaimStyle != "" {
		return sourceWellKnown
	}
	return sourceNone
}

func scopesSource(m *config.MergedProvider) string {
	if m.DBOverride != nil && len(m.DBOverride.Scopes) > 0 {
		return sourceDB
	}
	if m.EnvValues != nil && len(m.EnvValues.Scopes) > 0 {
		return sourceEnv
	}
	// Every provider gets the built-in "openid email profile" default
	// from the merge layer when no explicit value is set. We label that
	// "well_known" to keep the vocabulary closed and the GUI display
	// simple — the operator sees "Source: well-known default" which is
	// accurate.
	return sourceWellKnown
}

// buildCallbackURL is the public-facing callback URL for one provider.
// Mirrors auth.buildCallbackURL but that helper is package-private, so
// we reproduce the trivial join rather than exporting an implementation
// detail from the auth package.
func buildCallbackURL(base, providerID string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/v1/auth/oidc/" + providerID + "/callback"
}
