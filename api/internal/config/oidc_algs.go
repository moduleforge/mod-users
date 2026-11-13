package config

import (
	"fmt"
	"os"
	"strings"
)

// OIDCAllowedSigningAlgsEnv names the optional environment variable holding
// the operator's comma-separated restriction on the JWS algorithms accepted
// for OIDC ID tokens.
const OIDCAllowedSigningAlgsEnv = "OIDC_ALLOWED_SIGNING_ALGS"

// oidcSigningAlgAllowlist is the fixed set of asymmetric JWS algorithms an
// OIDC ID token may be signed with, in the deterministic order used wherever
// a resolved list is produced. "none" and the HMAC algorithms are
// deliberately absent: they are never accepted, even when a provider
// advertises them.
var oidcSigningAlgAllowlist = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
	"EdDSA",
}

// OIDCSigningAlgAllowlist returns a copy of the fixed allowlist of OIDC
// signing algorithms, in deterministic order.
func OIDCSigningAlgAllowlist() []string {
	return append([]string(nil), oidcSigningAlgAllowlist...)
}

// IsAllowedOIDCSigningAlg reports whether alg is on the fixed allowlist.
// Names are case-sensitive, as JWS "alg" values are.
func IsAllowedOIDCSigningAlg(alg string) bool {
	for _, a := range oidcSigningAlgAllowlist {
		if a == alg {
			return true
		}
	}
	return false
}

// ValidateOIDCSigningAlgs rejects any name that is not on the fixed
// allowlist (so "none", every HMAC algorithm and unknown names), naming the
// offending value.
func ValidateOIDCSigningAlgs(algs []string) error {
	for _, a := range algs {
		if !IsAllowedOIDCSigningAlg(a) {
			return fmt.Errorf("unsupported OIDC signing algorithm %q (allowed: %s)",
				a, strings.Join(oidcSigningAlgAllowlist, ", "))
		}
	}
	return nil
}

// ParseOIDCSigningAlgs parses a comma-separated algorithm list. Surrounding
// whitespace is trimmed and duplicates are dropped, keeping first-seen order.
// A blank value yields nil (no operator restriction). An empty element, or a
// name outside the allowlist, is an error.
func ParseOIDCSigningAlgs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("empty algorithm name in list")
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if err := ValidateOIDCSigningAlgs(out); err != nil {
		return nil, err
	}
	return out, nil
}

// loadOIDCAllowedSigningAlgs reads OIDC_ALLOWED_SIGNING_ALGS. A rejected value
// yields no restriction and is reported in problems.
func loadOIDCAllowedSigningAlgs() (algs []string, problems []string) {
	algs, err := ParseOIDCSigningAlgs(os.Getenv(OIDCAllowedSigningAlgsEnv))
	if err != nil {
		return nil, []string{OIDCAllowedSigningAlgsEnv + ": " + err.Error()}
	}
	return algs, nil
}
