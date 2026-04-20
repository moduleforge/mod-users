package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	corehttpapi "github.com/moduleforge/core-api/httpapi"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/users-module/api/internal/auth"
	"io"
	"log/slog"
)

// --- fakes reused from core-module's test helpers (duplicated here to avoid
// cross-package test dependency) ---

type fakeAuditWriter struct{}

func (f *fakeAuditWriter) Write(_ context.Context, _, _ string, _ *int64, _, _ any) error {
	return nil
}

type fakePrincipalExtractor struct {
	p  *coreservice.Principal
	ok bool
}

func (f *fakePrincipalExtractor) FromContext(_ context.Context) (*coreservice.Principal, bool) {
	return f.p, f.ok
}

type spyNaturalPersonService struct {
	// updateCalled records the entity UUID passed to UpdateByEntityUUID.
	updateCalled []uuid.UUID
	// getProfile is returned by all get operations.
	getProfile coreservice.Profile
}

func (s *spyNaturalPersonService) Create(_ context.Context, _ coredb.Querier, _ coreservice.Principal, _ coreservice.CreateNaturalPersonInput) (coredb.NaturalPerson, uuid.UUID, error) {
	return coredb.NaturalPerson{}, uuid.UUID{}, nil
}

func (s *spyNaturalPersonService) GetByEntityUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID) (coreservice.Profile, error) {
	return s.getProfile, nil
}

func (s *spyNaturalPersonService) UpdateByEntityUUID(_ context.Context, _ coredb.Querier, entityUUID uuid.UUID, _ coreservice.UpdateNaturalPersonInput, _ coreservice.Principal) error {
	s.updateCalled = append(s.updateCalled, entityUUID)
	return nil
}

var _ coreservice.NaturalPersonServicer = (*spyNaturalPersonService)(nil)

type fakeEntityService struct {
	profile coreservice.Profile
}

func (f *fakeEntityService) GetByUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID) (coredb.Entity, error) {
	return f.profile.Entity, nil
}

func (f *fakeEntityService) GetByID(_ context.Context, _ coredb.Querier, _ int64) (coredb.Entity, error) {
	return f.profile.Entity, nil
}

func (f *fakeEntityService) GetSelf(_ context.Context, _ coredb.Querier, _ coreservice.Principal) (coreservice.Profile, error) {
	return f.profile, nil
}

func (f *fakeEntityService) Archive(_ context.Context, _ coredb.Querier, _ uuid.UUID, _ coreservice.Principal) error {
	return nil
}

func (f *fakeEntityService) ResolveProfile(_ context.Context, _ coredb.Querier, _ uuid.UUID) (coreservice.Profile, error) {
	return f.profile, nil
}

var _ coreservice.EntityServicer = (*fakeEntityService)(nil)

type fakeLegalEntityService struct{}

func (f *fakeLegalEntityService) GetByEntityID(_ context.Context, _ coredb.Querier, _ int64) (coredb.LegalEntity, error) {
	return coredb.LegalEntity{}, nil
}

func (f *fakeLegalEntityService) Create(_ context.Context, _ coredb.Querier, _ coredb.CreateLegalEntityParams) (coredb.LegalEntity, error) {
	return coredb.LegalEntity{}, nil
}

var _ coreservice.LegalEntityServicer = (*fakeLegalEntityService)(nil)

type fakeCorporationService struct{}

func (f *fakeCorporationService) Create(_ context.Context, _ coredb.Querier, _ coreservice.Principal, _ coreservice.CreateCorporationInput) (coredb.Corporation, uuid.UUID, error) {
	return coredb.Corporation{}, uuid.UUID{}, nil
}

func (f *fakeCorporationService) GetByEntityUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID) (coreservice.Profile, error) {
	return coreservice.Profile{}, nil
}

func (f *fakeCorporationService) UpdateByEntityUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID, _ coreservice.UpdateCorporationInput, _ coreservice.Principal) error {
	return nil
}

var _ coreservice.CorporationServicer = (*fakeCorporationService)(nil)

type fakeServiceAccountService struct{}

