package auth

// googleMapper handles JWT claims issued by Google Identity Platform / Google Sign-In.
//
// Google JWTs carry end-user identity but do not include application-level roles.
// Role assignment for Google-authenticated users must be managed within the application
// (e.g., via the user_accounts table is_admin flag) rather than in the token itself.
type googleMapper struct {
	opts MapperOptions
}

func (m *googleMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	return Principal{
		Subject: sub,
		Issuer:  iss,
		Email:   getString(rawClaims, "email"),
		Roles:   nil, // Google does not embed roles in ID tokens
	}, nil
}
