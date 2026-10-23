// Package principals provides the HTTP handlers of the /v1/principals route
// group: list, read, disable and enable. Every route requires an
// authenticated caller (mount the group behind auth.NewRequireAuth).
//
// Authorization is the service's: the list is scoped by the row-level access
// functions, a read needs `read` on the principal (an unreadable principal is
// a 404, like an unknown one), and disable and enable need `update` plus a
// fresh authentication (409 users.step_up_required). The credential
// sub-routes of /v1/principals belong to the authorization-engine phase.
package principals

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// PrincipalsHandler serves the /v1/principals routes. Construct it with
// NewPrincipalsHandler.
type PrincipalsHandler struct {
	users *svc.UserService
}

// NewPrincipalsHandler constructs the /v1/principals handler over the user
// service, which must have been wired with UserService.WithSelfService.
func NewPrincipalsHandler(users *svc.UserService) *PrincipalsHandler {
	return &PrincipalsHandler{users: users}
}

// RegisterPrincipalRoutes mounts the /v1/principals routes onto r. The caller
// applies the authentication middleware (auth.NewRequireAuth) first.
func RegisterPrincipalRoutes(r chi.Router, h *PrincipalsHandler) {
	r.Get("/", h.List)
	r.Get("/{uuid}", h.Get)
	r.Post("/{uuid}/disable", h.Disable)
	r.Post("/{uuid}/enable", h.Enable)
}

// writeError renders err: a service.AuthError with its own code and status,
// anything else through apiresp.WriteError.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *svc.AuthError
	if errors.As(err, &ae) {
		apiresp.WriteJSON(w, ae.Status, ae.Envelope())
		return
	}
	apiresp.WriteError(w, r, err)
}

// requireIdentity writes a 500 and returns false when the request has no
// authenticated identity (the group was mounted outside RequireAuth).
func requireIdentity(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := localauth.IdentityFromContext(r.Context()); !ok {
		apiresp.WriteError(w, r, errors.New("principals routes mounted outside RequireAuth"))
		return false
	}
	return true
}

// queryInt32 parses an optional non-negative integer query parameter. An
// absent parameter is 0 (the service applies its default); a malformed or
// negative one is a 400 field error.
func queryInt32(r *http.Request, name string) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, apiresp.InvalidInput(apiresp.FieldError{
			Field: name, Code: "users." + name + "_invalid", Message: name + " must be a non-negative integer",
		})
	}
	return int32(n), nil
}

// List handles GET /v1/principals?kind=&limit=&offset=. kind is user or
// service_account (both when omitted). 200 with {"principals", "total",
// "limit", "offset"}.
func (h *PrincipalsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	limit, err := queryInt32(r, "limit")
	if err != nil {
		writeError(w, r, err)
		return
	}
	offset, err := queryInt32(r, "offset")
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := h.users.ListPrincipals(r.Context(), svc.ListPrincipalsInput{
		Kind: r.URL.Query().Get("kind"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, page)
}

// Get handles GET /v1/principals/{uuid}. 404 for an unknown principal and for
// one the caller may not read.
func (h *PrincipalsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.users.GetPrincipal(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// disableRequest is the optional body of POST /v1/principals/{uuid}/disable.
type disableRequest struct {
	// Reason is free text recorded on the principal.
	Reason string `json:"reason"`
}

// Disable handles POST /v1/principals/{uuid}/disable: disables the principal
// and revokes its sessions. The body is optional ({"reason": "..."}). 200 with
// the updated principal.
func (h *PrincipalsHandler) Disable(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req disableRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !server.Decode(w, r, &req) {
			return
		}
	}
	view, err := h.users.DisablePrincipal(r.Context(), id, req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// Enable handles POST /v1/principals/{uuid}/enable. The body is ignored. 200
// with the updated principal.
func (h *PrincipalsHandler) Enable(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.users.EnablePrincipal(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}
