package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/txhelper"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/server"
	db "github.com/moduleforge/mod-users/model/db"
)

// AppsHandler serves the apps/user-accounts membership endpoints under
// /v1/apps/{uuid}/user-accounts. Top-level /apps CRUD (Create/List/GetApp/
// UpdateApp/DeleteApp) has moved to mod-core (apps are now entities there);
// this handler retains only the membership operations, which continue to
// operate on mod-users' own apps_user_accounts join table.
type AppsHandler struct {
	pool           txhelper.DB
	q              db.Querier
	az             coreAuthz.Authorizer
	observers      *observer.ObserverGroup
	entityResolver *entity.Resolver
	coreQ          *coredb.Queries
}

// NewAppsHandler creates an AppsHandler. entityResolver resolves the
// {uuid} path param to the app's internal id (apps.id == entities.id,
// per mod-core's FK-anchor entity-subtype pattern) since mod-users no
// longer has its own apps table/query to look the row up directly.
//
// entityResolver.AllowNotFound("app") is set here so an unknown app uuid
// continues to surface as ErrNotFound (the resolver's default policy masks
// a missing entity as ErrForbidden), preserving this handler's original
// not-found behavior.
func NewAppsHandler(
	pool txhelper.DB,
	q *db.Queries,
	az coreAuthz.Authorizer,
	observers *observer.ObserverGroup,
	entityResolver *entity.Resolver,
	coreQ *coredb.Queries,
) *AppsHandler {
	entityResolver.AllowNotFound("app")
	return &AppsHandler{
		pool:           pool,
		q:              q,
		az:             az,
		observers:      observers,
		entityResolver: entityResolver,
		coreQ:          coreQ,
	}
}

// --- apps_users endpoints ---
// These endpoints are admin-only management operations. They do not emit
// audit events (no equivalent in the original audit gap report) but they
// do require authorization.

type assignUserRequest struct {
	UserUUID string   `json:"user_uuid"`
	Roles    []string `json:"roles"`
}

// AssignUser handles POST /v1/apps/{uuid}/user-accounts (admin).
func (h *AppsHandler) AssignUser(w http.ResponseWriter, r *http.Request) {
	// Authorize before resolving the {uuid} path param: this uses a nil
	// target (wildcard-admin check, like the rest of this handler), so it
	// does not need any data from the app row. Running it first means a
	// caller without the required grant is rejected with 403 before any
	// entity resolution occurs, so an arbitrary uuid can't be used as a
	// cross-entity existence probe against the shared entities table.
	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		writeAuthzError(w, r, err)
		return
	}

	appID, appUUID, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	var req assignUserRequest
	if err := server.Decode(r, &req); err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	if req.UserUUID == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "user_uuid", Code: "users.user_uuid_required", Message: "user_uuid is required",
		}))
		return
	}

	userUUID, err := uuid.Parse(req.UserUUID)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.assign_user: get user account: %w", err))
		return
	}

	roles := req.Roles
	if roles == nil {
		roles = []string{}
	}

	if err := h.q.AssignUserAccountToApp(r.Context(), db.AssignUserAccountToAppParams{
		AppID:         appID,
		UserAccountID: ua.ID,
		Roles:         roles,
	}); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.assign_user: %w", err))
		return
	}

	server.JSON(w, http.StatusCreated, map[string]any{
		"app_uuid":  appUUID.String(),
		"user_uuid": ua.Uuid.String(),
		"roles":     roles,
	})
}

// ListAppUsers handles GET /v1/apps/{uuid}/user-accounts (admin).
func (h *AppsHandler) ListAppUsers(w http.ResponseWriter, r *http.Request) {
	// Authorize before resolving the {uuid} path param — see AssignUser's
	// comment above for why this ordering matters.
	if err := h.az.Authorize(r.Context(), "read", nil); err != nil {
		writeAuthzError(w, r, err)
		return
	}

	appID, _, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	members, err := h.q.ListAppUserAccounts(r.Context(), appID)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.list_users: %w", err))
		return
	}

	resp := make([]map[string]any, 0, len(members))
	for _, m := range members {
		resp = append(resp, map[string]any{
			"user_account_id": m.UserAccountID,
			"roles":           m.Roles,
		})
	}
	server.JSON(w, http.StatusOK, map[string]any{"user_accounts": resp})
}

