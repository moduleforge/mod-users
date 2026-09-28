package service

// Unit tests for SSHKeyService (Register, List, Revoke). No real Postgres is
// used: db.Querier and coredb.Querier are satisfied by stubSSHQuerier and
// stubCoreQuerier below, and the transaction-scoped queries Register/Revoke
// issue via db.New(tx) are satisfied by fakeSSHTx/fakeSSHDB. Integration
// coverage against a real database lives in
// api/internal/authz/ssh_keys_integration_test.go.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/ssh"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/mod-users/model/db"
)

// ---------------------------------------------------------------------------
// Authorizer stubs
// ---------------------------------------------------------------------------

// recordingAuthorizer records whether Authorize was called and returns a
// fixed decision (nil to allow, err to deny). Used to prove call ordering
// (Authorize before any SSH key query) and unchanged-error propagation.
type recordingAuthorizer struct {
	called   bool
	lastOp   string
	decision error
}

func (a *recordingAuthorizer) Authorize(_ context.Context, op string, _ *int64) error {
	a.called = true
	a.lastOp = op
	return a.decision
}

var _ coreAuthz.Authorizer = (*recordingAuthorizer)(nil)

// ---------------------------------------------------------------------------
// stubCoreQuerier -- coredb.Querier, only GetEntityByID overridden
// ---------------------------------------------------------------------------

// stubCoreQuerier implements coredb.Querier by embedding a nil interface
// value: any method this test suite does not explicitly override panics
// loudly if called, which is exactly what should happen, since
// SSHKeyService/SSHKeyResolver call only GetEntityByID on this dependency.
type stubCoreQuerier struct {
	coredb.Querier

	entity    coredb.GetEntityByIDRow
	entityErr error
}

func (s *stubCoreQuerier) GetEntityByID(_ context.Context, _ int64) (coredb.GetEntityByIDRow, error) {
	return s.entity, s.entityErr
}

var _ coredb.Querier = (*stubCoreQuerier)(nil)

// ---------------------------------------------------------------------------
// stubSSHQuerier -- db.Querier, only the methods SSHKeyService/
// SSHKeyResolver call are overridden
// ---------------------------------------------------------------------------

// stubSSHQuerier implements db.Querier the same way stubCoreQuerier
// implements coredb.Querier: embed a nil interface so any unexercised
// method panics if called, and explicitly override the handful of methods
// under test. InsertSSHPublicKey/ArchiveSSHPublicKey are overridden purely
// as write-call tripwires (writeCalled) for SSHKeyResolver's "no writes"
// test -- SSHKeyService never reaches s.q for those; it uses db.New(tx),
// satisfied instead by fakeSSHTx.
type stubSSHQuerier struct {
	db.Querier

	accounts map[uuid.UUID]db.UserAccount

	listRows   []db.ModUsersSshPublicKey
	listErr    error
	countTotal int64
	countErr   error

	activeKey db.ModUsersSshPublicKey
	activeErr error

	resolveAccountHolder int64
	resolveErr           error

	writeCalled bool
}

func newStubSSHQuerier(accounts ...db.UserAccount) *stubSSHQuerier {
	m := make(map[uuid.UUID]db.UserAccount, len(accounts))
	for _, ua := range accounts {
		m[ua.Uuid] = ua
	}
	return &stubSSHQuerier{accounts: m}
}