func (f *fakeServiceAccountService) Create(_ context.Context, _ coredb.Querier, _ coreservice.Principal, _ coreservice.CreateServiceAccountInput) (coredb.ServiceAccount, uuid.UUID, error) {
	return coredb.ServiceAccount{}, uuid.UUID{}, nil
}

func (f *fakeServiceAccountService) GetByEntityUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID) (coreservice.Profile, error) {
	return coreservice.Profile{}, nil
}

func (f *fakeServiceAccountService) UpdateByEntityUUID(_ context.Context, _ coredb.Querier, _ uuid.UUID, _ coreservice.UpdateServiceAccountInput, _ coreservice.Principal) error {
	return nil
}

var _ coreservice.ServiceAccountServicer = (*fakeServiceAccountService)(nil)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// TestPutSelf_MountedInUsersModule verifies that:
//  1. The core router mounted at /v1 in users-module handles PUT /v1/self.
//  2. The CorePrincipalAdapter correctly lifts the users-module UserContext into
//     a core service.Principal that the handler can act on.
//  3. The NaturalPerson.UpdateByEntityUUID service method is called exactly once
//     with the caller's entity UUID.
func TestPutSelf_MountedInUsersModule(t *testing.T) {
	entityUUID := uuid.New()
	testPrincipal := &coreservice.Principal{UserID: 42, EntityID: 7, IsAdmin: false}
	testProfile := coreservice.Profile{
		Kind: "natural_person",
		Entity: coredb.Entity{
			ID:   7,
			Uuid: entityUUID,
		},
		NaturalPerson: &coredb.NaturalPerson{
			GivenName:  pgtype.Text{String: "Alice", Valid: true},
			FamilyName: pgtype.Text{String: "Smith", Valid: true},
		},
	}

	spyNP := &spyNaturalPersonService{getProfile: testProfile}
	entSvc := &fakeEntityService{profile: testProfile}

	// Build a fake principal extractor that delegates to CorePrincipalAdapter —
	// we inject a UserContext matching testPrincipal into the request context.
	svcs := &coreservice.Services{}
	svcs.Entity = entSvc
	svcs.NaturalPerson = spyNP
	svcs.LegalEntity = &fakeLegalEntityService{}
	svcs.Corporation = &fakeCorporationService{}
	svcs.ServiceAccount = &fakeServiceAccountService{}

	// Use CorePrincipalAdapter as the PrincipalExtractor so the test proves
	// the adapter correctly translates UserContext → Principal.
	adapter := auth.CorePrincipalAdapter{}

	coreRouter := corehttpapi.NewRouter(corehttpapi.Deps{
		Services:  svcs,
		Audit:     &fakeAuditWriter{},
		Principal: adapter,
		Logger:    noopLogger(),
	})

	// Mount core router inside a /v1 group, mirroring main.go.
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Mount("/", coreRouter)
	})

	body := bytes.NewBufferString(`{"given_name":"Updated"}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/self", body)
	req.Header.Set("Content-Type", "application/json")

	// Inject UserContext matching testPrincipal so CorePrincipalAdapter can
	// translate it into a coreservice.Principal.
	uc := &auth.UserContext{
		UserID:   testPrincipal.UserID,
		EntityID: testPrincipal.EntityID,
		IsAdmin:  testPrincipal.IsAdmin,
		Email:    "alice@example.com",
	}
	req = req.WithContext(auth.WithUserContext(req.Context(), uc))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Verify UpdateByEntityUUID was called with the correct entity UUID.
	if len(spyNP.updateCalled) != 1 {
		t.Fatalf("UpdateByEntityUUID call count: got %d, want 1", len(spyNP.updateCalled))
	}
	if spyNP.updateCalled[0] != entityUUID {
		t.Errorf("UpdateByEntityUUID entity UUID: got %s, want %s",
			spyNP.updateCalled[0], entityUUID)
	}

	// Verify the response body contains the expected profile fields.
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["kind"] != "natural_person" {
		t.Errorf("response kind: got %v, want natural_person", resp["kind"])
	}
}
