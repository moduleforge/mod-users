package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/txhelper"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	db "github.com/moduleforge/mod-users/model/db"
)

// sshKeyListDefaultLimit and sshKeyListMaxLimit clamp List's pagination
// parameters, matching the module's list convention (see
// UserAccountService.List).
const (
	sshKeyListDefaultLimit int32 = 20
	sshKeyListMaxLimit     int32 = 200
)

// sshKeyMutateOp is the operation Register and Revoke authorize against the
// account holder entity. Installing or removing a credential that grants
// git-over-SSH access as the account is impersonation-grade authority, so it
// requires `assume` (not the generic `update`): a grant of only `update`, on
// the holder or on the natural_person type entity (architecture.md D13),
// does not confer it. `manage` still does, because it implies `assume`; the
// account holder's own ownership arm also satisfies it, which keeps the
// self-service routes working unchanged. List stays on `read`.
const sshKeyMutateOp = "assume"

// errSSHKeyNotFoundForAccount is returned inside Revoke's transaction when
// ArchiveSSHPublicKey affects zero rows: the key UUID does not exist, does
// not belong to accountUUID, or is already revoked. Revoke maps it to the
// masked apiresp.ErrForbidden; it never reaches a caller directly.
var errSSHKeyNotFoundForAccount = errors.New("ssh_keys: key not found for account")

// SSHKey is the service-layer, public view of one registered SSH public
// key. It carries no internal ids -- only the UUID -- per the module's
// "internal ids are never exposed" convention.
type SSHKey struct {
	UUID        uuid.UUID
	KeyType     string
	Fingerprint string
	PublicKey   string
	Label       string
	CreatedAt   pgtype.Timestamptz
}

// SSHKeyService implements SSH public-key registration, listing, and
// revocation for a user account. Every method is keyed by the target
// account's UUID (not the caller's), so the self-service and operator route
// families (docs/architecture/ssh-keys.md D9) share one implementation: a
// handler passes either UserContext's own account UUID or the path UUID.
//
// Every method follows the same order of work, per AGENTS.md's
// "Authorization is checked first" convention and docs/architecture/
// ssh-keys.md's D9:
//  1. load the target user_accounts row by UUID and call Authorize against
//     its account holder entity (authorizeUserAccount), propagating any
//     error unchanged -- a miss makes a target-0 Authorize call, discards
//     it and returns the masked apiresp.ErrForbidden, never ErrNotFound
//     (existence masking);
//  2. load the account holder entity via coredb.GetEntityByID -- an
//     archived or missing holder returns the same masked ErrForbidden;
//  3. only then touch key data or write.
//
// Authorization is against `assume` for Register/Revoke (sshKeyMutateOp)
// and `read` for List.
//
// The one exception is Register: its stateless key and label validation
// (sshkey.Parse, sshkey.NormalizeLabel) runs before step 1, so a malformed
// key is a 400 even for a caller who may not assume the account.
type SSHKeyService struct {
	db    txhelper.DB
	q     db.Querier
	coreQ coredb.Querier
	az    coreAuthz.Authorizer
	obs   *observer.ObserverGroup
}

// NewSSHKeyService constructs an SSHKeyService.
func NewSSHKeyService(
	pool txhelper.DB,
	q db.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
) *SSHKeyService {
	return &SSHKeyService{
		db:    pool,
		q:     q,
		coreQ: coreQ,
		az:    az,
		obs:   obs,
	}
}

