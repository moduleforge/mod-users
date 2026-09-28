//go:build integration

package authz_test

// ssh_keys_integration_test.go proves SSHKeyService and SSHKeyResolver
// (api/internal/service/ssh_keys.go, ssh_key_resolver.go) against a real
// Postgres, per phase-01-ssh-public-keys/003-add-ssh-key-service-and-
// resolver.md's Requirement 14. It reuses this package's existing
// TestMain, checkPrereqs, resolveHost, wireServices, and seeding helpers
// (integPool, integAZ, seedUser, actorCtx) -- see authz_integration_test.go's
// header comment for run instructions and the macOS Docker Desktop
// host-resolution convention (AUTHZ_DEV_PG_HOST=localhost). Do not
// rediscover it.
//
// Scenarios verified (per the task doc's Requirement 14):
//  1. Self-registration succeeds, and ResolveActor resolves the registered
//     key to that user's account_holder.
//  2. A second active registration of the same key -- by the same user or
//     a different one -- returns users.ssh_key_in_use, and exactly one
//     active row exists for that fingerprint.
//  3. After Revoke, the very next ResolveActor call returns
//     ErrUnknownSSHKey, and the same key can then be registered again.
//  4. After the account holder entity is archived (the same
//     coreQ.ArchiveEntity call UserAccountService.Delete makes
//     internally), ResolveActor returns ErrUnknownSSHKey.
//  5. A non-admin user cannot List, Register, or Revoke for another
//     account (masked ErrForbidden); a wildcard-manage user can.
//  6. An unknown key returns ErrUnknownSSHKey.
//  7. Audit rows are written for both register and revoke, via a
//     recording observer.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	gossh "golang.org/x/crypto/ssh"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/service"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// ---------------------------------------------------------------------------
// Construction helpers
// ---------------------------------------------------------------------------

// newIntegSSHKeyService builds a real SSHKeyService wired to integPool and
// integAZ (this package's shared integration Authorizer), with obs as its
// ObserverGroup.
func newIntegSSHKeyService(obs *observer.ObserverGroup) *service.SSHKeyService {
	return service.NewSSHKeyService(integPool, usersdb.New(integPool), coredb.New(integPool), integAZ, obs)
}

// newIntegSSHKeyResolver builds a real SSHKeyResolver wired to integPool.
func newIntegSSHKeyResolver() *service.SSHKeyResolver {
	return service.NewSSHKeyResolver(usersdb.New(integPool), coredb.New(integPool))
}

// accountUUIDForEntity resolves entityID's user_accounts.uuid, needed
// because SSHKeyService is keyed by account UUID, not entity id, while
// seedUser returns only the entity id.
func accountUUIDForEntity(t *testing.T, entityID int64) uuid.UUID {
	t.Helper()
	ua, err := usersdb.New(integPool).GetUserAccountByAccountHolder(context.Background(), entityID)
	if err != nil {
		t.Fatalf("accountUUIDForEntity(%d): %v", entityID, err)
	}
	return ua.Uuid
}

// genIntegKey generates a fresh ed25519 keypair and returns both the parsed
// public key (for ResolveActor) and its authorized_keys line (for
// Register), so both derive from the same key material.
func genIntegKey(t *testing.T) (pub gossh.PublicKey, line string) {
	t.Helper()
	pk, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genIntegKey: generate: %v", err)
	}
	sshPub, err := gossh.NewPublicKey(pk)
	if err != nil {
		t.Fatalf("genIntegKey: wrap: %v", err)
	}
	line = strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sshPub))) + " ssh-keys-integ-test"
	return sshPub, line
}

