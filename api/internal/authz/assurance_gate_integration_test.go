//go:build integration

package authz_test

// assurance_gate_integration_test.go proves the operation-level assurance gate
// against the seeded operation policies (authz_operations.min_acr and
// max_auth_age, read through the real OperationRegistry): a session or
// credential context whose ACR is below min_acr, or whose authentication is
// older than max_auth_age (impersonate and credentials), is refused with the
// shared users.step_up_required action before any grant arm runs. Trusted
// system-actor contexts and the anonymous context skip it. The unit suite
// (authz_test.go) covers the same rules against stubs; this suite pins them to
// the seed.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

type gateOp struct {
	slug       string
	minACR     int
	maxAuthAge time.Duration
}

// seededGateOps reads the policy columns the gate applies from the database.
func seededGateOps(t *testing.T) []gateOp {
	t.Helper()
	rows, err := integPool.Query(context.Background(),
		`SELECT slug, min_acr, COALESCE(EXTRACT(EPOCH FROM max_auth_age), 0) FROM authz_operations ORDER BY id`)
	if err != nil {
		t.Fatalf("list operation policies: %v", err)
	}
	defer rows.Close()
	var out []gateOp
	for rows.Next() {
		var op gateOp
		var secs float64
		if err := rows.Scan(&op.slug, &op.minACR, &secs); err != nil {
			t.Fatalf("scan operation policy: %v", err)
		}
		op.maxAuthAge = time.Duration(secs * float64(time.Second))
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("operation policy rows: %v", err)
	}
	return out
}

// requireStepUpAction asserts err is the shared action-required refusal: the
// *apiresp.ActionRequiredError (errors.As, wrapped or not), never a plain 403,
// rendered by apiresp.WriteError as 409 users.step_up_required.
func requireStepUpAction(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: got nil, want the step-up refusal", msg)
		return
	}
	var are *apiresp.ActionRequiredError
	if !errors.As(err, &are) {
		t.Errorf("%s: %v does not satisfy *apiresp.ActionRequiredError", msg, err)
		return
	}
	if !errors.Is(err, authz.ErrStepUpRequired) || errors.Is(err, authz.ErrForbidden) {
		t.Errorf("%s: %v, want ErrStepUpRequired and not ErrForbidden", msg, err)
	}
	rec := httptest.NewRecorder()
	apiresp.WriteError(rec, httptest.NewRequest(http.MethodPost, "/", nil), fmt.Errorf("outer: %w", err))
	var body struct {
		Action struct {
			Code string `json:"code"`
		} `json:"action"`
	}
	if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil {
		t.Errorf("%s: decode %q: %v", msg, rec.Body.String(), jerr)
		return
	}
	if rec.Code != http.StatusConflict || body.Action.Code != "users.step_up_required" {
		t.Errorf("%s: rendered %d %s, want 409 users.step_up_required", msg, rec.Code, rec.Body.String())
	}
}

