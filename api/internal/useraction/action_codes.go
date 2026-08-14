// Package useraction declares mod-users' registered apiresp.ActionCode
// values. Per the API response design doc's "Action-code vocabulary"
// section, "the closed set of action codes and their bound statuses is owned
// per-module, in each module's own API reference" — mod-core/api/apiresp
// deliberately holds no registry or validation logic of its own. This
// package is that per-module registry for mod-users: each code is declared
// once here rather than re-stated as inline apiresp.ActionCode literals at
// every call site that needs one.
package useraction

import (
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
)

// EmailUnverified signals that the actor is authenticated but their email is
// unverified; the client navigates to email verification (action.path)
// before retrying.
var EmailUnverified = apiresp.ActionCode{Code: "users.email_unverified", Status: http.StatusForbidden}

// StepUpRequired signals that the operation requires a fresh step-up
// (re-authentication / MFA) challenge; the client navigates to the
// challenge, then retries.
var StepUpRequired = apiresp.ActionCode{Code: "users.step_up_required", Status: http.StatusConflict}

// OIDCNotConfirmed signals that the deployment's OIDC configuration is
// incomplete; the client navigates to configuration.
var OIDCNotConfirmed = apiresp.ActionCode{Code: "users.oidc_not_confirmed", Status: http.StatusServiceUnavailable}