func (s *stubSSHQuerier) GetUserAccountByUUID(_ context.Context, id uuid.UUID) (db.UserAccount, error) {
	if ua, ok := s.accounts[id]; ok {
		return ua, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

func (s *stubSSHQuerier) ListActiveSSHPublicKeysByUserAccount(_ context.Context, _ db.ListActiveSSHPublicKeysByUserAccountParams) ([]db.ModUsersSshPublicKey, error) {
	return s.listRows, s.listErr
}

func (s *stubSSHQuerier) CountActiveSSHPublicKeysByUserAccount(_ context.Context, _ int64) (int64, error) {
	return s.countTotal, s.countErr
}

func (s *stubSSHQuerier) GetActiveSSHPublicKeyByUUIDForUserAccount(_ context.Context, _ db.GetActiveSSHPublicKeyByUUIDForUserAccountParams) (db.ModUsersSshPublicKey, error) {
	return s.activeKey, s.activeErr
}

func (s *stubSSHQuerier) ResolveActiveSSHPublicKey(_ context.Context, _ db.ResolveActiveSSHPublicKeyParams) (int64, error) {
	return s.resolveAccountHolder, s.resolveErr
}

func (s *stubSSHQuerier) InsertSSHPublicKey(_ context.Context, _ db.InsertSSHPublicKeyParams) (db.ModUsersSshPublicKey, error) {
	s.writeCalled = true
	return db.ModUsersSshPublicKey{}, nil
}

func (s *stubSSHQuerier) ArchiveSSHPublicKey(_ context.Context, _ db.ArchiveSSHPublicKeyParams) (int64, error) {
	s.writeCalled = true
	return 0, nil
}

var _ db.Querier = (*stubSSHQuerier)(nil)

// ---------------------------------------------------------------------------
// fakeSSHTx / fakeSSHDB -- pgx.Tx / txhelper.DB for Register/Revoke's
// in-transaction db.New(tx) calls
// ---------------------------------------------------------------------------

// fakeSSHTx implements pgx.Tx. QueryRow answers InsertSSHPublicKey (:one);
// Exec answers ArchiveSSHPublicKey (:execrows). Both are independently
// configurable per test.
type fakeSSHTx struct {
	insertRow db.ModUsersSshPublicKey
	insertErr error

	archiveRows int64
	archiveErr  error
}

func (f *fakeSSHTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	if f.archiveErr != nil {
		return pgconn.CommandTag{}, f.archiveErr
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.archiveRows)), nil
}

func (f *fakeSSHTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return &fakeSSHKeyRow{row: f.insertRow, err: f.insertErr}
}

func (f *fakeSSHTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &emptyRows{}, nil
}

func (f *fakeSSHTx) Begin(_ context.Context) (pgx.Tx, error) { return f, nil }
func (f *fakeSSHTx) Commit(_ context.Context) error          { return nil }
func (f *fakeSSHTx) Rollback(_ context.Context) error        { return nil }
func (f *fakeSSHTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeSSHTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeSSHTx) LargeObjects() pgx.LargeObjects                             { return pgx.LargeObjects{} }
func (f *fakeSSHTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeSSHTx) Conn() *pgx.Conn { return nil }

var _ pgx.Tx = (*fakeSSHTx)(nil)

// fakeSSHKeyRow implements pgx.Row for InsertSSHPublicKey's 9-column scan.
type fakeSSHKeyRow struct {
	row db.ModUsersSshPublicKey
	err error
}

func (r *fakeSSHKeyRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) < 9 {
		return nil
	}
	if d, ok := dest[0].(*int64); ok {
		*d = r.row.ID
	}
	if d, ok := dest[1].(*uuid.UUID); ok {
		*d = r.row.Uuid
	}
	if d, ok := dest[2].(*int64); ok {
		*d = r.row.UserAccountID
	}
	if d, ok := dest[3].(*string); ok {
		*d = r.row.KeyType
	}
	if d, ok := dest[4].(*string); ok {
		*d = r.row.PublicKey
	}
	if d, ok := dest[5].(*string); ok {
		*d = r.row.FingerprintSha256
	}
	if d, ok := dest[6].(*string); ok {
		*d = r.row.Label
	}
	// CreatedAt (pgtype.Timestamptz) and ArchivedAt (*time.Time) are left
	// zero-valued; no test asserts on them.
	return nil
}

// fakeSSHDB implements txhelper.DB; BeginTx returns the configured
// fakeSSHTx.
type fakeSSHDB struct{ tx *fakeSSHTx }

func (f *fakeSSHDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.tx, nil
}

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// activeEntity returns a coredb.GetEntityByIDRow for entityID with no
// archival timestamp.
func activeEntity(entityID int64) coredb.GetEntityByIDRow {
	return coredb.GetEntityByIDRow{ID: entityID, FundamentalTypeSlug: "natural_person"}
}

// archivedEntity returns a coredb.GetEntityByIDRow for entityID with a
// non-nil ArchivedAt.
func archivedEntity(entityID int64) coredb.GetEntityByIDRow {
	archivedAt := time.Now().UTC()
	return coredb.GetEntityByIDRow{ID: entityID, FundamentalTypeSlug: "natural_person", ArchivedAt: &archivedAt}
}

