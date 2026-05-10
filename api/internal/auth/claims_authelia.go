package auth

// autheliaMapper handles JWT claims issued by Authelia.
//
// Authelia places the user's groups in the "groups" array claim. Group names are
// lowercased and compared against opts.AdminRole to determine whether the principal
// should receive the admin role.
type autheliaMapper struct {
	opts MapperOptions
}

func (m *autheliaMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	groups := lowercaseAll(getStringSlice(rawClaims, "groups"))

	var roles []string
	for _, g := range groups {
		if g == m.opts.AdminRole {
			roles = append(roles, m.opts.AdminRole)
			break
		}
	}

	// Include any remaining groups as roles so callers have full context.
	// Deduplicate the admin role if it was already added above.
	seen := make(map[string]struct{}, len(groups))
	for _, r := range roles {
		seen[r] = struct{}{}
	}
	for _, g := range groups {
		if _, ok := seen[g]; !ok {
			roles = append(roles, g)
			seen[g] = struct{}{}
		}
	}

	return Principal{
		Subject:       sub,
		Issuer:        iss,
		Email:         getString(rawClaims, "email"),
		EmailVerified: coerceBoolClaim(rawClaims, "email_verified"),
		Roles:         roles,
	}, nil
}
