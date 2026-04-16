package auth

// cognitoMapper handles JWT claims issued by AWS Cognito.
//
// Cognito places the user's Cognito group memberships in the "cognito:groups" claim.
// These groups are treated directly as roles after lowercasing.
type cognitoMapper struct {
	opts MapperOptions
}

func (m *cognitoMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	roles := lowercaseAll(getStringSlice(rawClaims, "cognito:groups"))

	return Principal{
		Subject: sub,
		Issuer:  iss,
		Email:   getString(rawClaims, "email"),
		Roles:   roles,
	}, nil
}