// loadAuthorizedAccount performs steps 1-2 of every SSHKeyService method's
// order of work: it loads accountUUID's user_accounts row and authorizes op
// against its account holder entity (masking a miss), then loads and checks
// the account holder entity for archival. It returns the loaded row and the account holder entity id
// (needed by callers for the write/audit steps) on success.
func (s *SSHKeyService) loadAuthorizedAccount(ctx context.Context, accountUUID uuid.UUID, op string) (db.UserAccount, int64, error) {
	ua, err := authorizeUserAccount(ctx, s.q, s.az, op, accountUUID)
	if err != nil {
		return db.UserAccount{}, 0, err
	}

	accountHolder := ua.AccountHolder
	entity, err := s.coreQ.GetEntityByID(ctx, accountHolder)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.UserAccount{}, 0, apiresp.ErrForbidden
		}
		return db.UserAccount{}, 0, fmt.Errorf("ssh_keys: load account holder entity: %w", err)
	}
	if entity.ArchivedAt != nil {
		return db.UserAccount{}, 0, apiresp.ErrForbidden
	}

	return ua, accountHolder, nil
}

// Register parses and validates publicKeyLine against the module's fixed
// SSH key acceptance policy (sshkey.Parse; see the algorithm policy in
// docs/mod-users-spec.md's Security requirements), then registers it for
// accountUUID. label overrides the key's authorized_keys comment when
// non-nil. stepUpUsed records whether the caller completed a step-up
// challenge for this call, for the audit trail.
//
// A duplicate active key (Postgres unique violation on
// ssh_public_keys_active_fingerprint_uq) returns apiresp.Conflict with
// detail code users.ssh_key_in_use, identically whether the existing holder
// is the caller or a different account (docs/architecture/ssh-keys.md D2)
// -- the uniqueness invariant is
// enforced by the schema, not by a Go pre-check, since a pre-check alone
// would lose a concurrent-registration race.
func (s *SSHKeyService) Register(ctx context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (SSHKey, error) {
	// Stateless key and label validation runs before authorization (400 ahead
	// of 403). The duplicate-fingerprint 409 stays after it, in the insert.
	parsed, err := sshkey.Parse(publicKeyLine)
	if err != nil {
		return SSHKey{}, mapSSHKeyParseError(err)
	}
	normalizedLabel, err := sshkey.NormalizeLabel(label, parsed.Comment)
	if err != nil {
		return SSHKey{}, mapSSHKeyLabelError(err)
	}

	ua, accountHolder, err := s.loadAuthorizedAccount(ctx, accountUUID, sshKeyMutateOp)
	if err != nil {
		return SSHKey{}, err
	}

	var out SSHKey
	var after map[string]any
	err = txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		row, err := db.New(tx).InsertSSHPublicKey(ctx, db.InsertSSHPublicKeyParams{
			UserAccountID:     ua.ID,
			KeyType:           parsed.Type,
			PublicKey:         parsed.Canonical,
			FingerprintSha256: parsed.Fingerprint,
			Label:             normalizedLabel,
		})
		if err != nil {
			return err
		}
		out = toSSHKey(row)
		after = sshKeyAuditSnapshot(row, stepUpUsed)

		return s.safeObserve(ctx, tx, "create", "ssh_public_key", &accountHolder, nil, after)
	})
	if err != nil {
		if isPgUniqueViolation(err) {
			return SSHKey{}, apiresp.Conflict(apiresp.FieldError{
				Field:   "public_key",
				Code:    "users.ssh_key_in_use",
				Message: "this public key is already registered",
			})
		}
		return SSHKey{}, fmt.Errorf("ssh_keys.Register: insert: %w", err)
	}

	s.safeObserveAfterCommit(ctx, "create", "ssh_public_key", &accountHolder, after)

	return out, nil
}

// List returns accountUUID's active (non-revoked) SSH keys, ordered by
// created_at then id ascending, plus the total count of active keys.
// limit/offset are clamped to the module's list convention (default 20,
// max 200).
func (s *SSHKeyService) List(ctx context.Context, accountUUID uuid.UUID, limit, offset int32) ([]SSHKey, int64, error) {
	ua, _, err := s.loadAuthorizedAccount(ctx, accountUUID, "read")
	if err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = sshKeyListDefaultLimit
	} else if limit > sshKeyListMaxLimit {
		limit = sshKeyListMaxLimit
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.q.ListActiveSSHPublicKeysByUserAccount(ctx, db.ListActiveSSHPublicKeysByUserAccountParams{
		UserAccountID: ua.ID,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("ssh_keys.List: list: %w", err)
	}

	total, err := s.q.CountActiveSSHPublicKeysByUserAccount(ctx, ua.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("ssh_keys.List: count: %w", err)
	}

	out := make([]SSHKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSSHKey(row))
	}
	return out, total, nil
}

