package auth

// firebaseMapper handles JWT claims issued by Firebase Authentication.
//
// Firebase does not include roles in the token by default; roles must be added via
// custom claims set server-side with the Firebase Admin SDK. Two conventions are
// supported:
//   - A "roles" array claim: {"roles": ["admin", "editor"]}
//   - A boolean "admin" claim: {"admin": true}  (maps to opts.AdminRole)
type firebaseMapper struct {
	opts MapperOptions
}

func (m *firebaseMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	roles := lowercaseAll(getStringSlice(rawClaims, "roles"))

	// Handle the boolean admin custom claim pattern.
	if adminVal, ok := rawClaims["admin"]; ok {
		if isAdmin, ok := adminVal.(bool); ok && isAdmin {
			alreadyAdmin := false
			for _, r := range roles {
				if r == m.opts.AdminRole {
					alreadyAdmin = true
					break
				}
			}
			if !alreadyAdmin {
				roles = append(roles, m.opts.AdminRole)
			}
		}
	}

	return Principal{
		Subject: sub,
		Issuer:  iss,
		Email:   getString(rawClaims, "email"),
		Roles:   roles,
	}, nil
}
