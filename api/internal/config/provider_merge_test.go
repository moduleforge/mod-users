package config

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/moduleforge/mod-users/model/db"
)

// stubProviderQuerier returns a canned list of DB rows.
type stubProviderQuerier struct {
	rows []db.OidcProvider
	err  error
}

func (s stubProviderQuerier) ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error) {
	return s.rows, s.err
}

// validText constructs a non-null pgtype.Text with the given value.
func validText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

func TestLoadMergedProviders_EnvOnly(t *testing.T) {
	env := ProviderRegistry{
		"google": Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
	merged, err := LoadMergedProviders(context.Background(), env, stubProviderQuerier{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, ok := merged["google"]
	if !ok {
		t.Fatalf("google missing from merged")
	}
	if g.Effective.ClientID != "env-gid" {
		t.Errorf("ClientID = %q, want env value", g.Effective.ClientID)
	}
	if g.Effective.ClientSecret != "env-gsecret" {
		t.Errorf("ClientSecret = %q, want env value", g.Effective.ClientSecret)
	}
	if !g.HasClientSecret {
		t.Errorf("HasClientSecret = false, want true")
	}
	if g.DBOverride != nil {
		t.Errorf("DBOverride should be nil")
	}
	if g.EnvValues == nil {
		t.Errorf("EnvValues should be non-nil for env-configured provider")
	}
	if g.WellKnownDefaults == nil {
		t.Errorf("WellKnownDefaults should be non-nil for google")
	}
}

func TestLoadMergedProviders_DBOverridesEnv(t *testing.T) {
	env := ProviderRegistry{
		"google": Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
	rows := []db.OidcProvider{
		{
			ID:           "google",
			ClientID:     validText("db-gid"),
			ClientSecret: pgtype.Text{}, // NULL — keep env
			Scopes:       nil,           // NULL — keep env
			Enabled:      true,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), env, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := merged["google"]
	if g.Effective.ClientID != "db-gid" {
		t.Errorf("ClientID = %q, want DB override", g.Effective.ClientID)
	}
	if g.Effective.ClientSecret != "env-gsecret" {
		t.Errorf("ClientSecret = %q, want env (DB is NULL)", g.Effective.ClientSecret)
	}
	if g.Effective.DisplayName != "Google" {
		t.Errorf("DisplayName = %q, want well-known default", g.Effective.DisplayName)
	}
	wantScopes := []string{"openid", "email", "profile"}
	if !reflect.DeepEqual(g.Effective.Scopes, wantScopes) {
		t.Errorf("Scopes = %v, want env scopes %v", g.Effective.Scopes, wantScopes)
	}
}

func TestLoadMergedProviders_DBOnlyWellKnown(t *testing.T) {
	rows := []db.OidcProvider{
		{
			ID:           "google",
			ClientID:     validText("db-only-gid"),
			ClientSecret: validText("db-only-gsecret"),
			Enabled:      true,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), ProviderRegistry{}, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := merged["google"]
	if g.Effective.IssuerURL != "https://accounts.google.com" {
		t.Errorf("IssuerURL = %q, want well-known google default", g.Effective.IssuerURL)
	}
	if g.Effective.ClaimStyle != "google" {
		t.Errorf("ClaimStyle = %q, want well-known google default", g.Effective.ClaimStyle)
	}
	if g.Effective.DisplayName != "Google" {
		t.Errorf("DisplayName = %q, want well-known default", g.Effective.DisplayName)
	}
	wantScopes := []string{"openid", "email", "profile"}
	if !reflect.DeepEqual(g.Effective.Scopes, wantScopes) {
		t.Errorf("Scopes fallback = %v, want defaultScopes %v", g.Effective.Scopes, wantScopes)
	}
	if g.EnvValues != nil {
		t.Errorf("EnvValues should be nil (DB-only provider)")
	}
}

func TestLoadMergedProviders_UnknownProviderFullyFromDB(t *testing.T) {
	rows := []db.OidcProvider{
		{
			ID:           "keycloak",
			DisplayName:  validText("My Keycloak"),
			IssuerUrl:    validText("https://kc.example.com/realms/main"),
			ClientID:     validText("kc-id"),
			ClientSecret: validText("kc-secret"),
			ClaimStyle:   validText("keycloak"),
			Scopes:       []string{"openid", "email", "profile", "roles"},
			Enabled:      true,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), ProviderRegistry{}, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k := merged["keycloak"]
	if k.Effective.DisplayName != "My Keycloak" {
		t.Errorf("DisplayName = %q, want DB value", k.Effective.DisplayName)
	}
	if k.WellKnownDefaults != nil {
		t.Errorf("WellKnownDefaults should be nil for keycloak")
	}
	if k.EnvValues != nil {
		t.Errorf("EnvValues should be nil for DB-only provider")
	}
	wantScopes := []string{"openid", "email", "profile", "roles"}
	if !reflect.DeepEqual(k.Effective.Scopes, wantScopes) {
		t.Errorf("Scopes = %v, want DB value %v", k.Effective.Scopes, wantScopes)
	}
}

func TestLoadMergedProviders_MicrosoftMultiTenant(t *testing.T) {
	rows := []db.OidcProvider{
		{
			ID:           "microsoft",
			ClientID:     validText("mid"),
			ClientSecret: validText("msecret"),
			Enabled:      true,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), ProviderRegistry{}, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := merged["microsoft"]
	if m.Effective.IssuerURL != "https://login.microsoftonline.com/common/v2.0" {
		t.Errorf("IssuerURL = %q, want default multi-tenant", m.Effective.IssuerURL)
	}
	if !m.Effective.MultiTenantIssuer {
		t.Errorf("MultiTenantIssuer should be true for /common/v2.0 endpoint")
	}
}

func TestMergedRegistry_FiltersDisabledAndIncomplete(t *testing.T) {
	merged := map[string]*MergedProvider{
		"ready": {
			ID:         "ready",
			Effective:  Provider{ID: "ready", ClientID: "x", IssuerURL: "https://y", ClaimStyle: "z"},
			DBOverride: &DBProviderRow{Enabled: true},
		},
		"disabled": {
			ID:         "disabled",
			Effective:  Provider{ID: "disabled", ClientID: "x", IssuerURL: "https://y", ClaimStyle: "z"},
			DBOverride: &DBProviderRow{Enabled: false},
		},
		"incomplete": {
			ID:        "incomplete",
			Effective: Provider{ID: "incomplete", ClientID: ""}, // no ClientID → not viable
		},
		"env-only": {
			ID:        "env-only",
			Effective: Provider{ID: "env-only", ClientID: "x", IssuerURL: "https://y", ClaimStyle: "z"},
			// No DBOverride → implicitly enabled.
		},
	}
	out := MergedRegistry(merged)
	if _, ok := out["ready"]; !ok {
		t.Errorf("ready should be in registry")
	}
	if _, ok := out["disabled"]; ok {
		t.Errorf("disabled should be filtered out")
	}
	if _, ok := out["incomplete"]; ok {
		t.Errorf("incomplete (no ClientID) should be filtered out")
	}
	if _, ok := out["env-only"]; !ok {
		t.Errorf("env-only should be in registry (no DB row = enabled)")
	}
}

func TestLoadMergedProviders_EmptyScopesInDBFallsThrough(t *testing.T) {
	// A DB row with Scopes=[] (len 0) should NOT clobber the env scopes.
	// This matches pickScopes treating "empty" as "no opinion" so the
	// operator can clear overrides.
	env := ProviderRegistry{
		"google": Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email"},
		},
	}
	rows := []db.OidcProvider{
		{
			ID:      "google",
			Scopes:  []string{}, // explicit empty array
			Enabled: true,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), env, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := merged["google"].Effective.Scopes
	want := []string{"openid", "email"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scopes = %v, want env fallback %v", got, want)
	}
}

func TestLoadMergedProviders_DisabledDBOverride(t *testing.T) {
	env := ProviderRegistry{
		"google": Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
	rows := []db.OidcProvider{
		{
			ID:      "google",
			Enabled: false,
		},
	}
	merged, err := LoadMergedProviders(context.Background(), env, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := merged["google"]
	if g.MergedEnabled() {
		t.Errorf("MergedEnabled should be false (DB row explicitly disabled)")
	}
	// MergedRegistry should drop it.
	if _, ok := MergedRegistry(merged)["google"]; ok {
		t.Errorf("MergedRegistry should drop disabled providers")
	}
}

func TestLoadMergedProviders_HasClientSecretAttribution(t *testing.T) {
	env := ProviderRegistry{
		"google": Provider{
			ID:           "google",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			IssuerURL:    "https://accounts.google.com",
			ClaimStyle:   "google",
			DisplayName:  "Google",
		},
	}
	// DB row with client_secret NULL — secret should still be true from env.
	rows := []db.OidcProvider{{ID: "google", Enabled: true}}
	merged, err := LoadMergedProviders(context.Background(), env, stubProviderQuerier{rows: rows})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !merged["google"].HasClientSecret {
		t.Errorf("HasClientSecret should be true when env provides secret")
	}
}