// Revoke archives the active key identified by keyUUID for accountUUID.
// Any failure to identify an active key belonging to this account --
// unknown UUID, another account's key, or an already-revoked key -- returns
// the masked apiresp.ErrForbidden, indistinguishable from an authorization
// denial. stepUpUsed records whether the caller completed a step-up
// challenge for this call, for the audit trail.
func (s *SSHKeyService) Revoke(ctx context.Context, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error {
	ua, accountHolder, err := s.loadAuthorizedAccount(ctx, accountUUID, sshKeyMutateOp)
	if err != nil {
		return err
	}

	before, err := s.q.GetActiveSSHPublicKeyByUUIDForUserAccount(ctx, db.GetActiveSSHPublicKeyByUUIDForUserAccountParams{
		Uuid:          keyUUID,
		UserAccountID: ua.ID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return apiresp.ErrForbidden
		}
		return fmt.Errorf("ssh_keys.Revoke: load key: %w", err)
	}

	err = txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := db.New(tx).ArchiveSSHPublicKey(ctx, db.ArchiveSSHPublicKeyParams{
			Uuid:          keyUUID,
			UserAccountID: ua.ID,
		})
		if err != nil {
			return fmt.Errorf("ssh_keys.Revoke: archive: %w", err)
		}
		if rows == 0 {
			// Lost a race against a concurrent revoke/re-registration
			// between the load above and this archive. Masked identically
			// to every other "not found for this account" outcome.
			return errSSHKeyNotFoundForAccount
		}

		after := sshKeyAuditSnapshot(before, stepUpUsed)
		return s.safeObserve(ctx, tx, "delete", "ssh_public_key", &accountHolder, nil, after)
	})
	if err != nil {
		if errors.Is(err, errSSHKeyNotFoundForAccount) {
			return apiresp.ErrForbidden
		}
		return err
	}

	s.safeObserveAfterCommit(ctx, "delete", "ssh_public_key", &accountHolder, sshKeyAuditSnapshot(before, stepUpUsed))

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// safeObserve calls obs.Observe only when s.obs is non-nil, mirroring
// IdentitiesHandler.safeObserve (api/internal/handlers/identities.go).
func (s *SSHKeyService) safeObserve(ctx context.Context, tx pgx.Tx, op, resource string, targetEntityID *int64, before, after any) error {
	if s.obs == nil {
		return nil
	}
	return s.obs.Observe(ctx, tx, op, resource, targetEntityID, before, after)
}

// safeObserveAfterCommit calls obs.ObserveAfterCommit only when s.obs is
// non-nil, mirroring IdentitiesHandler.safeObserveAfterCommit.
func (s *SSHKeyService) safeObserveAfterCommit(ctx context.Context, op, resource string, targetEntityID *int64, after any) {
	if s.obs == nil {
		return
	}
	s.obs.ObserveAfterCommit(ctx, op, resource, targetEntityID, after)
}

// toSSHKey converts a generated model row to the service-layer SSHKey type.
func toSSHKey(row db.ModUsersSshPublicKey) SSHKey {
	return SSHKey{
		UUID:        row.Uuid,
		KeyType:     row.KeyType,
		Fingerprint: row.FingerprintSha256,
		PublicKey:   row.PublicKey,
		Label:       row.Label,
		CreatedAt:   row.CreatedAt,
	}
}