func TestInteg_AssuranceGate_SeededPolicies(t *testing.T) {
	ops := seededGateOps(t)
	var withAge int
	for _, op := range ops {
		if op.maxAuthAge > 0 {
			withAge++
		}
	}
	// The seed's two max_auth_age operations are the ones the task names.
	if withAge != 2 {
		t.Fatalf("premise: %d operations have a max_auth_age, want exactly impersonate and credentials", withAge)
	}

	admin := seedOperator(t, "manage") // wildcard manage: every operation, so only the gate can refuse
	adminUser := ownerHolder(t, admin)
	target := seedUserID(t)
	key, _ := fxAPIKey(t, integPool, adminUser)

	session := func(acr int16, age time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: adminUser, ProfileID: admin, SessionID: 1, ACR: acr, AuthTime: time.Now().Add(-age),
		})
	}
	clientCredentials := func(acr int16, age time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: adminUser, ProfileID: admin, CredentialID: key, SessionID: 0, ACR: acr, AuthTime: time.Now().Add(-age),
		})
	}

	for _, op := range ops {
		slug := op.slug
		minACR := int16(op.minACR)
		for _, kind := range []struct {
			name string
			ctx  func(acr int16, age time.Duration) context.Context
		}{{"session", session}, {"client credentials", clientCredentials}} {
			// ACR below min_acr is a step-up, before the wildcard arm.
			requireStepUpAction(t, integAZ.Authorize(kind.ctx(minACR-1, 0), slug, &target), kind.name+": "+slug+" below min_acr")
			requireStepUpAction(t, integAZ.Authorize(kind.ctx(minACR-1, 0), slug, nil), kind.name+": "+slug+" below min_acr, nil target")
			requireStepUpAction(t, integAZ.AuthorizeType(kind.ctx(minACR-1, 0), slug, typeIDForSlug(t, "organization")),
				kind.name+": AuthorizeType "+slug+" below min_acr")
			requireStepUpAction(t, integAZ.AuthorizeTypeIn(kind.ctx(minACR-1, 0), slug, typeIDForSlug(t, "organization"), target),
				kind.name+": AuthorizeTypeIn "+slug+" below min_acr")

			// At min_acr and freshly authenticated: passes.
			if err := integAZ.Authorize(kind.ctx(minACR, time.Second), slug, &target); err != nil {
				t.Errorf("%s: %s at min_acr, fresh: %v, want nil", kind.name, slug, err)
			}

			if op.maxAuthAge > 0 {
				stale := op.maxAuthAge + time.Minute
				fresh := op.maxAuthAge - time.Minute
				requireStepUpAction(t, integAZ.Authorize(kind.ctx(minACR, stale), slug, &target), kind.name+": "+slug+" older than max_auth_age")
				if err := integAZ.Authorize(kind.ctx(minACR, fresh), slug, &target); err != nil {
					t.Errorf("%s: %s within max_auth_age: %v, want nil", kind.name, slug, err)
				}
			} else if err := integAZ.Authorize(kind.ctx(minACR, 24*time.Hour), slug, &target); err != nil {
				// An operation without max_auth_age is not aged out.
				t.Errorf("%s: %s with an old authentication but no max_auth_age: %v, want nil", kind.name, slug, err)
			}
		}
	}
}

func TestInteg_AssuranceGate_SkippedContexts(t *testing.T) {
	admin := seedOperator(t, "manage")
	adminUser := ownerHolder(t, admin)
	target := seedUserID(t)

	// A system actor (no session, no credential, no AuthTime) passes the gate
	// even for the aged operations, and falls through to its grants.
	system := opctx.WithSystemActor(context.Background(), adminUser, admin)
	for _, op := range []string{"read", "impersonate", "credentials"} {
		if err := integAZ.Authorize(system, op, &target); err != nil {
			t.Errorf("system actor, %s: %v, want nil (the gate is skipped)", op, err)
		}
	}

	// The anonymous context skips the gate and holds no grants, so what it gets
	// is ErrForbidden, never a step-up, even with a stale zero-ACR session id.
	anonProfile := anonymousProfileID(t)
	anonymous := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: ownerHolder(t, anonProfile), ProfileID: anonProfile, SessionID: 1, ACR: 0, Anonymous: true,
	})
	for _, op := range []string{"read", "impersonate", "credentials"} {
		err := integAZ.Authorize(anonymous, op, &target)
		if !errors.Is(err, authz.ErrForbidden) || errors.Is(err, authz.ErrStepUpRequired) {
			t.Errorf("anonymous, %s: %v, want ErrForbidden and no step-up", op, err)
		}
	}
}

func TestInteg_AssuranceGate_ImpersonationIsNotExempt(t *testing.T) {
	admin := seedOperator(t, "manage")
	adminUser := ownerHolder(t, admin)
	target := seedUserID(t)

	ctx := func(age time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: adminUser, ProfileID: admin, ImpersonatorPrincipalID: adminUser + 1,
			SessionID: 1, ACR: 1, AuthTime: time.Now().Add(-age),
		})
	}
	requireStepUpAction(t, integAZ.Authorize(ctx(time.Hour), "impersonate", &target), "impersonation context, stale")
	if err := integAZ.Authorize(ctx(time.Minute), "impersonate", &target); err != nil {
		t.Errorf("impersonation context, fresh: %v, want nil", err)
	}
}
