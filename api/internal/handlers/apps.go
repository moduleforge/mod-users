package handlers

import (
	"errors"
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

// NewAppsHandler creates an AppsHandler. The apps lookup goes through
// coreQ.GetAppByUUID (apps.id == entities.id, per mod-core's FK-anchor
// entity-subtype pattern), so entityResolver is no longer used by this
// handler. The parameter and field are kept only because the module
// manifest (moduleforge.module.yaml) and api/handlers/handlers.go pass it.
//
// The constructor deliberately does not call entityResolver.AllowNotFound:
// that resolver is shared with mod-core's services, and opting "app" into
// 404 would re-open the existence-probe policy mod-core closed. A 404 for an
// unknown app is legitimate here because the nil-target Authorize has already
// passed by the time the app is looked up.
func NewAppsHandler(
	pool txhelper.DB,
	q *db.Queries,
	az coreAuthz.Authorizer,
	observers *observer.ObserverGroup,
	entityResolver *entity.Resolver,
	coreQ *coredb.Queries,
) *AppsHandler {
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

// Validate runs the stateless user_uuid checks before authorization.
func (r *assignUserRequest) Validate() []apiresp.FieldError {
	if r.UserUUID == "" {
		return []apiresp.FieldError{{
			Field: "user_uuid", Code: "users.user_uuid_required", Message: "user_uuid is required",
		}}
	}
	if _, err := apiresp.ParseUUIDParam("user_uuid", r.UserUUID); err != nil {
		return []apiresp.FieldError{{
			Field: "user_uuid", Code: "invalid_uuid", Message: "must be a valid UUID",
		}}
	}
	return nil
}

// AssignUser handles POST /v1/apps/{uuid}/user-accounts (admin).
//
// Stateless checks (path UUID, body decode and validation) run before the
// nil-target Authorize (wildcard-admin check, not AuthorizeType); the app
// lookup runs only after it, so an arbitrary uuid cannot be used as an
// existence probe by a caller without the grant.
func (h *AppsHandler) AssignUser(w http.ResponseWriter, r *http.Request) {
	appUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	var req assignUserRequest
	if !server.Decode(w, r, &req) {
		return
	}

	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	appID, ok := h.loadApp(w, r, appUUID)
	if !ok {
		return
	}

	userUUID, err := apiresp.ParseUUIDParam("user_uuid", req.UserUUID)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The account is a referenced value in the body, not the addressed
		// resource (the app in the path is), so a miss is a 422 on the field
		// rather than a 404 for the route. Authorization has already passed.
		apiresp.WriteError(w, r, apiresp.Unprocessable(apiresp.FieldError{
			Field:   "user_uuid",
			Code:    "users.user_account_unknown",
			Message: "no user account exists with that uuid",
		}))
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
	appUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	if err := h.az.Authorize(r.Context(), "read", nil); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	appID, ok := h.loadApp(w, r, appUUID)
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
	appUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	userUUID, err := apiresp.ParseUUIDParam("user_account_uuid", chi.URLParam(r, "user_account_uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	appID, ok := h.loadApp(w, r, appUUID)
	if !ok {
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
	appUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	userUUID, err := apiresp.ParseUUIDParam("user_account_uuid", chi.URLParam(r, "user_account_uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	var req updateRolesRequest
	if !server.Decode(w, r, &req) {
		return
	}

	if err := h.az.Authorize(r.Context(), "update", nil); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	appID, ok := h.loadApp(w, r, appUUID)
	if !ok {
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

// loadApp resolves an already-parsed app uuid to the app's internal id
// (apps.id == entities.id) via coreQ.GetAppByUUID, which joins apps to
// entities and so is inherently type-scoped: a uuid naming a differently-
// typed entity does not match. It must be called only after the nil-target
// Authorize has passed, which is what makes the ErrNotFound it writes for a
// miss legitimate rather than an existence probe. Like the entityResolver
// lookup it replaces, it does not filter archived apps. Returns the id and
// whether the lookup succeeded.
func (h *AppsHandler) loadApp(w http.ResponseWriter, r *http.Request, appUUID uuid.UUID) (int64, bool) {
	app, err := h.coreQ.GetAppByUUID(r.Context(), appUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiresp.WriteError(w, r, apiresp.ErrNotFound)
			return 0, false
		}
		apiresp.WriteError(w, r, fmt.Errorf("apps.load_app: %w", err))
		return 0, false
	}
	return app.ID, true
}