// sshKeyAuditSnapshot builds the audit-log detail map for both register
// (create) and revoke (delete): {uuid, fingerprint, key_type, label}, plus
// step_up, matching identities.go's per-mutation step_up detail convention.
func sshKeyAuditSnapshot(row db.ModUsersSshPublicKey, stepUpUsed bool) map[string]any {
	return map[string]any{
		"uuid":        row.Uuid.String(),
		"fingerprint": row.FingerprintSha256,
		"key_type":    row.KeyType,
		"label":       row.Label,
		"step_up":     stepUpUsed,
	}
}

// sshKeyParseFieldError maps a sshkey.Parse error to its detail on field
// "public_key", per the sentinel-to-detail-code table api/internal/sshkey
// defines. ok is false for a sentinel it does not recognize.
func sshKeyParseFieldError(err error) (fe apiresp.FieldError, ok bool) {
	switch {
	case errors.Is(err, sshkey.ErrTooWeak):
		return apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_too_weak", Message: "key is too weak",
		}, true
	case errors.Is(err, sshkey.ErrUnsupportedType):
		return apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_type_unsupported", Message: "key type is not supported",
		}, true
	case errors.Is(err, sshkey.ErrInvalid):
		return apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid",
		}, true
	default:
		return apiresp.FieldError{}, false
	}
}

// sshKeyLabelFieldError maps a sshkey.NormalizeLabel error to its detail on
// field "label". ok is false for a sentinel it does not recognize.
func sshKeyLabelFieldError(err error) (fe apiresp.FieldError, ok bool) {
	switch {
	case errors.Is(err, sshkey.ErrLabelTooLong):
		return apiresp.FieldError{
			Field: "label", Code: "users.ssh_key_label_too_long", Message: "label is too long",
		}, true
	case errors.Is(err, sshkey.ErrLabelInvalidChars):
		return apiresp.FieldError{
			Field: "label", Code: "users.ssh_key_label_invalid", Message: "label contains invalid characters",
		}, true
	default:
		return apiresp.FieldError{}, false
	}
}

// mapSSHKeyParseError maps a sshkey.Parse error to its invalid-input error.
// A sentinel this function does not recognize is wrapped and returned as an
// internal error rather than silently defaulting to one of the known codes.
func mapSSHKeyParseError(err error) error {
	if fe, ok := sshKeyParseFieldError(err); ok {
		return apiresp.InvalidInput(fe)
	}
	return fmt.Errorf("ssh_keys: parse public key: %w", err)
}

// mapSSHKeyLabelError maps a sshkey.NormalizeLabel error to its
// invalid-input error.
func mapSSHKeyLabelError(err error) error {
	if fe, ok := sshKeyLabelFieldError(err); ok {
		return apiresp.InvalidInput(fe)
	}
	return fmt.Errorf("ssh_keys: normalize label: %w", err)
}

// ValidateSSHKeyShape runs the stateless key and label checks Register
// performs (sshkey.Parse, sshkey.NormalizeLabel) and returns them as field
// errors with the same detail codes Register produces. It touches no state,
// so an HTTP handler can call it before any step-up or authorization gate and
// report a malformed request as 400. publicKeyLine must be non-empty (the
// caller's required-field check owns that). An unrecognized parser sentinel
// is reported as users.ssh_key_invalid; Register remains the authoritative
// check for in-process callers.
func ValidateSSHKeyShape(publicKeyLine string, label *string) []apiresp.FieldError {
	parsed, err := sshkey.Parse(publicKeyLine)
	if err != nil {
		if fe, ok := sshKeyParseFieldError(err); ok {
			return []apiresp.FieldError{fe}
		}
		return []apiresp.FieldError{{Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid"}}
	}
	if _, err := sshkey.NormalizeLabel(label, parsed.Comment); err != nil {
		if fe, ok := sshKeyLabelFieldError(err); ok {
			return []apiresp.FieldError{fe}
		}
		return []apiresp.FieldError{{Field: "label", Code: "users.ssh_key_label_invalid", Message: "label contains invalid characters"}}
	}
	return nil
}
