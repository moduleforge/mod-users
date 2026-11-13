package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// oidcDefaultSigningAlg is the algorithm assumed when a provider's discovery
// document advertises none of the allowlisted ones (the OIDC default).
const oidcDefaultSigningAlg = "RS256"

// resolveOIDCSigningAlgs returns the JWS algorithms accepted for an OIDC
// issuer's ID tokens: the discovery document's
// id_token_signing_alg_values_supported intersected with the fixed asymmetric
// allowlist; RS256 alone when that intersection is empty; then intersected with
// operatorAllow when it is non-empty. "none", the HMAC algorithms and unknown
// names never survive, because the result is built only from allowlist
// entries. The order is the allowlist's order. An empty result is an error.
func resolveOIDCSigningAlgs(discovered, operatorAllow []string) ([]string, error) {
	allowlist := config.OIDCSigningAlgAllowlist()
	advertised := make(map[string]struct{}, len(discovered))
	for _, a := range discovered {
		advertised[a] = struct{}{}
	}
	resolved := make([]string, 0, len(allowlist))
	for _, a := range allowlist {
		if _, ok := advertised[a]; ok {
			resolved = append(resolved, a)
		}
	}
	if len(resolved) == 0 {
		resolved = append(resolved, oidcDefaultSigningAlg)
	}

	if len(operatorAllow) > 0 {
		permitted := make(map[string]struct{}, len(operatorAllow))
		for _, a := range operatorAllow {
			permitted[a] = struct{}{}
		}
		kept := resolved[:0]
		for _, a := range resolved {
			if _, ok := permitted[a]; ok {
				kept = append(kept, a)
			}
		}
		resolved = kept
	}
	if len(resolved) == 0 {
		return nil, errors.New("no acceptable signing algorithm: the provider's advertised algorithms and the operator restriction have none in common")
	}
	return resolved, nil
}

// oidcSigningAlgsFor resolves the accepted signing algorithms for a discovered
// provider. Errors name the issuer and never carry token data.
func oidcSigningAlgsFor(provider *oidc.Provider, issuerURL string, operatorAllow []string) ([]string, error) {
	var disc struct {
		Algs []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := provider.Claims(&disc); err != nil {
		return nil, fmt.Errorf("oidc issuer %q: read discovery document: %w", issuerURL, err)
	}
	algs, err := resolveOIDCSigningAlgs(disc.Algs, operatorAllow)
	if err != nil {
		return nil, fmt.Errorf("oidc issuer %q: %w", issuerURL, err)
	}
	return algs, nil
}

// errOIDCTokenTyp is the fixed error for a disallowed "typ" header. It never
// echoes the header value.
var errOIDCTokenTyp = errors.New("unsupported token typ")

// checkOIDCTokenTyp enforces the typ policy for OIDC ID tokens, read from the
// JWS protected header: an absent typ, "JWT" and "aud+jwt" are accepted
// (case-insensitively, per RFC 7515); any other value is rejected. Only
// compact serialization is accepted: input that is not exactly three
// dot-separated segments (JWS JSON serialization, padded input, two or four
// segments) is rejected, as is a token whose protected header cannot be
// decoded or whose typ is present but not a string. It runs before signature
// verification and trusts nothing the header says beyond this allow decision.
func checkOIDCTokenTyp(rawToken string) error {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return errOIDCTokenTyp
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errOIDCTokenTyp
	}
	var h struct {
		Typ json.RawMessage `json:"typ"`
	}
	if json.Unmarshal(b, &h) != nil {
		return errOIDCTokenTyp
	}
	if h.Typ == nil {
		return nil
	}
	var typ string
	if string(h.Typ) == "null" || json.Unmarshal(h.Typ, &typ) != nil {
		return errOIDCTokenTyp
	}
	if typ == "" || strings.EqualFold(typ, "JWT") || strings.EqualFold(typ, audienceTokenTyp) {
		return nil
	}
	return errOIDCTokenTyp
}
