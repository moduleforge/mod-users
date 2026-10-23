package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
)

// Audit op vocabulary (API surface section 10.1). mod-audit accepts only these
// verbs; under the propagate policy a rejected verb rolls back the operation.
// "assume" is gone and is never used.
const (
	opCreate = "create"
	opUpdate = "update"
	opDelete = "delete"
	opRevoke = "revoke"
	opLogin  = "login"
)

// Observer resources this package emits.
const (
	resourceUser           = "user"
	resourceUserIdentifier = "user_identifier"
	resourceCredential     = "credential"
	resourceSession        = "session"
)

// observeInTx runs the in-transaction observers for one event. It is a no-op
// when obs is nil (a service constructed without observers, as in unit tests).
// target is the entity the observation is about.
func observeInTx(ctx context.Context, obs *observer.ObserverGroup, tx pgx.Tx, op, resource string, target int64, before, after any) error {
	if obs == nil {
		return nil
	}
	return obs.Observe(ctx, tx, op, resource, &target, before, after)
}

// actingProfileParam returns the acting profile on ctx as a nullable id, for
// created_by_profile_id columns. It is NULL when ctx carries no AuthContext.
func actingProfileParam(ctx context.Context) pgtype.Int8 {
	if id, ok := opctx.ProfileID(ctx); ok && id > 0 {
		return pgtype.Int8{Int64: id, Valid: true}
	}
	return pgtype.Int8{}
}

// withActingProfile returns ctx carrying an AuthContext for principalID acting
// as profileID, with no session or credential. It is the context a sign-up
// flow uses for the observations of the entities it just created (API surface
// section 10.1, "Acting profile for unauthenticated flows"): the acting
// profile is the profile the flow creates, and the principal is its holder.
func withActingProfile(ctx context.Context, principalID, profileID int64) context.Context {
	return opctx.WithAuthContext(ctx, opctx.AuthContext{PrincipalID: principalID, ProfileID: profileID})
}

// CanonicalizeEmail returns the canonical form of an email identifier value:
// trimmed and lowercased. The table checks the same form.
func CanonicalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// maxEmailLen is the RFC 5321 limit on a mailbox address.
const maxEmailLen = 254

// validateEmail reports a field error when canon is not a bare, syntactically
// valid address. canon must already be canonical.
func validateEmail(canon string) error {
	if canon == "" {
		return invalidField("email", "users.email_required", "email is required")
	}
	if len(canon) > maxEmailLen {
		return invalidField("email", "users.email_invalid", "email is too long")
	}
	addr, err := mail.ParseAddress(canon)
	if err != nil || addr.Address != canon {
		return invalidField("email", "users.email_invalid", "email is not a valid address")
	}
	return nil
}

// ValidateEmail reports the field error the identifier services would return
// for value (canonicalized first), so an HTTP handler can answer 400 before
// any authorization or state check.
func ValidateEmail(value string) error {
	return validateEmail(CanonicalizeEmail(value))
}

// ValidatePassword reports the field error CreatePasswordInTx would return
// for a password of the wrong length, so an HTTP handler can answer 400
// before any authorization or state check.
func ValidatePassword(password string) error {
	if len(password) < passwordMinLength {
		return invalidField("password", "users.password_too_short", "password must be at least 12 characters")
	}
	if len(password) > maxPasswordBytes {
		return invalidField("password", "users.password_too_long", "password is too long")
	}
	return nil
}

// HashToken returns the lowercase hex SHA-256 of a high-entropy opaque token
// (an anonymous device token, an API key secret, a password-reset or handoff
// code). It is deterministic, so a token can be looked up by its hash. It must
// only be used for tokens with at least 128 bits of entropy; low-entropy codes
// use bcrypt (ChallengeService).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokenMatchesHash reports whether HashToken(token) equals hash, in constant
// time.
func TokenMatchesHash(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(hash)) == 1
}

// randomHex returns n random bytes as lowercase hex.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// timestamptz wraps t for a TIMESTAMPTZ parameter.
func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// nullableInt8 returns a nullable id; nil is NULL.
func nullableInt8(id *int64) pgtype.Int8 {
	if id == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *id, Valid: true}
}

// pgConstraint reports the violated constraint name and SQLSTATE of a
// PostgreSQL error anywhere in err's chain.
func pgConstraint(err error) (sqlstate, constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", "", false
	}
	return pgErr.Code, pgErr.ConstraintName, true
}

// foreignKeyViolationSQLState is the standard Postgres code for a foreign-key
// violation.
const foreignKeyViolationSQLState = "23503"

// mapUniqueViolation translates a unique violation on one of the named
// indexes (sqlstate.go) into its sentinel 409 error. It returns err unchanged
// for anything else, so callers wrap the result uniformly.
func mapUniqueViolation(err error) error {
	code, ok := UniqueViolationCode(err)
	if !ok {
		return err
	}
	switch code {
	case CodeIdentifierTaken:
		return ErrIdentifierTaken
	case CodeOIDCIdentityInUse:
		return ErrOIDCIdentityInUse
	case CodeSSHKeyInUse:
		return ErrSSHKeyInUse
	}
	return err
}

// Password unique indexes (cred_password), mapped locally to ErrPasswordExists.
const (
	passwordActiveIdentifierIndex = "cred_password_active_identifier_uq"
	passwordActiveUnboundIndex    = "cred_password_active_unbound_uq"
)

func isPasswordExistsViolation(err error) bool {
	state, name, ok := pgConstraint(err)
	return ok && state == uniqueViolationSQLState &&
		(name == passwordActiveIdentifierIndex || name == passwordActiveUnboundIndex)
}
