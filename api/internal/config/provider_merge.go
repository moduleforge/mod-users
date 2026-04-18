// Package config — provider_merge layers DB overrides (oidc_providers
// table) on top of env-declared providers to produce the effective
// Provider registry the OAuth orchestrator consumes.
//
// Precedence (highest-wins) for each editable field:
//
//  1. DB override row (NULL column = "no opinion")
//  2. Env value (AUTH_PROVIDER_{ID}_*)
//  3. Well-known default (google / microsoft)
//  4. Last-resort fallback for DisplayName (title-case of id)
//
// Scopes follow the same cascade with a baseline of
// []string{"openid","email","profile"} at the lowest tier.
//
// A provider appears in the effective registry iff:
//   - it has a resolvable issuer_url AND client_id (so OAuth init has
//     something to do), AND
//   - it's not explicitly disabled by the DB row.
//
// The previous confirm-flow provider_enabled map (JSONB in oidc_config)
// is applied as a second filter by the caller — this package stays
// focused on the env+overrides merge.
package config

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/moduleforge/users-module/model/db"
)

// OIDCProviderQuerier is the narrow subset of db.Querier needed to load
// provider override rows. Declared as a small interface so handler/tests
// can supply an in-memory fake without importing pgx.
type OIDCProviderQuerier interface {
	ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error)
}

// DBProviderRow carries the editable override fields in the same nullable
// shape sqlc produces, but package-local so the merge layer doesn't leak
// pgtype into higher layers beyond what's unavoidable. It's a thin alias
// over db.OidcProvider trimmed to the override-relevant columns.
type DBProviderRow struct {
	ID           string
	DisplayName  pgtype.Text
	IssuerURL    pgtype.Text
	ClientID     pgtype.Text
	ClientSecret pgtype.Text
	ClaimStyle   pgtype.Text
	Scopes       []string // nil = no override; []string{} = explicit empty override
	ScopesSet    bool     // true when the DB row provided a non-NULL scopes value
	Enabled      bool
}

// MergedProvider carries the effective Provider plus per-field
// attribution so the GUI can show correct grey-placeholder defaults.
type MergedProvider struct {
	ID string

	// Effective is the resolved Provider the OAuth orchestrator consumes.
	// Empty fields (e.g. ClientID == "") signal "no layer provided a value"
	// — the caller decides whether that's usable (needs ClientID) or
	// acceptable (DisplayName can fall back to title case).
	Effective Provider

	// DBOverride mirrors the oidc_providers row, or nil when no row exists.
	DBOverride *DBProviderRow

	// EnvValues captures what env alone would have produced, or nil when
	// env didn't configure this ID. Used by the GUI to show "(env)" as the
	// placeholder when a DB field is cleared.
	EnvValues *Provider

	// WellKnownDefaults reflects the hardcoded google/microsoft defaults
	// for this ID, or nil when the ID is not well-known.
	WellKnownDefaults *providerDefaults

	// HasClientSecret is true iff any layer provides a non-empty client
	// secret. Exposed to the GUI so the admin can tell whether a secret
	// is currently set without it being returned in any response.
	HasClientSecret bool
}