// genEd25519Line builds a valid "ssh-ed25519 <base64> <comment>"
// authorized_keys line, accepted by sshkey.Parse.
func genEd25519Line(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap ed25519 key: %v", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	return line
}

// dsaFixtureLine is a fixed, previously-generated ssh-dss authorized_keys
// line -- accepted by ssh.ParseAuthorizedKey but refused by the D6
// allow-list -- used only to exercise the ErrUnsupportedType mapping. It
// carries no live private key material.
const dsaFixtureLine = "ssh-dss AAAAB3NzaC1kc3MAAACBAIibNa/UzcRaMAlmSFZULXNL6OJ4TmjixAy6xGBQKa9yNgeyxQH7ui0N+yafI4r9XpjJl2FSgyMVrET6B8CIzRig8VV8AKgSywPPGBWqbKAk75Zbm/orPj96Q1C34qz+5t2tYiVqmmzbn0BQjTLid3LbimpexgJdXsFgNbXXppl1AAAAFQCHFKTNWmU/9vZ0LbZi8FMtYZgkQQAAAIAikNXAq3tVinv2GwwuZS2/TSlqZivr+iLq6JdM53H/gOz3RWC5B0otnCPUw5GG+VDBuPqW87aCV145YdyQ8S+hgqPSEJPi/R6TMJB6l0B7bNxdob17nZr75N1dzpIOgUrPK8wdko81hzrJ/PvVjInhXWOt+Dr6z5AE7Bg8CZgwpgAAAIAEUF2nIU6i6XWfFxLt0hY3nnU5YAqjowyAz9XPK1aAWMNg0ilmXTN30IjrSrnoJgvQmMiyE5gm4d50/Voa73CHm8zGOssffoGE35kC4Ws2o0vyA4oTBYSgzEpLh7Pxs01HpEHF2mqy/ldidlpWbg4U5uMgj3x3sznSPxEJGDODNw== test-dsa"

// newTestSSHKeyService builds an SSHKeyService directly (same package as
// the type under test), wiring the stubs above.
func newTestSSHKeyService(fdb *fakeSSHDB, q *stubSSHQuerier, coreQ *stubCoreQuerier, az coreAuthz.Authorizer) *SSHKeyService {
	return &SSHKeyService{
		db:    fdb,
		q:     q,
		coreQ: coreQ,
		az:    az,
		obs:   &observer.ObserverGroup{},
	}
}

