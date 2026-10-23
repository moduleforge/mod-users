package auth

import (
	"fmt"
	"strings"
	"unicode"
)

// getString returns the string value at key in claims, or "" if absent or not a string.
func getString(claims map[string]any, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// getStringSlice returns a []string from claims[key].
// Handles both []string and []any (the latter being what JSON unmarshaling produces).
// Returns nil when the key is absent, the value is not a slice, or the slice is empty.
func getStringSlice(claims map[string]any, key string) []string {
	v, ok := claims[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		result := make([]string, 0, len(t))
		for _, elem := range t {
			if s, ok := elem.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// getNestedStringSlice navigates a series of map keys to reach a []string value.
// For example, getNestedStringSlice(claims, "realm_access", "roles") traverses
// claims["realm_access"]["roles"].
func getNestedStringSlice(claims map[string]any, keys ...string) []string {
	if len(keys) == 0 {
		return nil
	}
	current := claims
	for _, k := range keys[:len(keys)-1] {
		v, ok := current[k]
		if !ok {
			return nil
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		current = m
	}
	return getStringSlice(current, keys[len(keys)-1])
}

// getByPath navigates a dot-separated path and returns the value at that location,
// or nil if any segment is missing or not a map[string]any (except the final segment).
func getByPath(claims map[string]any, path string) any {
	if path == "" {
		return nil
	}
	parts := strings.SplitN(path, ".", 2)
	v, ok := claims[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return getByPath(m, parts[1])
}

// getStringByPath returns the string at the dot-separated path, or "".
func getStringByPath(claims map[string]any, path string) string {
	v := getByPath(claims, path)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// getStringSliceByPath returns a []string at the dot-separated path.
func getStringSliceByPath(claims map[string]any, path string) []string {
	v := getByPath(claims, path)
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		result := make([]string, 0, len(t))
		for _, elem := range t {
			if s, ok := elem.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// lowercaseAll returns a new slice with every element converted to lowercase.
func lowercaseAll(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// coerceBoolClaim reads key from claims and returns its boolean value.
// It handles both native bool and string ("true"/"false") forms as produced
// by different IdPs. Any other type or absent key returns false.
func coerceBoolClaim(claims map[string]any, key string) bool {
	v, ok := claims[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	}
	return false
}

// extractRequired pulls the mandatory OIDC "sub" and "iss" claims.
// Returns an error if either is absent or empty.
func extractRequired(claims map[string]any) (sub, iss string, err error) {
	sub = getString(claims, "sub")
	if sub == "" {
		return "", "", fmt.Errorf("auth: missing required claim \"sub\"")
	}
	iss = getString(claims, "iss")
	if iss == "" {
		return "", "", fmt.Errorf("auth: missing required claim \"iss\"")
	}
	return sub, iss, nil
}

// maxNameRunes bounds an IdP-supplied name taken into a person record.
const maxNameRunes = 100

// cleanName trims s, drops control and format characters, and bounds it to
// maxNameRunes. IdP claims are untrusted text.
func cleanName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n == maxNameRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// parseAssertedACR reads the id_token acr claim. Only a bare level 1, 2 or 3
// (as a string or a JSON number) is understood; URN-style acr values are
// IdP-specific and map to 0 (no assertion).
func parseAssertedACR(v any) int16 {
	switch t := v.(type) {
	case string:
		switch strings.TrimSpace(t) {
		case "1":
			return 1
		case "2":
			return 2
		case "3":
			return 3
		}
	case float64:
		if t == 1 || t == 2 || t == 3 {
			return int16(t)
		}
	}
	return 0
}

// enrichIdentity adds what the per-style mappers do not extract: the name
// claims and the asserted acr, read from the verified id_token's raw claims.
func enrichIdentity(id ExternalIdentity, claims map[string]any) ExternalIdentity {
	id.GivenName = cleanName(getString(claims, "given_name"))
	id.FamilyName = cleanName(getString(claims, "family_name"))
	if name := strings.Fields(getString(claims, "name")); len(name) > 0 {
		if id.GivenName == "" {
			id.GivenName = cleanName(name[0])
		}
		if id.FamilyName == "" && len(name) > 1 {
			id.FamilyName = cleanName(strings.Join(name[1:], " "))
		}
	}
	id.AssertedACR = parseAssertedACR(claims["acr"])
	return id
}
