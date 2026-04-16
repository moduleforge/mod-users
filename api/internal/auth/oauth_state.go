package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StatePayload is what the state cookie and OAuth state parameter both carry.
// The fields are intentionally short to keep the encoded value small.
type StatePayload struct {
	Provider   string `json:"p"`
	ReturnPath string `json:"r"`
	Nonce      string `json:"n"` // base64 nonce used as the OIDC nonce claim
	Expires    int64  `json:"e"` // unix seconds
}

// Expired reports whether the payload's expiry is in the past.
func (s StatePayload) Expired(now time.Time) bool {
	return now.Unix() > s.Expires
}

// StateSigner signs and verifies OAuth state payloads using HMAC-SHA256.
// The same key material is shared with the local JWT signer (cfg.JWTSecret)
// to avoid a second secret the operator would have to rotate independently.
type StateSigner struct {
	key []byte
}

// NewStateSigner constructs a StateSigner from raw key bytes. The key must
// be non-empty; 32+ bytes is recommended but not enforced.
func NewStateSigner(key []byte) (*StateSigner, error) {
	if len(key) == 0 {
		return nil, errors.New("oauth: state signer key must not be empty")
	}
	// Copy the key so callers can't mutate it out from under us.
	k := make([]byte, len(key))
	copy(k, key)
	return &StateSigner{key: k}, nil
}

// Sign serializes payload to JSON and returns a signed "<body>.<mac>" token
// where both halves are url-safe base64 without padding.
func (s *StateSigner) Sign(payload StatePayload) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("oauth state: marshal: %w", err)
	}
	bodyEncoded := base64.RawURLEncoding.EncodeToString(body)

	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(bodyEncoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return bodyEncoded + "." + sig, nil
}

// Verify parses a signed token, checks its MAC in constant time, verifies
// that it has not expired, and returns the decoded payload on success.
func (s *StateSigner) Verify(token string) (StatePayload, error) {
	var empty StatePayload

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return empty, errors.New("oauth state: malformed token")
	}
	bodyEncoded, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(bodyEncoded))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	// Compare as raw bytes with hmac.Equal for constant-time semantics; comparing
	// the base64 strings is also constant-time but less standard idiom.
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return empty, errors.New("oauth state: signature mismatch")
	}

	bodyBytes, err := base64.RawURLEncoding.DecodeString(bodyEncoded)
	if err != nil {
		return empty, fmt.Errorf("oauth state: decode body: %w", err)
	}

	var payload StatePayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return empty, fmt.Errorf("oauth state: unmarshal: %w", err)
	}

	if payload.Expired(time.Now()) {
		return empty, errors.New("oauth state: expired")
	}

	return payload, nil
}