// LoadMergedProviders reads the DB override rows and merges them onto
// the env-derived registry. Callers pass the env registry they already
// loaded via LoadProviders (no re-read from env here — keeps the flow
// testable and pins env semantics to a single code path).
//
// The returned map is keyed by provider id and contains one entry per
// id that appears in either env or DB. Both sides are merged even if
// only one layer knows about the id: a DB-only provider (new keycloak
// override configured via GUI) and an env-only provider (classic
// deployment) are equally legitimate.
func LoadMergedProviders(ctx context.Context, envRegistry ProviderRegistry, q OIDCProviderQuerier) (map[string]*MergedProvider, error) {
	dbRows, err := q.ListOIDCProviders(ctx)
	if err != nil {
		return nil, err
	}

	dbByID := make(map[string]DBProviderRow, len(dbRows))
	for _, r := range dbRows {
		dbByID[r.ID] = toDBProviderRow(r)
	}

	// Union of ids known to either layer.
	ids := make(map[string]struct{}, len(envRegistry)+len(dbRows))
	for id := range envRegistry {
		ids[id] = struct{}{}
	}
	for id := range dbByID {
		ids[id] = struct{}{}
	}

	out := make(map[string]*MergedProvider, len(ids))
	for id := range ids {
		env, hasEnv := envRegistry[id]
		dbRow, hasDB := dbByID[id]
		defaults, hasDefaults := wellKnownProviders[id]

		var envPtr *Provider
		if hasEnv {
			envCopy := env
			envPtr = &envCopy
		}
		var dbPtr *DBProviderRow
		if hasDB {
			dbCopy := dbRow
			dbPtr = &dbCopy
		}
		var defPtr *providerDefaults
		if hasDefaults {
			defCopy := defaults
			defPtr = &defCopy
		}

		merged := mergeFields(id, envPtr, dbPtr, defPtr)
		out[id] = merged
	}
	return out, nil
}

// mergeFields implements the precedence cascade. Pure function; callers
// supply already-unpacked per-layer snapshots so the merge stays
// trivially testable.
func mergeFields(id string, env *Provider, dbRow *DBProviderRow, defaults *providerDefaults) *MergedProvider {
	effective := Provider{
		ID:           id,
		DisplayName:  pickStr(nullableStr(dbRow, dbDisplayName), envStr(env, envDisplayName), defaultsStr(defaults, defDisplayName), titleCase(id)),
		IssuerURL:    pickStr(nullableStr(dbRow, dbIssuerURL), envStr(env, envIssuerURL), defaultsStr(defaults, defIssuerURL)),
		ClientID:     pickStr(nullableStr(dbRow, dbClientID), envStr(env, envClientID)),
		ClientSecret: pickStr(nullableStr(dbRow, dbClientSecret), envStr(env, envClientSecret)),
		ClaimStyle:   pickStr(nullableStr(dbRow, dbClaimStyle), envStr(env, envClaimStyle), defaultsStr(defaults, defClaimStyle)),
		Scopes:       pickScopes(dbRow, env, defaultScopes),
	}
	effective.MultiTenantIssuer = isMultiTenantIssuer(effective.IssuerURL)

	return &MergedProvider{
		ID:                id,
		Effective:         effective,
		DBOverride:        dbRow,
		EnvValues:         env,
		WellKnownDefaults: defaults,
		HasClientSecret:   effective.ClientSecret != "",
	}
}

// --- accessors used by mergeFields to pull the right field from each layer ---

type dbField func(*DBProviderRow) (value string, isSet bool)

var (
	dbDisplayName  dbField = func(r *DBProviderRow) (string, bool) { return pgtypeText(r.DisplayName) }
	dbIssuerURL    dbField = func(r *DBProviderRow) (string, bool) { return pgtypeText(r.IssuerURL) }
	dbClientID     dbField = func(r *DBProviderRow) (string, bool) { return pgtypeText(r.ClientID) }
	dbClientSecret dbField = func(r *DBProviderRow) (string, bool) { return pgtypeText(r.ClientSecret) }
	dbClaimStyle   dbField = func(r *DBProviderRow) (string, bool) { return pgtypeText(r.ClaimStyle) }
)

type envField func(*Provider) string

var (
	envDisplayName  envField = func(p *Provider) string { return p.DisplayName }
	envIssuerURL    envField = func(p *Provider) string { return p.IssuerURL }
	envClientID     envField = func(p *Provider) string { return p.ClientID }
	envClientSecret envField = func(p *Provider) string { return p.ClientSecret }
	envClaimStyle   envField = func(p *Provider) string { return p.ClaimStyle }
)

type defField func(providerDefaults) string

var (
	defDisplayName defField = func(d providerDefaults) string { return d.DisplayName }
	defIssuerURL   defField = func(d providerDefaults) string { return d.IssuerURL }
	defClaimStyle  defField = func(d providerDefaults) string { return d.ClaimStyle }
)

