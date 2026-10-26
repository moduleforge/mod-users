package service

// Fakes shared by the unit tests of the identity services (users,
// identifiers, credentials, challenges, ssh resolver). Behavior against a real
// database is covered by the DB-backed suites.

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// fakeUsersQ implements the subset of usersdb.Querier the unit tests touch;
// any other method panics through the nil embedded interface.
type fakeUsersQ struct {
	usersdb.Querier

	getUser            func(int64) (usersdb.User, error)
	getPrimaryEmail    func(int64) (usersdb.UserIdentifier, error)
	createIdentifier   func(usersdb.CreateIdentifierParams) (usersdb.UserIdentifier, error)
	clearPrimary       int
	createCredential   func(usersdb.CreateCredentialParams) (usersdb.Credential, error)
	createOIDC         func(usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error)
	getCredentialByID  func(int64) (usersdb.Credential, error)
	getCredentialKind  func(string) (usersdb.CredentialKind, error)
	countInteractive   func(int64) (int64, error)
	revokeCredential   func(usersdb.RevokeCredentialParams) (usersdb.Credential, error)
	revokeSessions     func(int64) ([]usersdb.Session, error)
	getIdentifierByID  func(int64) (usersdb.UserIdentifier, error)
	markVerified       func(int64) (usersdb.UserIdentifier, error)
	getActiveEmailOTP  func(int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error)
	createEmailOTP     func(usersdb.CreateEmailOTPCredentialParams) (usersdb.CredEmailOtp, error)
	setIdentifierPrim  func(usersdb.SetIdentifierPrimaryParams) (usersdb.UserIdentifier, error)
	createUser         func(usersdb.CreateUserParams) (usersdb.User, error)
	setUserPerson      func(usersdb.SetUserPersonParams) (usersdb.User, error)
	createAnon         func(usersdb.CreateAnonCredentialParams) (usersdb.CredAnon, error)
	lockedUsers        []int64
	revokedCredentials []int64
}

func (f *fakeUsersQ) GetUserByEntityID(_ context.Context, id int64) (usersdb.User, error) {
	return f.getUser(id)
}

func (f *fakeUsersQ) GetUserByEntityIDForUpdate(_ context.Context, id int64) (usersdb.User, error) {
	f.lockedUsers = append(f.lockedUsers, id)
	if f.getUser == nil {
		return usersdb.User{EntityID: id}, nil
	}
	return f.getUser(id)
}

func (f *fakeUsersQ) GetPrimaryEmailByUser(_ context.Context, id int64) (usersdb.UserIdentifier, error) {
	return f.getPrimaryEmail(id)
}

func (f *fakeUsersQ) ClearPrimaryEmail(context.Context, int64) error {
	f.clearPrimary++
	return nil
}

func (f *fakeUsersQ) CreateIdentifier(_ context.Context, a usersdb.CreateIdentifierParams) (usersdb.UserIdentifier, error) {
	return f.createIdentifier(a)
}

func (f *fakeUsersQ) CreateCredential(_ context.Context, a usersdb.CreateCredentialParams) (usersdb.Credential, error) {
	return f.createCredential(a)
}

func (f *fakeUsersQ) CreateOIDCCredential(_ context.Context, a usersdb.CreateOIDCCredentialParams) (usersdb.CredOidc, error) {
	return f.createOIDC(a)
}

func (f *fakeUsersQ) GetCredentialByID(_ context.Context, id int64) (usersdb.Credential, error) {
	return f.getCredentialByID(id)
}

func (f *fakeUsersQ) GetCredentialKind(_ context.Context, kind string) (usersdb.CredentialKind, error) {
	return f.getCredentialKind(kind)
}

func (f *fakeUsersQ) CountActiveInteractiveCredentials(_ context.Context, id int64) (int64, error) {
	return f.countInteractive(id)
}

func (f *fakeUsersQ) RevokeCredential(_ context.Context, a usersdb.RevokeCredentialParams) (usersdb.Credential, error) {
	f.revokedCredentials = append(f.revokedCredentials, a.ID)
	return f.revokeCredential(a)
}

