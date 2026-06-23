package service

// Tests for the UpdateUserAccount upgrade-flow logic:
//   - TestUpdateUserAccount_UpgradeDeletesAnonTokens: patching email from nil →
//     non-nil causes DeleteAnonTokensByUserAccountID to be called.
//   - TestUpdateUserAccount_NamedEmailDoesNotDeleteTokens: patching email on a
//     named account does NOT call DeleteAnonTokensByUserAccountID.
//
// These tests use a minimal fake pgx.Tx that records Exec calls so we can
// assert on which SQL queries were issued inside the transaction.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coreAuthz "github.com/moduleforge/core-api/authz"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/observer"
	db "github.com/moduleforge/users-module/model/db"
)

// ---------------------------------------------------------------------------
// ptr helper
// ---------------------------------------------------------------------------

// ptr returns a pointer to the given string. Useful for constructing
// UpdateUserAccountInput fields in tests.
func ptr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// allowAllAuthorizer
// ---------------------------------------------------------------------------

// allowAllAuthorizer always permits the operation. Used in service tests to
// bypass authorization logic and focus on business logic.
type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(_ context.Context, _ string, _ *int64) error {
	return nil
}

var _ coreAuthz.Authorizer = allowAllAuthorizer{}

// ---------------------------------------------------------------------------
// stubUAQuerier
// ---------------------------------------------------------------------------

// stubUAQuerier implements db.Querier for the service methods under test.
// Only the methods called by Update (GetUserAccountByUUID) need implementations;
// all others panic to detect unexpected calls.
type stubUAQuerier struct {
	accounts map[uuid.UUID]db.UserAccount
}

func newStubUAQuerier(accounts ...db.UserAccount) *stubUAQuerier {
	m := make(map[uuid.UUID]db.UserAccount, len(accounts))
	for _, ua := range accounts {
		m[ua.Uuid] = ua
	}
	return &stubUAQuerier{accounts: m}
}

