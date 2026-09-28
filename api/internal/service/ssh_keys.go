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

// errSSHKeyNotFoundForAccount is returned inside Revoke's transaction when
// ArchiveSSHPublicKey affects zero rows: the key UUID does not exist, does
// not belong to accountUUID, or is already revoked. Revoke maps it to the
// masked apiresp.ErrForbidden (D10); it never reaches a caller directly.
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
// families (design note D9) share one implementation: a handler passes
// either UserContext's own account UUID or the path UUID.
//
// Every method follows the same order of work, per AGENTS.md's
// "Authorization is checked first" convention and design note D9:
//  1. load the target user_accounts row by UUID -- a miss returns the
//     masked apiresp.ErrForbidden, never ErrNotFound (D10, existence
//     masking);
//  2. load the account holder entity via coredb.GetEntityByID -- an
//     archived holder returns the same masked ErrForbidden;
//  3. call Authorize against the account holder entity, propagating any
//     error unchanged;
//  4. only then parse input, touch key data, or write.
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

// loadAuthorizedAccount performs steps 1-3 of every SSHKeyService method's
// order of work: it loads accountUUID's user_accounts row, loads and checks
// its account holder entity for archival, and authorizes op against that
// entity. It returns the loaded row and the account holder entity id
// (needed by callers for the write/audit steps) on success.
func (s *SSHKeyService) loadAuthorizedAccount(ctx context.Context, accountUUID uuid.UUID, op string) (db.UserAccount, int64, error) {
	ua, err := s.q.GetUserAccountByUUID(ctx, accountUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.UserAccount{}, 0, apiresp.ErrForbidden
		}
		return db.UserAccount{}, 0, fmt.Errorf("ssh_keys: load account: %w", err)
	}

	accountHolder := ua.AccountHolder
	entity, err := s.coreQ.GetEntityByID(ctx, accountHolder)
	if err != nil {
		return db.UserAccount{}, 0, fmt.Errorf("ssh_keys: load account holder entity: %w", err)
	}
	if entity.ArchivedAt != nil {
		return db.UserAccount{}, 0, apiresp.ErrForbidden
	}

	if err := s.az.Authorize(ctx, op, &accountHolder); err != nil {
		return db.UserAccount{}, 0, err
	}

	return ua, accountHolder, nil
}

// Register parses and validates publicKeyLine against the module's fixed
// SSH key acceptance policy (sshkey.Parse, design note D6), then registers
// it for accountUUID. label overrides the key's authorized_keys comment
// when non-nil (design note D7). stepUpUsed records whether the caller
// completed a step-up challenge for this call, for the audit trail (D8).
//
// A duplicate active key (Postgres unique violation on
// ssh_public_keys_active_fingerprint_uq) returns apiresp.Conflict with
// detail code users.ssh_key_in_use, identically whether the existing holder
// is the caller or a different account (D2) -- the uniqueness invariant is
// enforced by the schema, not by a Go pre-check, since a pre-check alone
// would lose a concurrent-registration race.
func (s *SSHKeyService) Register(ctx context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (SSHKey, error) {
	ua, accountHolder, err := s.loadAuthorizedAccount(ctx, accountUUID, "update")
	if err != nil {
		return SSHKey{}, err
	}

	parsed, err := sshkey.Parse(publicKeyLine)
	if err != nil {
		return SSHKey{}, mapSSHKeyParseError(err)
	}
	normalizedLabel, err := sshkey.NormalizeLabel(label, parsed.Comment)
	if err != nil {
		return SSHKey{}, mapSSHKeyLabelError(err)
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
// the masked apiresp.ErrForbidden (D10), indistinguishable from an
// authorization denial. stepUpUsed records whether the caller completed a
// step-up challenge for this call, for the audit trail (D8).
func (s *SSHKeyService) Revoke(ctx context.Context, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error {
	ua, accountHolder, err := s.loadAuthorizedAccount(ctx, accountUUID, "update")
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

// sshKeyAuditSnapshot builds the audit-log detail map design note D10
// specifies for both register (create) and revoke (delete): {uuid,
// fingerprint, key_type, label}, plus step_up (D8's per-mutation audit
// convention, matching identities.go's step_up detail).
func sshKeyAuditSnapshot(row db.ModUsersSshPublicKey, stepUpUsed bool) map[string]any {
	return map[string]any{
		"uuid":        row.Uuid.String(),
		"fingerprint": row.FingerprintSha256,
		"key_type":    row.KeyType,
		"label":       row.Label,
		"step_up":     stepUpUsed,
	}
}

// mapSSHKeyParseError maps a sshkey.Parse error to the D6 detail code on
// field "public_key", per the sentinel-to-detail-code table task 002
// defines. A sentinel this function does not recognize is wrapped and
// returned as an internal error rather than silently defaulting to one of
// the known codes.
func mapSSHKeyParseError(err error) error {
	switch {
	case errors.Is(err, sshkey.ErrTooWeak):
		return apiresp.InvalidInput(apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_too_weak", Message: "key is too weak",
		})
	case errors.Is(err, sshkey.ErrUnsupportedType):
		return apiresp.InvalidInput(apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_type_unsupported", Message: "key type is not supported",
		})
	case errors.Is(err, sshkey.ErrInvalid):
		return apiresp.InvalidInput(apiresp.FieldError{
			Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid",
		})
	default:
		return fmt.Errorf("ssh_keys: parse public key: %w", err)
	}
}

// mapSSHKeyLabelError maps a sshkey.NormalizeLabel error to its D7 detail
// code on field "label".
func mapSSHKeyLabelError(err error) error {
	if errors.Is(err, sshkey.ErrLabelTooLong) {
		return apiresp.InvalidInput(apiresp.FieldError{
			Field: "label", Code: "users.ssh_key_label_too_long", Message: "label is too long",
		})
	}
	return fmt.Errorf("ssh_keys: normalize label: %w", err)
}
