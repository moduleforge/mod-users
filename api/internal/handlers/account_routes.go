package handlers

import "github.com/go-chi/chi/v5"

// RegisterAccountRoutes mounts every route in the /v1/user-accounts and
// /v1/apps user-account management group onto r. The caller is responsible
// for applying middleware (requireOIDCConfirmed, requireAuth,
// requireVerifiedEmail) and the /v1 prefix before calling this function;
// RegisterAccountRoutes does not add middleware or a prefix itself.
//
// Three handler args are required because the assume-identity route is served
// by *AssumeHandler and the apps/user-accounts routes are served by
// *AppsHandler — neither is reachable from *UserAccountsHandler. Passing all
// three avoids a structural change to any of the handler types.
//
// Routes registered (all paths relative to the prefix the caller supplies):
//
//	GET    /user-accounts
//	POST   /user-accounts
//	GET    /user-accounts/{uuid}
//	PUT    /user-accounts/{uuid}
//	DELETE /user-accounts/{uuid}
//	POST   /user-accounts/{uuid}/grant-admin
//	POST   /user-accounts/{uuid}/revoke-admin
//	POST   /user-accounts/{uuid}/assume
//	POST   /apps/{uuid}/user-accounts
//	GET    /apps/{uuid}/user-accounts
//	DELETE /apps/{uuid}/user-accounts/{user_account_uuid}
//	PUT    /apps/{uuid}/user-accounts/{user_account_uuid}/roles
func RegisterAccountRoutes(r chi.Router, h *UserAccountsHandler, assume *AssumeHandler, apps *AppsHandler) {
	// User account management. Authorization is enforced at the service layer:
	// list/create require wildcard admin; get/update/delete enforce per-entity
	// authorization. RequireAdmin middleware has been removed; the Authorizer is
	// the sole gate.
	r.Get("/user-accounts", h.List)
	r.Post("/user-accounts", h.Create)
	r.Get("/user-accounts/{uuid}", h.Get)
	r.Put("/user-accounts/{uuid}", h.Update)
	r.Delete("/user-accounts/{uuid}", h.Delete)
	r.Post("/user-accounts/{uuid}/grant-admin", h.GrantAdmin)
	r.Post("/user-accounts/{uuid}/revoke-admin", h.RevokeAdmin)

	// Assume identity (admin).
	r.Post("/user-accounts/{uuid}/assume", assume.Assume)

	// Apps user-accounts.
	r.Post("/apps/{uuid}/user-accounts", apps.AssignUser)
	r.Get("/apps/{uuid}/user-accounts", apps.ListAppUsers)
	r.Delete("/apps/{uuid}/user-accounts/{user_account_uuid}", apps.RemoveUser)
	r.Put("/apps/{uuid}/user-accounts/{user_account_uuid}/roles", apps.UpdateUserRoles)
}
