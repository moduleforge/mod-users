package handlers

import (
	"fmt"
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	db "github.com/moduleforge/mod-users/model/db"
)

// SelfHandler serves /v1/self. /self is a composite identity endpoint:
// core-module owns the entity data (given_name, family_name, etc.) via
// EntityService.GetSelf, while users-module owns the users-row data
// (email, timestamps, uuid). This handler stitches the two.
type SelfHandler struct {
	q        *db.Queries
	coreQ    *coredb.Queries
	coreSvcs *coreservice.Services
}

// NewSelfHandler constructs the /self handler with its dependencies.
func NewSelfHandler(q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services) *SelfHandler {
	return &SelfHandler{q: q, coreQ: coreQ, coreSvcs: coreSvcs}
}

// Get returns the caller's full profile: user account row fields + entity/subtype.
func (h *SelfHandler) Get(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	ua, err := h.q.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("self.Get: %w", err))
		return
	}

	profile, err := h.coreSvcs.Entity.GetSelf(r.Context(), h.coreQ)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("self.Get: %w", err))
		return
	}

	server.JSON(w, http.StatusOK, buildSelfResponse(ua, profile))
}

// selfUpdateRequest is the body for PUT /v1/self.
type selfUpdateRequest struct {
	GivenName  *string `json:"given_name"`
	FamilyName *string `json:"family_name"`
}

// Put updates the caller's mutable profile fields (currently only
// natural_person given_name/family_name). Returns the composed profile.
func (h *SelfHandler) Put(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	var req selfUpdateRequest
	if err := server.Decode(r, &req); err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

	ua, err := h.q.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("self.Put: %w", err))
		return
	}

	if req.GivenName != nil || req.FamilyName != nil {
		// account_holder = entity_id on the legal_entities/natural_persons chain.
		entity, err := h.coreQ.GetEntityByID(r.Context(), ua.AccountHolder)
		if err != nil {
			apiresp.WriteError(w, r, fmt.Errorf("self.Put: %w", err))
			return
		}
		err = h.coreSvcs.NaturalPerson.UpdateByEntityUUID(
			r.Context(),
			h.coreQ,
			entity.Uuid,
			coreservice.UpdateNaturalPersonInput{GivenName: req.GivenName, FamilyName: req.FamilyName},
		)
		if err != nil {
			writeCoreServiceErr(w, r, err)
			return
		}
	}

	// Re-fetch the now-updated profile.
	profile, err := h.coreSvcs.Entity.GetSelf(r.Context(), h.coreQ)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("self.Put: %w", err))
		return
	}

	server.JSON(w, http.StatusOK, buildSelfResponse(ua, profile))
}

// buildSelfResponse composes the flat response shape the frontend
// (UserSelf interface) expects.
func buildSelfResponse(ua db.UserAccount, profile coreservice.Profile) map[string]any {
	var emailVal any
	if ua.Email.Valid {
		emailVal = ua.Email.String
	}
	resp := map[string]any{
		"uuid":        ua.Uuid.String(),
		"entity_uuid": profile.Entity.Uuid.String(),
		"email":       emailVal,
		"created_at":  ua.CreatedAt.Time,
		"updated_at":  ua.UpdatedAt.Time,
	}

	switch profile.Kind {
	case "natural_person":
		if np := profile.NaturalPerson; np != nil {
			resp["given_name"] = np.GivenName.String
			resp["family_name"] = np.FamilyName.String
		}
	case "corporation":
		if corp := profile.Corporation; corp != nil {
			resp["legal_name"] = corp.LegalName
		}
	case "service_account":
		if sa := profile.ServiceAccount; sa != nil {
			resp["label"] = sa.Label
		}
	}

	return resp
}

// writeCoreServiceErr maps core service sentinels to HTTP responses by
// delegating to apiresp.WriteError. coreservice.ErrNotFound/ErrForbidden/
// ErrInvalidInput are already aliases of apiresp's canonical sentinels (see
// mod-core/api/service/errors.go), so apiresp.WriteError classifies them
// correctly with no local switch.
func writeCoreServiceErr(w http.ResponseWriter, r *http.Request, err error) {
	apiresp.WriteError(w, r, err)
}