// assertFieldError routes err through apiresp.WriteError (mirroring
// TestErrEmailTaken_ConflictDetail in user_accounts_anon_test.go) and
// asserts the resulting envelope's status and single field-level detail.
// apiresp exposes no public detail-carrying interface to recover Code from
// err directly, so this re-derives it the same way a real caller would.
func assertFieldError(t *testing.T, err error, wantStatus int, wantField, wantCode string) {
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
			Code    string               `json:"code"`
			Details []apiresp.FieldError `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON: %v, body=%s", err, rr.Body.String())
	}
	if len(body.Error.Details) != 1 {
		t.Fatalf("error.details: got %+v, want exactly one entry", body.Error.Details)
	}
	got := body.Error.Details[0]
	if got.Field != wantField || got.Code != wantCode {
		t.Errorf("error.details[0]: got {Field:%q Code:%q}, want {Field:%q Code:%q}", got.Field, got.Code, wantField, wantCode)
	}
}

// ---------------------------------------------------------------------------
// Authorize-first / ordering / propagation
// ---------------------------------------------------------------------------

func TestSSHKeyService_Register_AuthorizeCalledBeforeAnyKeyQuery_DenialPropagatesUnchanged(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	denyErr := errors.New("denied for test")

	q := newStubSSHQuerier(ua)
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{decision: denyErr}
	svc := newTestSSHKeyService(&fakeSSHDB{tx: &fakeSSHTx{}}, q, coreQ, az)

	_, err := svc.Register(context.Background(), accountUUID, genEd25519Line(t, ""), nil, false)

	if !az.called {
		t.Fatal("Authorize was never called")
	}
	if az.lastOp != "update" {
		t.Errorf("Authorize op: got %q, want %q", az.lastOp, "update")
	}
	if err != denyErr {
		t.Errorf("Register error: got %v, want the authorizer's error unchanged (%v)", err, denyErr)
	}
	if q.writeCalled {
		t.Error("InsertSSHPublicKey was called despite the authorization denial")
	}
}

func TestSSHKeyService_List_AuthorizeCalledBeforeAnyKeyQuery_DenialPropagatesUnchanged(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	denyErr := errors.New("denied for test")

	q := newStubSSHQuerier(ua)
	q.listErr = errors.New("list should never be reached")
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{decision: denyErr}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	_, _, err := svc.List(context.Background(), accountUUID, 0, 0)

	if !az.called {
		t.Fatal("Authorize was never called")
	}
	if az.lastOp != "read" {
		t.Errorf("Authorize op: got %q, want %q", az.lastOp, "read")
	}
	if err != denyErr {
		t.Errorf("List error: got %v, want the authorizer's error unchanged (%v)", err, denyErr)
	}
}

func TestSSHKeyService_Revoke_AuthorizeCalledBeforeAnyKeyQuery_DenialPropagatesUnchanged(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	keyUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	denyErr := errors.New("denied for test")

	q := newStubSSHQuerier(ua)
	q.activeErr = errors.New("GetActiveSSHPublicKeyByUUIDForUserAccount should never be reached")
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{decision: denyErr}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	err := svc.Revoke(context.Background(), accountUUID, keyUUID, false)

	if !az.called {
		t.Fatal("Authorize was never called")
	}
	if az.lastOp != "update" {
		t.Errorf("Authorize op: got %q, want %q", az.lastOp, "update")
	}
	if err != denyErr {
		t.Errorf("Revoke error: got %v, want the authorizer's error unchanged (%v)", err, denyErr)
	}
	if q.writeCalled {
		t.Error("ArchiveSSHPublicKey was called despite the authorization denial")
	}
}

// ---------------------------------------------------------------------------
// Masked-forbidden existence checks (D10)
// ---------------------------------------------------------------------------

func TestSSHKeyService_Register_MissingAccountReturnsMaskedForbidden(t *testing.T) {
	t.Parallel()

	q := newStubSSHQuerier() // no accounts seeded
	coreQ := &stubCoreQuerier{}
	az := &recordingAuthorizer{}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	_, err := svc.Register(context.Background(), uuid.New(), genEd25519Line(t, ""), nil, false)

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Register error: got %v, want apiresp.ErrForbidden", err)
	}
	if az.called {
		t.Error("Authorize was called for a missing account; must fail before Authorize")
	}
}

func TestSSHKeyService_Register_ArchivedHolderReturnsMaskedForbidden(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}

	q := newStubSSHQuerier(ua)
	coreQ := &stubCoreQuerier{entity: archivedEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	_, err := svc.Register(context.Background(), accountUUID, genEd25519Line(t, ""), nil, false)

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Register error: got %v, want apiresp.ErrForbidden", err)
	}
	if az.called {
		t.Error("Authorize was called for an archived account holder; must fail before Authorize")
	}
}

func TestSSHKeyService_Revoke_ZeroRowsAffectedReturnsMaskedForbidden(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	keyUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}

	q := newStubSSHQuerier(ua)
	q.activeKey = db.ModUsersSshPublicKey{Uuid: keyUUID, UserAccountID: ua.ID}
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	svc := newTestSSHKeyService(&fakeSSHDB{tx: &fakeSSHTx{archiveRows: 0}}, q, coreQ, az)

	err := svc.Revoke(context.Background(), accountUUID, keyUUID, false)

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Revoke error: got %v, want apiresp.ErrForbidden", err)
	}
}

func TestSSHKeyService_Revoke_UnknownKeyUUIDReturnsMaskedForbidden(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}

	q := newStubSSHQuerier(ua)
	q.activeErr = pgx.ErrNoRows
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	err := svc.Revoke(context.Background(), accountUUID, uuid.New(), false)

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Revoke error: got %v, want apiresp.ErrForbidden", err)
	}
}

// ---------------------------------------------------------------------------
// Register: parse/label sentinel -> detail code mapping
// ---------------------------------------------------------------------------

func TestSSHKeyService_Register_ParseAndLabelErrorsMapToDetailCodes(t *testing.T) {
	over100 := strings.Repeat("x", 101)

	tests := []struct {
		name      string
		line      string
		label     *string
		wantField string
		wantCode  string
	}{
		{name: "garbage input is invalid", line: "not an ssh key", wantField: "public_key", wantCode: "users.ssh_key_invalid"},
		{name: "unsupported key type (dsa)", line: dsaFixtureLine, wantField: "public_key", wantCode: "users.ssh_key_type_unsupported"},
		{name: "label too long", line: genEd25519Line(t, ""), label: &over100, wantField: "label", wantCode: "users.ssh_key_label_too_long"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			accountUUID := uuid.New()
			ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
			q := newStubSSHQuerier(ua)
			coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
			az := &recordingAuthorizer{}
			svc := newTestSSHKeyService(&fakeSSHDB{tx: &fakeSSHTx{}}, q, coreQ, az)

			_, err := svc.Register(context.Background(), accountUUID, tc.line, tc.label, false)

			assertFieldError(t, err, http.StatusBadRequest, tc.wantField, tc.wantCode)
		})
	}
}

// ---------------------------------------------------------------------------
// Register: unique-violation -> conflict mapping
// ---------------------------------------------------------------------------

func TestSSHKeyService_Register_UniqueViolationMapsToConflict(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	q := newStubSSHQuerier(ua)
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "ssh_public_keys_active_fingerprint_uq"}
	svc := newTestSSHKeyService(&fakeSSHDB{tx: &fakeSSHTx{insertErr: pgErr}}, q, coreQ, az)

	_, err := svc.Register(context.Background(), accountUUID, genEd25519Line(t, ""), nil, false)

	assertFieldError(t, err, http.StatusConflict, "public_key", "users.ssh_key_in_use")
}

// ---------------------------------------------------------------------------
// Register: happy path
// ---------------------------------------------------------------------------

func TestSSHKeyService_Register_Success(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	keyUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	q := newStubSSHQuerier(ua)
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	insertRow := db.ModUsersSshPublicKey{
		ID: 5, Uuid: keyUUID, UserAccountID: ua.ID,
		KeyType: ssh.KeyAlgoED25519, PublicKey: "canonical-key", FingerprintSha256: "SHA256:fp", Label: "my key",
	}
	svc := newTestSSHKeyService(&fakeSSHDB{tx: &fakeSSHTx{insertRow: insertRow}}, q, coreQ, az)

	out, err := svc.Register(context.Background(), accountUUID, genEd25519Line(t, ""), nil, true)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if out.UUID != keyUUID {
		t.Errorf("UUID: got %v, want %v", out.UUID, keyUUID)
	}
	if out.Fingerprint != "SHA256:fp" {
		t.Errorf("Fingerprint: got %q, want %q", out.Fingerprint, "SHA256:fp")
	}
	if !az.called || az.lastOp != "update" {
		t.Errorf("Authorize: called=%v op=%q, want called=true op=update", az.called, az.lastOp)
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestSSHKeyService_List_Success(t *testing.T) {
	t.Parallel()

	accountUUID := uuid.New()
	ua := db.UserAccount{ID: 1, Uuid: accountUUID, AccountHolder: 100}
	q := newStubSSHQuerier(ua)
	q.listRows = []db.ModUsersSshPublicKey{
		{ID: 1, Uuid: uuid.New(), KeyType: ssh.KeyAlgoED25519, FingerprintSha256: "SHA256:a"},
		{ID: 2, Uuid: uuid.New(), KeyType: ssh.KeyAlgoED25519, FingerprintSha256: "SHA256:b"},
	}
	q.countTotal = 2
	coreQ := &stubCoreQuerier{entity: activeEntity(ua.AccountHolder)}
	az := &recordingAuthorizer{}
	svc := newTestSSHKeyService(&fakeSSHDB{}, q, coreQ, az)

	keys, total, err := svc.List(context.Background(), accountUUID, 0, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if total != 2 {
		t.Errorf("total: got %d, want 2", total)
	}
	if len(keys) != 2 {
		t.Fatalf("keys: got %d, want 2", len(keys))
	}
}