func (f *fakeUsersQ) RevokeSessionsHoldingCredential(_ context.Context, id int64) ([]usersdb.Session, error) {
	return f.revokeSessions(id)
}

// fakeCoreQ implements the subset of coredb.Querier the unit tests touch.
type fakeCoreQ struct {
	coredb.Querier

	getServiceAccount func(int64) (coredb.ServiceAccount, error)
	getEntity         func(int64) (coredb.GetEntityByIDRow, error)
	getPrincipal      func(int64) (coredb.Principal, error)
	getPersonal       func(int64) (coredb.Profile, error)
	listByHolder      func(int64) ([]coredb.Profile, error)
}

func (f *fakeCoreQ) GetServiceAccountByEntityID(_ context.Context, id int64) (coredb.ServiceAccount, error) {
	return f.getServiceAccount(id)
}

func (f *fakeCoreQ) GetEntityByID(_ context.Context, id int64) (coredb.GetEntityByIDRow, error) {
	return f.getEntity(id)
}

func (f *fakeCoreQ) GetPrincipalByEntityID(_ context.Context, id int64) (coredb.Principal, error) {
	return f.getPrincipal(id)
}

func (f *fakeCoreQ) GetPersonalProfileByHolder(_ context.Context, id int64) (coredb.Profile, error) {
	return f.getPersonal(id)
}

func (f *fakeCoreQ) ListProfilesByHolder(_ context.Context, id int64) ([]coredb.Profile, error) {
	if f.listByHolder == nil {
		return nil, nil
	}
	return f.listByHolder(id)
}

// observation is one recorded Observe call.
type observation struct {
	Op, Resource string
	Target       int64
	Actor        int64
}

// recordingObserver records in-transaction observations.
type recordingObserver struct {
	mu   sync.Mutex
	seen []observation
}

func (r *recordingObserver) Observe(ctx context.Context, _ pgx.Tx, op, resource string, target *int64, _, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o := observation{Op: op, Resource: resource}
	if target != nil {
		o.Target = *target
	}
	if profile, ok := opctx.ProfileID(ctx); ok {
		o.Actor = profile
	}
	r.seen = append(r.seen, o)
	return nil
}

func (r *recordingObserver) ObserveAfterCommit(context.Context, string, string, *int64, any) {}

func (f *fakeUsersQ) GetIdentifierByID(_ context.Context, id int64) (usersdb.UserIdentifier, error) {
	return f.getIdentifierByID(id)
}

func (f *fakeUsersQ) MarkIdentifierVerified(_ context.Context, id int64) (usersdb.UserIdentifier, error) {
	return f.markVerified(id)
}

func (f *fakeUsersQ) GetActiveEmailOTPByIdentifier(_ context.Context, id int64) (usersdb.GetActiveEmailOTPByIdentifierRow, error) {
	return f.getActiveEmailOTP(id)
}

func (f *fakeUsersQ) CreateEmailOTPCredential(_ context.Context, a usersdb.CreateEmailOTPCredentialParams) (usersdb.CredEmailOtp, error) {
	return f.createEmailOTP(a)
}

func (f *fakeUsersQ) SetIdentifierPrimary(_ context.Context, a usersdb.SetIdentifierPrimaryParams) (usersdb.UserIdentifier, error) {
	return f.setIdentifierPrim(a)
}

func (f *fakeUsersQ) CreateUser(_ context.Context, a usersdb.CreateUserParams) (usersdb.User, error) {
	return f.createUser(a)
}

func (f *fakeUsersQ) SetUserPerson(_ context.Context, a usersdb.SetUserPersonParams) (usersdb.User, error) {
	return f.setUserPerson(a)
}

func (f *fakeUsersQ) CreateAnonCredential(_ context.Context, a usersdb.CreateAnonCredentialParams) (usersdb.CredAnon, error) {
	return f.createAnon(a)
}
