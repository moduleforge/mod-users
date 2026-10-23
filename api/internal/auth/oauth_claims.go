package auth

import "fmt"

// ExternalIdentity is the normalized identity an external OIDC provider's
// verified id_token asserts, as the OAuth orchestrator's claim mappers
// extract it. It describes the IdP's view of the caller before any database
// lookup; it is never derived from, and never becomes, a local access token.
type ExternalIdentity struct {
	Subject       string // OIDC sub
	Issuer        string // OIDC iss
	Email         string
	EmailVerified bool     // true when the IdP asserted email_verified; false is the zero value and means "not asserted"
	Roles         []string // normalized, lowercased IdP roles; informational only
}

// ClaimMapper translates the raw claims of an external provider's verified
// id_token into an ExternalIdentity. Each OIDC provider has its own
// conventions for embedding email and role information; implementations
// encapsulate those differences so the OAuth orchestrator stays
// provider-agnostic. The mappers are internal to the orchestrator: they map
// external id_token claims only and never touch local access tokens.
type ClaimMapper interface {
	Map(rawClaims map[string]any) (ExternalIdentity, error)
}

// MapperOptions configures provider-specific claim extraction behaviour.
// Fields are optional; sensible defaults apply where noted.
type MapperOptions struct {
	// AdminRole is the post-normalization (lowercased) role string some
	// mappers synthesize from provider-specific admin markers (Azure AD
	// directory roles, Firebase's admin flag, Authelia groups). Defaults to
	// "admin" when empty. Informational only: no local authority derives from
	// an external role.
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