func (s *stubUAQuerier) GetUserAccountByUUID(_ context.Context, id uuid.UUID) (db.UserAccount, error) {
	if ua, ok := s.accounts[id]; ok {
		return ua, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

// All other Querier methods are not called by Update; these stubs satisfy the
// interface but return errors if somehow invoked.
func (s *stubUAQuerier) GetUserAccountByID(_ context.Context, _ int64) (db.UserAccount, error) {
	return db.UserAccount{}, nil
}
func (s *stubUAQuerier) GetUserAccountByEmail(_ context.Context, _ string) (db.UserAccount, error) {
	return db.UserAccount{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) GetUserAccountByAccountHolder(_ context.Context, _ int64) (db.UserAccount, error) {
	return db.UserAccount{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) SearchUserAccounts(_ context.Context, _ db.SearchUserAccountsParams) ([]db.UserAccount, error) {
	return nil, nil
}
func (s *stubUAQuerier) CreateUserAccount(_ context.Context, _ db.CreateUserAccountParams) (db.UserAccount, error) {
	return db.UserAccount{}, nil
}
func (s *stubUAQuerier) UpdateUserAccount(_ context.Context, _ db.UpdateUserAccountParams) error {
	return nil
}
func (s *stubUAQuerier) GetAuthLocal(_ context.Context, _ int64) (db.AuthLocal, error) {
	return db.AuthLocal{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) UpsertAuthLocal(_ context.Context, _ db.UpsertAuthLocalParams) error {
	return nil
}
func (s *stubUAQuerier) DeleteAuthLocal(_ context.Context, _ int64) error { return nil }
func (s *stubUAQuerier) CreateEmailCode(_ context.Context, _ db.CreateEmailCodeParams) (db.EmailCode, error) {
	return db.EmailCode{}, nil
}
func (s *stubUAQuerier) GetActiveEmailCode(_ context.Context, _ db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	return db.EmailCode{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) ConsumeEmailCode(_ context.Context, _ int64) error { return nil }
func (s *stubUAQuerier) CreatePasswordReset(_ context.Context, _ db.CreatePasswordResetParams) (db.PasswordReset, error) {
	return db.PasswordReset{}, nil
}
func (s *stubUAQuerier) GetActivePasswordReset(_ context.Context, _ string) (db.PasswordReset, error) {
	return db.PasswordReset{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) ConsumePasswordReset(_ context.Context, _ int64) error { return nil }
func (s *stubUAQuerier) CreateAnonToken(_ context.Context, _ db.CreateAnonTokenParams) (db.AnonToken, error) {
	return db.AnonToken{}, nil
}
func (s *stubUAQuerier) GetAnonTokenBySessionToken(_ context.Context, _ string) (db.AnonToken, error) {
	return db.AnonToken{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) GetAnonTokensByDeviceID(_ context.Context, _ string) ([]db.AnonToken, error) {
	return nil, nil
}
func (s *stubUAQuerier) DeleteAnonTokensByUserAccountID(_ context.Context, _ int64) error {
	return nil
}
func (s *stubUAQuerier) CountOIDCIdentitiesByUserAccount(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (s *stubUAQuerier) ListOIDCIdentitiesByUserAccount(_ context.Context, _ int64) ([]db.AuthOidcIdentity, error) {
	return nil, nil
}
func (s *stubUAQuerier) GetOIDCIdentityByIssuerSubject(_ context.Context, _ db.GetOIDCIdentityByIssuerSubjectParams) (db.AuthOidcIdentity, error) {
	return db.AuthOidcIdentity{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) InsertOIDCIdentity(_ context.Context, _ db.InsertOIDCIdentityParams) (db.AuthOidcIdentity, error) {
	return db.AuthOidcIdentity{}, nil
}
func (s *stubUAQuerier) TouchOIDCIdentityLastSeen(_ context.Context, _ int64) error { return nil }
func (s *stubUAQuerier) DeleteOIDCIdentityByUUID(_ context.Context, _ db.DeleteOIDCIdentityByUUIDParams) (int64, error) {
	return 0, nil
}
func (s *stubUAQuerier) GetOIDCConfig(_ context.Context) (db.OidcConfig, error) {
	return db.OidcConfig{}, nil
}
func (s *stubUAQuerier) UpdateOIDCConfig(_ context.Context, _ bool) error { return nil }
func (s *stubUAQuerier) GetOIDCProvider(_ context.Context, _ string) (db.OidcProvider, error) {
	return db.OidcProvider{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) ListOIDCProviders(_ context.Context) ([]db.OidcProvider, error) {
	return nil, nil
}
func (s *stubUAQuerier) UpsertOIDCProvider(_ context.Context, _ db.UpsertOIDCProviderParams) (db.OidcProvider, error) {
	return db.OidcProvider{}, nil
}
func (s *stubUAQuerier) DeleteOIDCProvider(_ context.Context, _ string) (int64, error) { return 0, nil }
func (s *stubUAQuerier) SetOIDCProviderEnabled(_ context.Context, _ db.SetOIDCProviderEnabledParams) error {
	return nil
}
func (s *stubUAQuerier) GetSetupTokenHash(_ context.Context) (pgtype.Text, error) {
	return pgtype.Text{}, nil
}
func (s *stubUAQuerier) SetSetupTokenHash(_ context.Context, _ pgtype.Text) error { return nil }
func (s *stubUAQuerier) ClearSetupTokenHash(_ context.Context) error               { return nil }
func (s *stubUAQuerier) CreateApp(_ context.Context, _ db.CreateAppParams) (db.App, error) {
	return db.App{}, nil
}
func (s *stubUAQuerier) GetAppByUUID(_ context.Context, _ uuid.UUID) (db.App, error) {
	return db.App{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) GetAppBySlug(_ context.Context, _ string) (db.App, error) {
	return db.App{}, pgx.ErrNoRows
}
func (s *stubUAQuerier) ListApps(_ context.Context) ([]db.App, error) { return nil, nil }
func (s *stubUAQuerier) UpdateApp(_ context.Context, _ db.UpdateAppParams) error { return nil }
func (s *stubUAQuerier) ArchiveApp(_ context.Context, _ int64) error              { return nil }
func (s *stubUAQuerier) AssignUserAccountToApp(_ context.Context, _ db.AssignUserAccountToAppParams) error {
	return nil
}
func (s *stubUAQuerier) RemoveUserAccountFromApp(_ context.Context, _ db.RemoveUserAccountFromAppParams) error {
	return nil
}
func (s *stubUAQuerier) ListAppUserAccounts(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	return nil, nil
}
func (s *stubUAQuerier) ListUserAccountApps(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	return nil, nil
}
func (s *stubUAQuerier) SetAppUserAccountRoles(_ context.Context, _ db.SetAppUserAccountRolesParams) error {
	return nil
}
func (s *stubUAQuerier) SetDefaultApp(_ context.Context, _ db.SetDefaultAppParams) error {
	return nil
}

var _ db.Querier = (*stubUAQuerier)(nil)

// ---------------------------------------------------------------------------
// fakeTx — records Exec calls for SQL assertion
// ---------------------------------------------------------------------------

// fakeTx implements pgx.Tx. Exec calls are recorded; QueryRow returns a
// pre-configured fakeRow. Commit and Rollback are no-ops. All other methods
// are no-ops that return zero values.
type fakeTx struct {
	mu      sync.Mutex
	execs   []string  // SQL strings passed to Exec
	queryUA db.UserAccount // returned by QueryRow (GetUserAccountByID)
}

func (f *fakeTx) execWasCalled(sqlSubstr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.execs {
		if strings.Contains(s, sqlSubstr) {
			return true
		}
	}
	return false
}

// Exec records the SQL statement.
func (f *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, sql)
	return pgconn.NewCommandTag("OK"), nil
}

// QueryRow returns a fakeRow that Scans the pre-configured UserAccount.
func (f *fakeTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return &fakeUARow{ua: f.queryUA}
}

// Query returns an empty rows iterator.
func (f *fakeTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &emptyRows{}, nil
}

func (f *fakeTx) Begin(_ context.Context) (pgx.Tx, error)   { return f, nil }
func (f *fakeTx) Commit(_ context.Context) error             { return nil }
func (f *fakeTx) Rollback(_ context.Context) error           { return nil }
func (f *fakeTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeTx) LargeObjects() pgx.LargeObjects                              { return pgx.LargeObjects{} }
func (f *fakeTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeTx) Conn() *pgx.Conn { return nil }

// fakeUARow implements pgx.Row for UserAccount scans.
type fakeUARow struct {
	ua db.UserAccount
}

func (r *fakeUARow) Scan(dest ...any) error {
	if len(dest) < 8 {
		return nil
	}
	// Match the scan order in GetUserAccountByID:
	// id, uuid, account_holder, email, email_verified_at, default_app_id, created_at, updated_at
	if d, ok := dest[0].(*int64); ok {
		*d = r.ua.ID
	}
	if d, ok := dest[1].(*uuid.UUID); ok {
		*d = r.ua.Uuid
	}
	if d, ok := dest[2].(*int64); ok {
		*d = r.ua.AccountHolder
	}
	if d, ok := dest[3].(*pgtype.Text); ok {
		*d = r.ua.Email
	}
	if d, ok := dest[4].(**time.Time); ok {
		*d = r.ua.EmailVerifiedAt
	}
	if d, ok := dest[5].(*pgtype.Int8); ok {
		*d = r.ua.DefaultAppID
	}
	if d, ok := dest[6].(*pgtype.Timestamptz); ok {
		*d = r.ua.CreatedAt
	}
	if d, ok := dest[7].(*pgtype.Timestamptz); ok {
		*d = r.ua.UpdatedAt
	}
	return nil
}

// emptyRows is a pgx.Rows that yields no rows.
type emptyRows struct{}

func (e *emptyRows) Close()                                          {}
func (e *emptyRows) Err() error                                      { return nil }
func (e *emptyRows) CommandTag() pgconn.CommandTag                   { return pgconn.CommandTag{} }
func (e *emptyRows) FieldDescriptions() []pgconn.FieldDescription    { return nil }
func (e *emptyRows) Next() bool                                      { return false }
func (e *emptyRows) Scan(_ ...any) error                             { return nil }
func (e *emptyRows) Values() ([]any, error)                          { return nil, nil }
func (e *emptyRows) RawValues() [][]byte                             { return nil }
func (e *emptyRows) Conn() *pgx.Conn                                 { return nil }

// ---------------------------------------------------------------------------
// fakeDB — implements txhelper.DB
// ---------------------------------------------------------------------------

// fakeDB implements txhelper.DB; BeginTx returns the configured fakeTx.
type fakeDB struct {
	tx *fakeTx
}

func (f *fakeDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.tx, nil
}

// ---------------------------------------------------------------------------
// Build a minimal UserAccountService for Update tests
// ---------------------------------------------------------------------------

func newUpgradeTestService(q db.Querier, ftx *fakeTx) *UserAccountService {
	return &UserAccountService{
		db:    &fakeDB{tx: ftx},
		q:     q,
		coreQ: nil, // Update only touches coreQ if GivenName/FamilyName provided
		az:    allowAllAuthorizer{},
		obs:   &observer.ObserverGroup{},
		// npService unused by Update
	}
}

// ---------------------------------------------------------------------------
// TestUpdateUserAccount_UpgradeDeletesAnonTokens
// ---------------------------------------------------------------------------

// TestUpdateUserAccount_UpgradeDeletesAnonTokens verifies that when Update
// patches email from NULL (anonymous) to a real email address, the service
// calls DeleteAnonTokensByUserAccountID inside the transaction.
func TestUpdateUserAccount_UpgradeDeletesAnonTokens(t *testing.T) {
	t.Parallel()

	anonUUID := uuid.New()
	anonUA := db.UserAccount{
		ID:            10,
		Uuid:          anonUUID,
		AccountHolder: 100,
		Email:         pgtype.Text{Valid: false}, // anonymous
	}

	q := newStubUAQuerier(anonUA)
	ftx := &fakeTx{queryUA: db.UserAccount{
		ID:    anonUA.ID,
		Uuid:  anonUA.Uuid,
		Email: pgtype.Text{String: "user@example.com", Valid: true},
	}}
	svc := newUpgradeTestService(q, ftx)

	_, err := svc.Update(context.Background(), anonUUID, UpdateUserAccountInput{
		Email: ptr("user@example.com"),
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Assert that the DELETE FROM anon_tokens SQL was executed in the tx.
	if !ftx.execWasCalled("DELETE FROM anon_tokens") {
		t.Error("DeleteAnonTokensByUserAccountID was not called; expected cascade delete on upgrade")
	}
}

// ---------------------------------------------------------------------------
// TestUpdateUserAccount_NamedEmailDoesNotDeleteTokens
// ---------------------------------------------------------------------------

// TestUpdateUserAccount_NamedEmailDoesNotDeleteTokens verifies that when
// Update patches email on a named account (email already non-null), the
// service does NOT call DeleteAnonTokensByUserAccountID.
func TestUpdateUserAccount_NamedEmailDoesNotDeleteTokens(t *testing.T) {
	t.Parallel()

	namedUUID := uuid.New()
	namedUA := db.UserAccount{
		ID:            20,
		Uuid:          namedUUID,
		AccountHolder: 200,
		Email:         pgtype.Text{String: "old@example.com", Valid: true}, // named
	}

	q := newStubUAQuerier(namedUA)
	ftx := &fakeTx{queryUA: db.UserAccount{
		ID:    namedUA.ID,
		Uuid:  namedUA.Uuid,
		Email: pgtype.Text{String: "new@example.com", Valid: true},
	}}
	svc := newUpgradeTestService(q, ftx)

	_, err := svc.Update(context.Background(), namedUUID, UpdateUserAccountInput{
		Email: ptr("new@example.com"),
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Assert that no DELETE FROM anon_tokens was executed.
	if ftx.execWasCalled("DELETE FROM anon_tokens") {
		t.Error("DeleteAnonTokensByUserAccountID was called on a named account; should not have been")
	}
}

// Ensure stubUAQuerier satisfies coreservice.ErrNotFound for test completeness.
var _ = coreservice.ErrNotFound
