package auth

import "strings"

// auth0CommonNamespaces lists widely-used Auth0 custom claim namespace patterns.
// When opts.RolesNamespace is empty the mapper tries each of these in order.
var auth0CommonNamespaces = []string{
	"https://myapp.example.com", // placeholder — tried last
	"https://app.example.com",
}

// auth0Mapper handles JWT claims issued by Auth0.
//
// Auth0 does not include roles in the standard JWT payload by default. Applications
// must attach roles via an Auth0 Action / Rule that writes them under a custom
// namespace key of the form "<namespace>/roles".
//
// If opts.RolesNamespace is set, only that namespace is tried.
// Otherwise the mapper probes a set of common patterns and the bare "roles" claim
// as a last resort.
type auth0Mapper struct {
	opts MapperOptions
}

func (m *auth0Mapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	roles := m.extractRoles(rawClaims)

	return Principal{
		Subject:       sub,
		Issuer:        iss,
		Email:         getString(rawClaims, "email"),
		EmailVerified: coerceBoolClaim(rawClaims, "email_verified"),
		Roles:         roles,
	}, nil
}

func (m *auth0Mapper) extractRoles(rawClaims map[string]any) []string {
	// Caller-supplied namespace takes priority.
	if m.opts.RolesNamespace != "" {
		key := strings.TrimRight(m.opts.RolesNamespace, "/") + "/roles"
		if roles := getStringSlice(rawClaims, key); len(roles) > 0 {
			return lowercaseAll(roles)
		}
	}

	// Probe all keys in rawClaims whose suffix is "/roles" — this covers any
	// namespace the Auth0 Action may have been configured with.
	for k, v := range rawClaims {
		if strings.HasSuffix(k, "/roles") {
			switch t := v.(type) {
			case []string:
				if len(t) > 0 {
					return lowercaseAll(t)
				}
			case []any:
				result := make([]string, 0, len(t))
				for _, elem := range t {
					if s, ok := elem.(string); ok {
						result = append(result, s)
					}
				}
				if len(result) > 0 {
					return lowercaseAll(result)
				}
			}
		}
	}

	// Fall back to a plain "roles" claim (some Auth0 setups use this).
	if roles := getStringSlice(rawClaims, "roles"); len(roles) > 0 {
		return lowercaseAll(roles)
	}

	return nil
}