// RemoveUser handles DELETE /v1/apps/{uuid}/user-accounts/{user_account_uuid} (admin).
func (h *AppsHandler) RemoveUser(w http.ResponseWriter, r *http.Request) {
	// Authorize before resolving the {uuid} path param — see AssignUser's
	// comment above for why this ordering matters.
	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		writeAuthzError(w, r, err)
		return
	}

	appID, _, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	rawUserUUID := chi.URLParam(r, "user_account_uuid")
	userUUID, err := uuid.Parse(rawUserUUID)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.remove_user: get user account: %w", err))
		return
	}

	if err := h.q.RemoveUserAccountFromApp(r.Context(), db.RemoveUserAccountFromAppParams{
		AppID:         appID,
		UserAccountID: ua.ID,
	}); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.remove_user: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type updateRolesRequest struct {
	Roles []string `json:"roles"`
}

// UpdateUserRoles handles PUT /v1/apps/{uuid}/user-accounts/{user_account_uuid}/roles (admin).
func (h *AppsHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	// Authorize before resolving the {uuid} path param — see AssignUser's
	// comment above for why this ordering matters.
	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		writeAuthzError(w, r, err)
		return
	}

	appID, appUUID, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	rawUserUUID := chi.URLParam(r, "user_account_uuid")
	userUUID, err := uuid.Parse(rawUserUUID)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.update_roles: get user account: %w", err))
		return
	}

	var req updateRolesRequest
	if err := server.Decode(r, &req); err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	if req.Roles == nil {
		req.Roles = []string{}
	}

	if err := h.q.SetAppUserAccountRoles(r.Context(), db.SetAppUserAccountRolesParams{
		AppID:         appID,
		UserAccountID: ua.ID,
		Roles:         req.Roles,
	}); err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("apps.update_roles: %w", err))
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"app_uuid":  appUUID.String(),
		"user_uuid": ua.Uuid.String(),
		"roles":     req.Roles,
	})
}

// loadAppByUUIDParam extracts the {uuid} chi param and resolves it to the
// app's internal id via entityResolver (apps.id == entities.id, per
// mod-core's FK-anchor entity-subtype pattern for apps). Returns the
// resolved id, the parsed uuid (echoed back in responses), and whether
// resolution succeeded. An unknown app uuid writes ErrNotFound (see
// NewAppsHandler's AllowNotFound("app") call).
//
// entityResolver.Resolve only confirms that the uuid exists somewhere in the
// shared, cross-module entities table — it has no notion of "app" beyond the
// not-found policy slug, so it resolves equally well for a natural_person,
// corporation, or service_account uuid. Previously this method queried
// mod-users' own apps table directly (SELECT ... FROM apps WHERE uuid = $1),
// which was inherently type-scoped: a uuid naming a real but differently-
// typed entity could never match. To preserve that type-scoping now that
// resolution goes through the shared table, this method does a supplementary
// lookup against mod-core's apps table (via coreQ.GetAppByUUID, which joins
// apps to entities) and treats a miss there the same as an unresolvable uuid:
// ErrNotFound.
func (h *AppsHandler) loadAppByUUIDParam(w http.ResponseWriter, r *http.Request) (int64, uuid.UUID, bool) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return 0, uuid.UUID{}, false
	}
	appID, err := h.entityResolver.Resolve(r.Context(), h.coreQ, parsed, "app")
	if err != nil {
		apiresp.WriteError(w, r, err)
		return 0, uuid.UUID{}, false
	}

	if _, err := h.coreQ.GetAppByUUID(r.Context(), parsed); err != nil {
		if err == pgx.ErrNoRows {
			apiresp.WriteError(w, r, apiresp.ErrNotFound)
			return 0, uuid.UUID{}, false
		}
		apiresp.WriteError(w, r, fmt.Errorf("apps.load_app: %w", err))
		return 0, uuid.UUID{}, false
	}

	return appID, parsed, true
}
