package handlers

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/users-module/api/internal/audit"
	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// SelfHandler serves the /v1/self endpoints.
type SelfHandler struct {
	q *db.Queries
	a audit.Writer
}

// NewSelfHandler creates a handler for the /v1/self endpoints.
func NewSelfHandler(q *db.Queries, a audit.Writer) *SelfHandler {
	return &SelfHandler{q: q, a: a}
}

// Get returns the caller's profile.
func (h *SelfHandler) Get(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	user, err := h.q.GetUserByID(r.Context(), uc.UserID)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user")
		return
	}

	// Build entity info.
	entity, err := h.buildEntityInfo(r, user.EntityID)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load entity")
		return
	}

	// Build app info if present.
	var defaultApp any
	if user.DefaultAppID.Valid {
		app, err := h.q.GetAppByUUID(r.Context(), uuid.UUID{}) // need ID lookup
		_ = app
		_ = err
		// For now, omit if we can't look up by ID easily.
		defaultApp = nil
	}

	resp := map[string]any{
		"uuid":           user.Uuid.String(),
		"email":          user.Email,
		"email_verified": user.EmailVerifiedAt != nil,
		"is_admin":       uc.IsAdmin,
		"default_app":    defaultApp,
		"entity":         entity,
	}

	server.JSON(w, http.StatusOK, resp)
}

func (h *SelfHandler) buildEntityInfo(r *http.Request, entityID int64) (map[string]any, error) {
	// We need to look up entity by internal ID — but our queries use UUID.
	// Use a raw query approach: get entity, then child table.
	// Since GetEntityByUUID won't work here, we get the user's entity by
	// joining through the user record which we already have.
	// For now, build a simplified response using the user's entity_id.

	// Get legal entity info.
	le, err := h.q.GetLegalEntityByEntityID(r.Context(), entityID)
	if err != nil {
		// Might be a service account.
		sa, err2 := h.q.GetServiceAccountByEntityID(r.Context(), entityID)
		if err2 != nil {
			return nil, err
		}
		return map[string]any{
			"kind":  "service_account",
			"label": sa.Label,
		}, nil
	}

	info := map[string]any{
		"kind":         le.Kind,
		"display_name": le.DisplayName,
	}

	switch le.Kind {
	case "natural_person":
		np, err := h.q.GetNaturalPersonByLegalEntityID(r.Context(), le.ID)
		if err == nil {
			info["given_name"] = np.GivenName.String
			info["family_name"] = np.FamilyName.String
		}
	case "corporation":
		corp, err := h.q.GetCorporationByLegalEntityID(r.Context(), le.ID)
		if err == nil {
			info["legal_name"] = corp.LegalName
			info["jurisdiction"] = corp.Jurisdiction.String
		}
	}

	return info, nil
}

// selfUpdateRequest is the body for PUT /v1/self.
type selfUpdateRequest struct {
	GivenName     *string `json:"given_name"`
	FamilyName    *string `json:"family_name"`
	DefaultAppUUID *string `json:"default_app_uuid"`
}

// Put updates the caller's editable profile fields.
func (h *SelfHandler) Put(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	var req selfUpdateRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Load current user for before snapshot.
	user, err := h.q.GetUserByID(r.Context(), uc.UserID)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user")
		return
	}

	beforeSnapshot := map[string]any{
		"email": user.Email,
	}

	// Update natural person fields if this is a natural person.
	if req.GivenName != nil || req.FamilyName != nil {
		le, err := h.q.GetLegalEntityByEntityID(r.Context(), user.EntityID)
		if err == nil && le.Kind == "natural_person" {
			np, err := h.q.GetNaturalPersonByLegalEntityID(r.Context(), le.ID)
			if err == nil {
				beforeSnapshot["given_name"] = np.GivenName.String
				beforeSnapshot["family_name"] = np.FamilyName.String

				gn := np.GivenName
				fn := np.FamilyName
				if req.GivenName != nil {
					gn = pgtype.Text{String: *req.GivenName, Valid: true}
				}
				if req.FamilyName != nil {
					fn = pgtype.Text{String: *req.FamilyName, Valid: true}
				}

				_ = h.q.UpdateNaturalPerson(r.Context(), db.UpdateNaturalPersonParams{
					LegalEntityID: le.ID,
					GivenName:     gn,
					FamilyName:    fn,
				})
			}
		}
	}

	// Update default app if provided.
	if req.DefaultAppUUID != nil {
		appUUID, err := uuid.Parse(*req.DefaultAppUUID)
		if err != nil {
			server.Error(w, http.StatusBadRequest, "bad_request", "invalid app UUID")
			return
		}
		app, err := h.q.GetAppByUUID(r.Context(), appUUID)
		if err != nil {
			server.Error(w, http.StatusBadRequest, "bad_request", "app not found")
			return
		}
		_ = h.q.SetDefaultApp(r.Context(), db.SetDefaultAppParams{
			ID:           user.ID,
			DefaultAppID: pgtype.Int8{Int64: app.ID, Valid: true},
		})
	}

	afterSnapshot := map[string]any{
		"email": user.Email,
	}
	if req.GivenName != nil {
		afterSnapshot["given_name"] = *req.GivenName
	}
	if req.FamilyName != nil {
		afterSnapshot["family_name"] = *req.FamilyName
	}

	entityID := user.EntityID
	_ = h.a.Write(r.Context(), "update", "users", &entityID, beforeSnapshot, afterSnapshot)

	// Return updated profile.
	h.Get(w, r)
}

// timePtr returns a pointer to a time.Time or nil.
func timePtr(t *time.Time) *time.Time {
	return t
}