// nullableStr returns the DB override value only when the row exists AND
// the field is non-NULL. Empty strings are treated as "not set" so a row
// with an explicit empty-string DisplayName behaves like an absent field
// — which matches the operator's mental model ("leaving it blank means
// use the default").
func nullableStr(r *DBProviderRow, fn dbField) string {
	if r == nil {
		return ""
	}
	v, ok := fn(r)
	if !ok {
		return ""
	}
	return v
}

// envStr returns the field from env or "" when env didn't configure this
// provider.
func envStr(p *Provider, fn envField) string {
	if p == nil {
		return ""
	}
	return fn(p)
}

// defaultsStr returns the hardcoded default or "" when not well-known.
func defaultsStr(d *providerDefaults, fn defField) string {
	if d == nil {
		return ""
	}
	return fn(*d)
}

// pickStr returns the first non-empty argument. Intentionally variadic
// so the cascade length can vary per field without a family of helpers.
func pickStr(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// pickScopes walks the scope cascade. Empty slices are treated as "no
// opinion" at their layer so a DB row can clear the list back to env
// defaults by submitting null (rather than an empty array).
func pickScopes(dbRow *DBProviderRow, env *Provider, fallback []string) []string {
	if dbRow != nil && dbRow.ScopesSet && len(dbRow.Scopes) > 0 {
		out := make([]string, len(dbRow.Scopes))
		copy(out, dbRow.Scopes)
		return out
	}
	if env != nil && len(env.Scopes) > 0 {
		out := make([]string, len(env.Scopes))
		copy(out, env.Scopes)
		return out
	}
	if len(fallback) > 0 {
		out := make([]string, len(fallback))
		copy(out, fallback)
		return out
	}
	return nil
}

// pgtypeText reads a pgtype.Text: (value, true) when valid and
// non-empty; ("", false) otherwise. Empty strings are treated the same
// as NULL so the merge semantics match what the operator expects from
// "clearing" a field in the GUI.
func pgtypeText(t pgtype.Text) (string, bool) {
	if !t.Valid {
		return "", false
	}
	if t.String == "" {
		return "", false
	}
	return t.String, true
}

// toDBProviderRow converts a sqlc row to the package-local shape. Copies
// Scopes so later mutation of one doesn't alias the other.
func toDBProviderRow(r db.OidcProvider) DBProviderRow {
	out := DBProviderRow{
		ID:           r.ID,
		DisplayName:  r.DisplayName,
		IssuerURL:    r.IssuerUrl,
		ClientID:     r.ClientID,
		ClientSecret: r.ClientSecret,
		ClaimStyle:   r.ClaimStyle,
		Enabled:      r.Enabled,
	}
	if r.Scopes != nil {
		out.ScopesSet = true
		out.Scopes = append([]string(nil), r.Scopes...)
	}
	return out
}

// MergedEnabled reports whether the provider should be surfaced to the
// OAuth orchestrator based on the DB row's enabled flag. Env-only
// providers are always enabled at this layer (the /confirm provider_enabled
// JSONB map is a separate filter applied upstream by the caller).
func (m *MergedProvider) MergedEnabled() bool {
	if m.DBOverride != nil {
		return m.DBOverride.Enabled
	}
	return true
}

// MergedRegistry extracts the Effective Provider from each merged entry
// into a plain ProviderRegistry, dropping providers that either lack the
// minimum fields required by OAuth (ClientID, IssuerURL, ClaimStyle) or
// are disabled at the DB-override layer. Useful for building the registry
// NewOAuth / OAuth.Rebuild consume.
func MergedRegistry(merged map[string]*MergedProvider) ProviderRegistry {
	out := make(ProviderRegistry, len(merged))
	for id, m := range merged {
		if !m.MergedEnabled() {
			continue
		}
		if m.Effective.ClientID == "" || m.Effective.IssuerURL == "" || m.Effective.ClaimStyle == "" {
			// Not viable for OAuth init — skip but keep the merged entry
			// around for the GUI (which reads MergedProvider directly).
			continue
		}
		out[id] = m.Effective
	}
	return out
}
