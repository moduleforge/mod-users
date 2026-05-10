package auth

// Azure AD well-known directory role GUIDs (wids claim).
// The Global Administrator role is mapped to the normalized "admin" role.
// https://learn.microsoft.com/en-us/azure/active-directory/roles/permissions-reference
var azureAdminWIDs = map[string]struct{}{
	"62e90394-69f5-4237-9190-012177145e10": {}, // Global Administrator
	"194ae4cb-b126-40b2-bd5b-6091b380977d": {}, // Security Administrator
	"f28a1f50-f6e7-4571-818b-6a12f2af6b6c": {}, // SharePoint Administrator (commonly proxied as admin)
}

// microsoftMapper handles JWT claims issued by Microsoft Azure AD / Entra ID.
//
// Email is drawn from "email" first, then "preferred_username" as a fallback
// (Entra ID often sets preferred_username to the UPN which is the user's email).
//
// Roles come from two sources:
//   - "roles" claim: application roles assigned via App Registration manifest
//   - "wids" claim: Azure AD directory role GUIDs; known admin GUIDs are mapped to "admin"
type microsoftMapper struct {
	opts MapperOptions
}

func (m *microsoftMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	email := getString(rawClaims, "email")
	if email == "" {
		email = getString(rawClaims, "preferred_username")
	}

	roles := lowercaseAll(getStringSlice(rawClaims, "roles"))

	// Augment with directory roles from the wids claim.
	if wids := getStringSlice(rawClaims, "wids"); len(wids) > 0 {
		hasAdmin := false
		for _, r := range roles {
			if r == m.opts.AdminRole {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			for _, wid := range wids {
				if _, known := azureAdminWIDs[wid]; known {
					roles = append(roles, m.opts.AdminRole)
					break
				}
			}
		}
	}

	return Principal{
		Subject:       sub,
		Issuer:        iss,
		Email:         email,
		EmailVerified: coerceBoolClaim(rawClaims, "email_verified"),
		Roles:         roles,
	}, nil
}
