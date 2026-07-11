package auth

import (
	"testing"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// claimsShapedLikeLocalJWT mirrors the flat "email" + "roles" claim layout
// this module's own locally-minted JWTs use (see cmd/server/main.go's
// hardcoded generic-mapper construction).
func claimsShapedLikeLocalJWT() map[string]any {
	return map[string]any{
		"sub":   "user-123",
		"iss":   "local",
		"email": "owner@example.com",
		"roles": []any{"admin"},
	}
}

// TestNewClaimMapper_GenericStylePopulatesEmailAndRolesPaths reproduces task
// 008's exact observed failure ("generic mapper requires opts.EmailPath")
// and confirms the facade now sets EmailPath/RolesPath for the "generic"
// style, so Map succeeds against a flat email+roles claim map.
func TestNewClaimMapper_GenericStylePopulatesEmailAndRolesPaths(t *testing.T) {
	mapper, err := NewClaimMapper("generic", config.AuthConfig{AdminRole: "admin"})
	if err != nil {
		t.Fatalf("NewClaimMapper(\"generic\", ...) returned unexpected error: %v", err)
	}

	principal, err := mapper.Map(claimsShapedLikeLocalJWT())
	if err != nil {
		t.Fatalf("mapper.Map(...) returned unexpected error: %v (EmailPath/RolesPath must be non-zero for the generic style)", err)
	}

	if principal.Email != "owner@example.com" {
		t.Errorf("principal.Email = %q, want %q", principal.Email, "owner@example.com")
	}
	if len(principal.Roles) != 1 || principal.Roles[0] != "admin" {
		t.Errorf("principal.Roles = %v, want [admin]", principal.Roles)
	}
}

// TestBuildMapperOptions confirms the fix is scoped to the "generic" style
// only: EmailPath/RolesPath are populated with the fixed local-JWT values for
// "generic", and stay zero-valued for every other style — matching today's
// behavior exactly for those cases (Requirement 6).
func TestBuildMapperOptions(t *testing.T) {
	tests := []struct {
		name          string
		style         string
		wantEmailPath string
		wantRolesPath string
	}{
		{name: "generic", style: "generic", wantEmailPath: "email", wantRolesPath: "roles"},
		{name: "google", style: "google", wantEmailPath: "", wantRolesPath: ""},
		{name: "microsoft", style: "microsoft", wantEmailPath: "", wantRolesPath: ""},
		{name: "authelia", style: "authelia", wantEmailPath: "", wantRolesPath: ""},
		{name: "keycloak", style: "keycloak", wantEmailPath: "", wantRolesPath: ""},
		{name: "cognito", style: "cognito", wantEmailPath: "", wantRolesPath: ""},
		{name: "auth0", style: "auth0", wantEmailPath: "", wantRolesPath: ""},
		{name: "firebase", style: "firebase", wantEmailPath: "", wantRolesPath: ""},
		{name: "unknown-style-still-zeroed", style: "some-other-style", wantEmailPath: "", wantRolesPath: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := buildMapperOptions(tc.style, config.AuthConfig{AdminRole: "admin"})
			if opts.EmailPath != tc.wantEmailPath {
				t.Errorf("buildMapperOptions(%q, ...).EmailPath = %q, want %q", tc.style, opts.EmailPath, tc.wantEmailPath)
			}
			if opts.RolesPath != tc.wantRolesPath {
				t.Errorf("buildMapperOptions(%q, ...).RolesPath = %q, want %q", tc.style, opts.RolesPath, tc.wantRolesPath)
			}
			if opts.AdminRole != "admin" {
				t.Errorf("buildMapperOptions(%q, ...).AdminRole = %q, want %q (AdminRole must still pass through)", tc.style, opts.AdminRole, "admin")
			}
		})
	}
}

// TestNewClaimMapper_NonGenericStyleUnaffected confirms end-to-end that a
// non-"generic" style continues to work exactly as before: it does not
// require EmailPath/RolesPath to be set, and NewClaimMapper still succeeds
// and produces a working mapper.
func TestNewClaimMapper_NonGenericStyleUnaffected(t *testing.T) {
	mapper, err := NewClaimMapper("keycloak", config.AuthConfig{AdminRole: "admin"})
	if err != nil {
		t.Fatalf("NewClaimMapper(\"keycloak\", ...) returned unexpected error: %v", err)
	}

	principal, err := mapper.Map(map[string]any{
		"sub":          "user-456",
		"iss":          "https://keycloak.example.com",
		"email":        "user@example.com",
		"realm_access": map[string]any{"roles": []any{"viewer"}},
	})
	if err != nil {
		t.Fatalf("mapper.Map(...) returned unexpected error: %v", err)
	}
	if principal.Email != "user@example.com" {
		t.Errorf("principal.Email = %q, want %q", principal.Email, "user@example.com")
	}
}
