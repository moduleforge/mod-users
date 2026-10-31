package service

import (
	"errors"
	"fmt"

	"github.com/moduleforge/core-api/apiresp"
)

// Error codes (the `code` of a 409 detail) owned by the identity services,
// beyond the index-mapped ones in sqlstate.go.
const (
	CodeLastCredential     = "users.last_credential"
	CodeGuestUpgradeNeeded = "users.guest_upgrade_required"
	CodePasswordExists     = "users.password_exists"
	CodeIdentifierInUse    = "users.identifier_in_use"
	passwordMinLength      = 12
)

// ErrIdentifierTaken is returned when an identifier (kind, canonical value) is
// already registered, verified or not (users.identifier_taken, 409).
var ErrIdentifierTaken = apiresp.Conflict(apiresp.FieldError{
	Field: "value", Code: CodeIdentifierTaken, Message: "this identifier is already registered",
})

// ErrOIDCIdentityInUse is returned when an active credential already holds the
// (issuer, subject) pair (users.oidc_identity_in_use, 409).
var ErrOIDCIdentityInUse = apiresp.Conflict(apiresp.FieldError{
	Field: "subject", Code: CodeOIDCIdentityInUse, Message: "this external identity is already linked",
})

// ErrSSHKeyInUse is returned when the key's fingerprint is active on another
// credential, or on the same principal (users.ssh_key_in_use, 409).
var ErrSSHKeyInUse = apiresp.Conflict(apiresp.FieldError{
	Field: "public_key", Code: CodeSSHKeyInUse, Message: "this public key is already registered",
})

// ErrLastCredential is returned when removing a credential would leave the
// principal without an active interactive credential (users.last_credential,
// 409).
var ErrLastCredential = apiresp.Conflict(apiresp.FieldError{
	Field: "credential", Code: CodeLastCredential, Message: "the last sign-in credential cannot be removed",
})

// ErrGuestMustUpgrade is returned when an operation needs a registered user
// but the target is a guest (users.guest_upgrade_required, 409).
var ErrGuestMustUpgrade = apiresp.Conflict(apiresp.FieldError{
	Field: "user", Code: CodeGuestUpgradeNeeded, Message: "a guest must upgrade to a full account first",
})

// ErrPasswordExists is returned when the principal (unbound) or the identifier
// (bound) already has an active password credential (users.password_exists,
// 409). Replace a password by revoking the old credential first.
var ErrPasswordExists = apiresp.Conflict(apiresp.FieldError{
	Field: "password", Code: CodePasswordExists, Message: "an active password already exists",
})

// ErrIdentifierInUse is returned when an identifier cannot be removed because a
// restricting foreign key from outside this module's credential tables still
// references it. Credentials bound to the identifier do not block removal: they
// are revoked and their identifier_id is set to NULL. users.identifier_in_use,
// 409.
var ErrIdentifierInUse = apiresp.Conflict(apiresp.FieldError{
	Field: "identifier", Code: CodeIdentifierInUse, Message: "the identifier is bound to credentials and cannot be removed",
})

// ErrInvalidCode is returned for every failed code or token exchange: no open
// challenge, a wrong code, an exhausted attempt budget, an expired or already
// consumed challenge. The causes are deliberately indistinguishable. It wraps
// apiresp.ErrUnauthenticated, so apiresp.WriteError renders it as a 401.
var ErrInvalidCode = fmt.Errorf("%w: invalid or expired code", apiresp.ErrUnauthenticated)

// ErrChallengeCooldown is returned by challenge creation when a challenge of
// the same subject was created within the cooldown window. Callers answer
// "too soon" (or, for anti-enumeration endpoints, silently skip).
var ErrChallengeCooldown = errors.New("service: challenge requested within the cooldown window")

// Helpers for field-level input errors.

func invalidField(field, code, message string) error {
	return apiresp.InvalidInput(apiresp.FieldError{Field: field, Code: code, Message: message})
}