// assertIntegFieldError routes err through apiresp.WriteError and asserts
// the resulting envelope's status and single field-level detail, mirroring
// api/internal/service/ssh_keys_test.go's assertFieldError (apiresp
// exposes no public detail-carrying interface to recover Code from err
// directly).
func assertIntegFieldError(t *testing.T, err error, wantStatus int, wantField, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/self/ssh-keys", nil)
	rr := httptest.NewRecorder()
	apiresp.WriteError(rr, req, err)

	if rr.Code != wantStatus {
		t.Fatalf("status: got %d, want %d (err=%v)", rr.Code, wantStatus, err)
	}
	var body struct {
		Error struct {
			Details []apiresp.FieldError `json:"details"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(rr.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("body not valid JSON: %v, body=%s", jsonErr, rr.Body.String())
	}
	if len(body.Error.Details) != 1 {
		t.Fatalf("error.details: got %+v, want exactly one entry", body.Error.Details)
	}
	got := body.Error.Details[0]
	if got.Field != wantField || got.Code != wantCode {
		t.Errorf("error.details[0]: got {Field:%q Code:%q}, want {Field:%q Code:%q}", got.Field, got.Code, wantField, wantCode)
	}
}

// activeFingerprintRowCount counts active (non-archived) rows in
// mod_users.ssh_public_keys for fingerprint, via direct SQL -- the
// simplest way to independently verify D2's "exactly one active row"
// invariant without going back through the service layer under test.
func activeFingerprintRowCount(t *testing.T, fingerprint string) int {
	t.Helper()
	const sql = `SELECT count(*) FROM mod_users.ssh_public_keys WHERE fingerprint_sha256 = $1 AND archived_at IS NULL`
	var count int
	if err := integPool.QueryRow(context.Background(), sql, fingerprint).Scan(&count); err != nil {
		t.Fatalf("activeFingerprintRowCount: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// Audit recorder
// ---------------------------------------------------------------------------

// sshAuditRecord captures one Observe/ObserveAfterCommit call.
type sshAuditRecord struct {
	op             string
	resource       string
	targetEntityID int64
}

// sshAuditRecorder is a observer.MutationObserver that records every call
// it receives, in and after the transaction, for scenario 7 (audit rows
// are written for register and revoke).
type sshAuditRecorder struct {
	mu          sync.Mutex
	inTx        []sshAuditRecord
	afterCommit []sshAuditRecord
}

func (r *sshAuditRecorder) Observe(_ context.Context, _ pgx.Tx, op, resource string, targetEntityID *int64, _, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id int64
	if targetEntityID != nil {
		id = *targetEntityID
	}
	r.inTx = append(r.inTx, sshAuditRecord{op: op, resource: resource, targetEntityID: id})
	return nil
}

func (r *sshAuditRecorder) ObserveAfterCommit(_ context.Context, op, resource string, targetEntityID *int64, _ any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id int64
	if targetEntityID != nil {
		id = *targetEntityID
	}
	r.afterCommit = append(r.afterCommit, sshAuditRecord{op: op, resource: resource, targetEntityID: id})
}

var _ observer.MutationObserver = (*sshAuditRecorder)(nil)

// hasRecord reports whether records contains an entry matching op,
// resource, and targetEntityID.
func hasRecord(records []sshAuditRecord, op, resource string, targetEntityID int64) bool {
	for _, r := range records {
		if r.op == op && r.resource == resource && r.targetEntityID == targetEntityID {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Scenario 1: self-registration then resolution
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_SelfRegisterThenResolve(t *testing.T) {
	userID := seedUser(t, "ssh-self-a@example.com", false)
	accountUUID := accountUUIDForEntity(t, userID)
	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	resolver := newIntegSSHKeyResolver()

	pub, line := genIntegKey(t)

	if _, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false); err != nil {
		t.Fatalf("Register: %v", err)
	}

	actorID, err := resolver.ResolveActor(context.Background(), pub)
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}
	if actorID != userID {
		t.Errorf("ResolveActor actorID: got %d, want %d", actorID, userID)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: duplicate active registration -> conflict, single active row
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_DuplicateRegistration_BySameUser_ReturnsConflict(t *testing.T) {
	userID := seedUser(t, "ssh-dup-same@example.com", false)
	accountUUID := accountUUIDForEntity(t, userID)
	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	pub, line := genIntegKey(t)

	if _, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	_, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false)
	assertIntegFieldError(t, err, http.StatusConflict, "public_key", "users.ssh_key_in_use")

	if got := activeFingerprintRowCount(t, sshkey.Fingerprint(pub)); got != 1 {
		t.Errorf("active rows for fingerprint: got %d, want 1", got)
	}
}

func TestInteg_SSHKeys_DuplicateRegistration_ByDifferentUser_ReturnsConflict(t *testing.T) {
	ownerID := seedUser(t, "ssh-dup-owner@example.com", false)
	ownerUUID := accountUUIDForEntity(t, ownerID)
	otherID := seedUser(t, "ssh-dup-other@example.com", false)
	otherUUID := accountUUIDForEntity(t, otherID)
	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	pub, line := genIntegKey(t)

	if _, err := svc.Register(actorCtx(ownerID), ownerUUID, line, nil, false); err != nil {
		t.Fatalf("owner Register: %v", err)
	}
	// otherID registering for their own account (otherUUID) with the same
	// key line -- the identical response as the same-user case (D2: never
	// reveals which account holds the key).
	_, err := svc.Register(actorCtx(otherID), otherUUID, line, nil, false)
	assertIntegFieldError(t, err, http.StatusConflict, "public_key", "users.ssh_key_in_use")

	if got := activeFingerprintRowCount(t, sshkey.Fingerprint(pub)); got != 1 {
		t.Errorf("active rows for fingerprint: got %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: revoke -> immediate resolve failure -> re-registration works
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_RevokeThenResolveFails_ThenReRegisterSucceeds(t *testing.T) {
	userID := seedUser(t, "ssh-revoke@example.com", false)
	accountUUID := accountUUIDForEntity(t, userID)
	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	resolver := newIntegSSHKeyResolver()
	pub, line := genIntegKey(t)

	key, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := resolver.ResolveActor(context.Background(), pub); err != nil {
		t.Fatalf("pre-revoke ResolveActor: %v", err)
	}

	if err := svc.Revoke(actorCtx(userID), accountUUID, key.UUID, false); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, err := resolver.ResolveActor(context.Background(), pub); !errors.Is(err, service.ErrUnknownSSHKey) {
		t.Errorf("post-revoke ResolveActor: got %v, want ErrUnknownSSHKey", err)
	}

	if _, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false); err != nil {
		t.Errorf("re-registration after revoke: got %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: archived account holder -> resolve fails
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_ArchivedHolder_ResolveFails(t *testing.T) {
	userID := seedUser(t, "ssh-archived@example.com", false)
	accountUUID := accountUUIDForEntity(t, userID)
	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	resolver := newIntegSSHKeyResolver()
	pub, line := genIntegKey(t)

	if _, err := svc.Register(actorCtx(userID), accountUUID, line, nil, false); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := resolver.ResolveActor(context.Background(), pub); err != nil {
		t.Fatalf("pre-archival ResolveActor: %v", err)
	}

	// Archive the account holder entity via the same underlying query
	// UserAccountService.Delete uses internally (coreQtx.ArchiveEntity(ctx,
	// entityRow.Uuid); see api/internal/service/user_accounts.go's Delete).
	// Constructing a full UserAccountService here would additionally
	// require a NaturalPersonServicer and a types.Resolver this scenario
	// has no other use for; calling the same query it calls proves the
	// identical DB-level effect ResolveActor must react to.
	coreQ := coredb.New(integPool)
	entity, err := coreQ.GetEntityByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("load entity: %v", err)
	}
	if err := coreQ.ArchiveEntity(context.Background(), entity.Uuid); err != nil {
		t.Fatalf("archive entity: %v", err)
	}

	if _, err := resolver.ResolveActor(context.Background(), pub); !errors.Is(err, service.ErrUnknownSSHKey) {
		t.Errorf("post-archival ResolveActor: got %v, want ErrUnknownSSHKey", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: operator authorization
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_OperatorAuthorization(t *testing.T) {
	targetID := seedUser(t, "ssh-op-target@example.com", false)
	targetUUID := accountUUIDForEntity(t, targetID)
	nonAdminID := seedUser(t, "ssh-op-nonadmin@example.com", false)
	adminID := seedUser(t, "ssh-op-admin@example.com", true)

	svc := newIntegSSHKeyService(observer.NewObserverGroup())
	_, line := genIntegKey(t)

	if _, _, err := svc.List(actorCtx(nonAdminID), targetUUID, 0, 0); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("non-admin List: got %v, want ErrForbidden", err)
	}
	if _, err := svc.Register(actorCtx(nonAdminID), targetUUID, line, nil, false); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("non-admin Register: got %v, want ErrForbidden", err)
	}

	key, err := svc.Register(actorCtx(adminID), targetUUID, line, nil, false)
	if err != nil {
		t.Fatalf("admin Register: %v", err)
	}
	if _, _, err := svc.List(actorCtx(adminID), targetUUID, 0, 0); err != nil {
		t.Errorf("admin List: got %v, want nil", err)
	}

	if err := svc.Revoke(actorCtx(nonAdminID), targetUUID, key.UUID, false); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("non-admin Revoke: got %v, want ErrForbidden", err)
	}
	if err := svc.Revoke(actorCtx(adminID), targetUUID, key.UUID, false); err != nil {
		t.Errorf("admin Revoke: got %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: unknown key
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_UnknownKey_ResolveFails(t *testing.T) {
	resolver := newIntegSSHKeyResolver()
	pub, _ := genIntegKey(t) // never registered

	if _, err := resolver.ResolveActor(context.Background(), pub); !errors.Is(err, service.ErrUnknownSSHKey) {
		t.Errorf("unknown key ResolveActor: got %v, want ErrUnknownSSHKey", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: audit rows for register and revoke
// ---------------------------------------------------------------------------

func TestInteg_SSHKeys_AuditRowsWrittenForRegisterAndRevoke(t *testing.T) {
	userID := seedUser(t, "ssh-audit@example.com", false)
	accountUUID := accountUUIDForEntity(t, userID)
	rec := &sshAuditRecorder{}
	svc := newIntegSSHKeyService(observer.NewObserverGroup(rec))
	_, line := genIntegKey(t)

	key, err := svc.Register(actorCtx(userID), accountUUID, line, nil, true)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.Revoke(actorCtx(userID), accountUUID, key.UUID, true); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if !hasRecord(rec.inTx, "create", "ssh_public_key", userID) {
		t.Errorf("no in-tx create audit record: %+v", rec.inTx)
	}
	if !hasRecord(rec.afterCommit, "create", "ssh_public_key", userID) {
		t.Errorf("no post-commit create audit record: %+v", rec.afterCommit)
	}
	if !hasRecord(rec.inTx, "delete", "ssh_public_key", userID) {
		t.Errorf("no in-tx delete audit record: %+v", rec.inTx)
	}
	if !hasRecord(rec.afterCommit, "delete", "ssh_public_key", userID) {
		t.Errorf("no post-commit delete audit record: %+v", rec.afterCommit)
	}
}
