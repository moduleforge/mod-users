package auth

import "fmt"

// genericMapper is a configurable mapper suitable for any OIDC provider whose
// claim layout can be described with dot-separated paths.
//
// opts.EmailPath and opts.RolesPath must be set; both accept dot-notation like
// "realm_access.roles" or simply "email".
type genericMapper struct {
	opts MapperOptions
}

func (m *genericMapper) Map(rawClaims map[string]any) (ExternalIdentity, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return ExternalIdentity{}, err
	}

	if m.opts.EmailPath == "" {
		return ExternalIdentity{}, fmt.Errorf("auth: generic mapper requires opts.EmailPath")
	}
	if m.opts.RolesPath == "" {
		return ExternalIdentity{}, fmt.Errorf("auth: generic mapper requires opts.RolesPath")
	}

	email := getStringByPath(rawClaims, m.opts.EmailPath)
	roles := lowercaseAll(getStringSliceByPath(rawClaims, m.opts.RolesPath))

	return ExternalIdentity{
		Subject:       sub,
		Issuer:        iss,
		Email:         email,
		EmailVerified: coerceBoolClaim(rawClaims, "email_verified"),
		Roles:         roles,
	}, nil
}
