package auth

import "fmt"

// ClaimMapper translates the raw claims from a verified JWT into a normalized Principal.
// Each OIDC provider has its own conventions for embedding email and role information;
// implementations encapsulate those differences so the rest of the system stays provider-agnostic.
type ClaimMapper interface {
	Map(rawClaims map[string]any) (Principal, error)
}

// MapperOptions configures provider-specific claim extraction behaviour.
// Fields are optional; sensible defaults apply where noted.
type MapperOptions struct {
	// AdminRole is the post-normalization (lowercased) role string that confers admin access.
	// Defaults to "admin" when empty.
	AdminRole string

	// RolesNamespace is the custom claim namespace used by Auth0-style providers,
	// e.g. "https://myapp.example.com". The mapper appends "/roles" to build the full key.
	RolesNamespace string

	// RolesPath is a dot-separated JSON path used by the generic mapper to locate roles,
	// e.g. "realm_access.roles".
	RolesPath string

	// EmailPath is a dot-separated JSON path used by the generic mapper to locate the email,
	// e.g. "email" or "user_info.email".
	EmailPath string
}

// NewClaimMapper returns a ClaimMapper for the named OIDC claim style.
// Valid style values: "google", "microsoft", "authelia", "keycloak", "cognito",
// "auth0", "firebase", "generic".
func NewClaimMapper(style string, opts MapperOptions) (ClaimMapper, error) {
	if opts.AdminRole == "" {
		opts.AdminRole = "admin"
	}
	switch style {
	case "google":
		return &googleMapper{opts: opts}, nil
	case "microsoft":
		return &microsoftMapper{opts: opts}, nil
	case "authelia":
		return &autheliaMapper{opts: opts}, nil
	case "keycloak":
		return &keycloakMapper{opts: opts}, nil
	case "cognito":
		return &cognitoMapper{opts: opts}, nil
	case "auth0":
		return &auth0Mapper{opts: opts}, nil
	case "firebase":
		return &firebaseMapper{opts: opts}, nil
	case "generic":
		return &genericMapper{opts: opts}, nil
	default:
		return nil, fmt.Errorf("unknown OIDC claim style: %q", style)
	}
}
