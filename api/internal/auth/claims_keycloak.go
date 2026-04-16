package auth

// keycloakMapper handles JWT claims issued by Keycloak.
//
// Realm-level roles are nested under "realm_access": {"roles": [...]}.
// Client-level roles live under "resource_access": {<clientID>: {"roles": [...]}};
// those are not extracted here because the application should rely on realm roles
// for consistent cross-client authorization.
type keycloakMapper struct {
	opts MapperOptions
}

func (m *keycloakMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	roles := lowercaseAll(getNestedStringSlice(rawClaims, "realm_access", "roles"))

	return Principal{
		Subject: sub,
		Issuer:  iss,
		Email:   getString(rawClaims, "email"),
		Roles:   roles,
	}, nil
}
