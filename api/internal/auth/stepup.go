package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// stepUpPurpose is the fixed purpose string written into step-up tokens and
// email_codes rows. Clients cannot substitute a different purpose.
const stepUpPurpose = "credential_change"

// StepUpTTL is the lifetime of a step-up token. Short on purpose: the token
// is single-use and tied to a single credential-mutation request. Exported so
// the verify handler can report expires_in without duplicating the constant.
const StepUpTTL = 5 * time.Minute

// stepUpHeader is the compact JWS header, base64url-encoded. We fix the
// algorithm and type so the wire format is stable and self-describing.
// alg=HS256, typ=step-up — the custom typ differentiates these tokens from
// the main session JWTs so a leaked session token cannot be replayed as a
// step-up token (different purpose field is the real guard; typ is defense
// in depth).
var stepUpHeader = base64.RawURLEncoding.EncodeToString(
	[]byte(`{"alg":"HS256","typ":"step-up"}`),
)

// stepUpPayload is the JSON payload embedded in a step-up token.
type stepUpPayload struct {
	UserAccountID int64  `json:"uaid"`
	Purpose       string `json:"p"`
	JTI           string `json:"jti"`
	Exp           int64  `json:"exp"`
}

// IssueStepUpToken mints a short-lived HMAC-SHA256 step-up token for the
// given user account. The token is a compact JWS (header.payload.sig) signed
// with secret (JWT_SECRET). It returns the encoded token, the jti (for
// replay tracking), and the expiry time.
func IssueStepUpToken(secret []byte, userAccountID int64, ttl time.Duration) (token, jti string, exp time.Time, err error) {
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return "", "", time.Time{}, fmt.Errorf("stepup: generate jti: %w", err)
	}
	jti = hex.EncodeToString(raw)
	exp = time.Now().Add(ttl)

	payload := stepUpPayload{
		UserAccountID: userAccountID,
		Purpose:       stepUpPurpose,
		JTI:           jti,
		Exp:           exp.Unix(),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("stepup: marshal payload: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := stepUpHeader + "." + encodedPayload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token = signingInput + "." + sig
	return token, jti, exp, nil
}

// ErrStepUpRequired is the sentinel returned by VerifyStepUpToken (and by
// requireStepUp on the handler) when the step-up check fails for any reason.
// Callers map this to a 409 step_up_required response.
var ErrStepUpRequired = errors.New("stepup: step-up token required or invalid")

// VerifyStepUpToken validates a compact JWS step-up token. It checks:
//   - HMAC-SHA256 signature over header.payload using secret.
//   - Expiry: Exp must be in the future.
//   - Purpose: payload.p must equal "credential_change".
//   - Binding: payload.uaid must equal userAccountID.
//   - Single-use: jti must not already be in consumed; on success the jti is
//     stored so subsequent calls with the same token are rejected.
//
// The consumed map is process-local. Cross-process consistency is not required
// for this use case: tokens have a 5-minute TTL and are tied to a single
// browser session. A restart clears the cache, leaving a small replay window
// for tokens issued immediately before the restart — tolerable.
func VerifyStepUpToken(secret []byte, token string, userAccountID int64, consumed *sync.Map) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrStepUpRequired
	}

	header, encodedPayload, sig := parts[0], parts[1], parts[2]

	// Verify the header matches what we produce.
	if header != stepUpHeader {
		return ErrStepUpRequired
	}

	// Verify signature.
	signingInput := header + "." + encodedPayload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return ErrStepUpRequired
	}

	// Decode and parse payload.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return ErrStepUpRequired
	}
	var p stepUpPayload
	if err := json.Unmarshal(payloadJSON, &p); err != nil {
		return ErrStepUpRequired
	}

	// Expiry check.
	if time.Now().Unix() > p.Exp {
		return ErrStepUpRequired
	}

	// Purpose check — prevents session JWTs from being replayed here.
	if p.Purpose != stepUpPurpose {
		return ErrStepUpRequired
	}

	// Binding check — prevents one user's step-up token from being used by another.
	if p.UserAccountID != userAccountID {
		return ErrStepUpRequired
	}

	// Single-use check. Store expiry alongside jti so the janitor can prune.
	if isConsumedJTI(consumed, p.JTI, p.Exp) {
		return ErrStepUpRequired
	}

	return nil
}

// StartStepUpJanitor launches a background goroutine that prunes expired jti
// entries from consumed every minute. Pruning is best-effort: entries that
// expire while the janitor is between ticks are harmless because tokens are
// already past their Exp, so VerifyStepUpToken rejects them regardless. The
// goroutine runs until done is closed.
//
// Callers (main.go) should pass a channel tied to the server's shutdown
// context. The consumed map is the same instance passed to VerifyStepUpToken.
// Each entry in consumed is a jti → struct{}; the janitor re-parses the
// corresponding token to read the exp. Since we store only the jti (not the
// full token), we instead store jti → expUnix so the janitor can prune
// without re-parsing.
//
// Implementation note: VerifyStepUpToken and StartStepUpJanitor use the same
// consumed *sync.Map but store different value types:
//   - VerifyStepUpToken stores struct{} (keyed by jti).
//   - StartStepUpJanitor cannot determine expiry from struct{}.
//
// To keep the two functions independent and the Map homogeneous, we store
// the expiry unix timestamp as int64 in the map value, and VerifyStepUpToken
// uses StoreJTI (below) rather than LoadOrStore directly.
//
// This is an internal package so the coupling is contained.

// StoreConsumedJTI stores jti → expUnix in consumed. Called by
// VerifyStepUpToken on successful verification. Exposed so the janitor
// can read expiry values.
//
// Note: This is intentionally not exported outside this package since
// it is an implementation detail of the consumed-tokens cache. The value
// type is int64 (Unix expiry) so the janitor can prune expired entries.
func storeConsumedJTI(consumed *sync.Map, jti string, expUnix int64) (alreadyUsed bool) {
	_, loaded := consumed.LoadOrStore(jti, expUnix)
	return loaded
}

// verifyAndStore is the single-use check used inside VerifyStepUpToken.
// Returns true if the jti was already in consumed (replay).
func isConsumedJTI(consumed *sync.Map, jti string, expUnix int64) bool {
	return storeConsumedJTI(consumed, jti, expUnix)
}

// StartStepUpJanitor prunes expired jti entries from the consumed map every
// minute. Entries whose stored value (int64 Unix expiry) is in the past are
// deleted. The goroutine exits when done is closed.
func StartStepUpJanitor(consumed *sync.Map, done <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				consumed.Range(func(key, value any) bool {
					exp, ok := value.(int64)
					if !ok || now.Unix() > exp {
						consumed.Delete(key)
					}
					return true
				})
			}
		}
	}()
}
