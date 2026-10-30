package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// This file holds the principal-administration methods behind /v1/principals.
//
// Authority. Unlike /v1/me, these act on a principal other than the caller's
// and so authorize through the Authorizer: the list through the row-scoping
// functions accessible_user_ids_for_actor and
// accessible_service_account_ids_for_actor, a read through `read` on the
// principal, and disable and enable through `update`. A principal the caller
// cannot read is reported as not found (masking), exactly like an unknown
// uuid. The credential sub-routes of /v1/principals belong to the
// authorization-engine phase.

// Operation slugs the principal methods authorize.
const (
	opSlugRead   = "read"
	opSlugList   = "list"
	opSlugUpdate = "update"
)

// maxDisableReasonRunes bounds the free-text reason of a disable.
const maxDisableReasonRunes = 500

// PrincipalView is the administration view of a principal (GET /v1/principals,
// GET /v1/principals/{uuid}). Internal ids are never serialized.
type PrincipalView struct {
	UUID        uuid.UUID `json:"uuid"`
	Kind        string    `json:"kind"`
	DisplayName string    `json:"display_name"`
	// Email is the primary email of a user, null when there is none.
	Email   *string `json:"email"`
	IsGuest bool    `json:"is_guest"`
	// Managed is true for a managed user; ManagingPartyUUID names its party.
	Managed           bool       `json:"managed"`
	ManagingPartyUUID *uuid.UUID `json:"managing_party_uuid"`
	Disabled          bool       `json:"disabled"`
	DisabledAt        *time.Time `json:"disabled_at"`
	DisabledReason    *string    `json:"disabled_reason"`
	CreatedAt         time.Time  `json:"created_at"`
}

// ListPrincipalsInput is the input of ListPrincipals.
type ListPrincipalsInput struct {
	// Kind restricts the list to PrincipalKindUser or
	// PrincipalKindServiceAccount; empty lists both.
	Kind   string
	Limit  int32
	Offset int32
}

// PrincipalPage is one page of principals.
type PrincipalPage struct {
	Principals []PrincipalView `json:"principals"`
	Total      int64           `json:"total"`
	Limit      int32           `json:"limit"`
	Offset     int32           `json:"offset"`
}

// principalScopeSQL selects the (id, created_at) of the principals the actor
// ($1, a profile entity id) may list for the operations in $2, restricted to
// kind $3 (” for both). The access functions are generated per type by
// mod-core's setup.ApplyFuncs through localAuthz.AccessFuncGenerator, which
// filters every row through the owning-party gate; mod-users ships only the
// user stub. Run it only in a transaction bound with setup.BindAuthInputs.
const principalScopeSQL = `
SELECT e.id AS id, e.created_at AS created_at
  FROM mod_users.users u
  JOIN public.entities e ON e.id = u.entity_id AND e.archived_at IS NULL
  JOIN public.accessible_user_ids_for_actor($1::bigint, $2::int[]) acc ON acc.entity_id = u.entity_id
 WHERE $3::text IN ('', '` + PrincipalKindUser + `')
UNION ALL
SELECT e.id, e.created_at
  FROM public.service_accounts sa
  JOIN public.entities e ON e.id = sa.entity_id AND e.archived_at IS NULL
  JOIN public.accessible_service_account_ids_for_actor($1::bigint, $2::int[]) acc ON acc.entity_id = sa.entity_id
 WHERE $3::text IN ('', '` + PrincipalKindServiceAccount + `')`

const (
	principalPageSQL  = `SELECT id FROM (` + principalScopeSQL + `) s ORDER BY created_at, id LIMIT $4 OFFSET $5`
	principalCountSQL = `SELECT count(*) FROM (` + principalScopeSQL + `) s`
)

// principalExtrasSQL reads what PrincipalView adds to a PrincipalSummary.
const principalExtrasSQL = `
SELECT e.id, e.created_at, pr.disabled_at, pr.disabled_reason,
       u.managing_party_id IS NOT NULL, mpe.uuid
  FROM public.entities e
  JOIN public.principals pr ON pr.entity_id = e.id
  LEFT JOIN mod_users.users u ON u.entity_id = e.id
  LEFT JOIN public.entities mpe ON mpe.id = u.managing_party_id
 WHERE e.id = ANY($1::bigint[])`

// ListPrincipals lists the principals the caller may list, oldest first
// (GET /v1/principals). The row scope is the access functions' (decision U18);
// a caller with no list authority gets an empty page.
func (s *UserService) ListPrincipals(ctx context.Context, in ListPrincipalsInput) (PrincipalPage, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return PrincipalPage{}, err
	}
	switch in.Kind {
	case "", PrincipalKindUser, PrincipalKindServiceAccount:
	default:
		return PrincipalPage{}, invalidField("kind", "users.principal_kind_invalid", "kind must be user or service_account")
	}
	if s.ops == nil || s.pool == nil {
		return PrincipalPage{}, errors.New("users: principal listing is not configured")
	}
	limit, offset := coreservice.Pagination{Limit: in.Limit, Offset: in.Offset}.Normalize()
	opIDs, err := s.ops.SatisfiedBy(opSlugList)
	if err != nil {
		return PrincipalPage{}, fmt.Errorf("users: resolve list operation: %w", err)
	}

	// WHY one read-only transaction per request: the access functions read the
	// request's session, credential and system-actor marker from
	// transaction-local settings, so the count, the page and the detail reads
	// must share the transaction that bound them, and the binding must not
	// outlive this request (it ends with the rollback). IMPLIES: the
	// transaction is never reused for another context, and nothing here can
	// write.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PrincipalPage{}, fmt.Errorf("users: begin principal list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// WHY bind before any list query: accessible_user_ids_for_actor and
	// accessible_service_account_ids_for_actor pass these inputs to the
	// owning-party row filter (mod_users.access_row_permitted), which applies
	// to each row the gate single-row Authorize applies. IMPLIES: a binding
	// error refuses the request; it never falls back to an unbound list (which
	// would silently drop every row a restrictive party policy gates) or to a
	// binding left over from another request. BindAuthInputs writes the
	// settings transaction-locally (set_config is_local = true), never for the
	// whole session.
	if err := setup.BindAuthInputs(ctx, tx); err != nil {
		return PrincipalPage{}, fmt.Errorf("users: bind authorization inputs: %w", err)
	}

	var total int64
	if err := tx.QueryRow(ctx, principalCountSQL, ac.ProfileID, opIDs, in.Kind).Scan(&total); err != nil {
		return PrincipalPage{}, fmt.Errorf("users: count principals: %w", err)
	}
	ids := make([]int64, 0, limit)
	rows, err := tx.Query(ctx, principalPageSQL, ac.ProfileID, opIDs, in.Kind, limit, offset)
	if err != nil {
		return PrincipalPage{}, fmt.Errorf("users: list principals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return PrincipalPage{}, fmt.Errorf("users: scan principal id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return PrincipalPage{}, fmt.Errorf("users: list principals: %w", err)
	}
	rows.Close()

	views, err := principalViews(ctx, tx, ids)
	if err != nil {
		return PrincipalPage{}, err
	}
	return PrincipalPage{Principals: views, Total: total, Limit: limit, Offset: offset}, nil
}

// adminCaller returns the caller's AuthContext for a principal-administration
// call: authenticated, acting as a profile.
func adminCaller(ctx context.Context) (opctx.AuthContext, error) {
	ac, ok := opctx.Auth(ctx)
	if !ok || ac.Anonymous || ac.PrincipalID <= 0 || ac.ProfileID <= 0 {
		return opctx.AuthContext{}, apiresp.ErrUnauthenticated
	}
	return ac, nil
}

// principalViews builds the views of ids, in the order given. Ids that are not
// principals are skipped.
func principalViews(ctx context.Context, db usersdb.DBTX, ids []int64) ([]PrincipalView, error) {
	out := make([]PrincipalView, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	sums, err := ReadPrincipalSummaries(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	type extra struct {
		createdAt  time.Time
		disabledAt *time.Time
		reason     pgtype.Text
		managed    bool
		party      pgtype.UUID
	}
	extras := make(map[int64]extra, len(ids))
	rows, err := db.Query(ctx, principalExtrasSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("users: read principal details: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id        int64
			x         extra
			createdAt pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &createdAt, &x.disabledAt, &x.reason, &x.managed, &x.party); err != nil {
			return nil, fmt.Errorf("users: scan principal details: %w", err)
		}
		x.createdAt = createdAt.Time
		extras[id] = x
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("users: read principal details: %w", err)
	}

	for _, id := range ids {
		sum, ok := sums[id]
		if !ok {
			continue
		}
		x := extras[id]
		v := PrincipalView{
			UUID: sum.PrincipalUUID, Kind: sum.Kind, DisplayName: sum.DisplayName, IsGuest: sum.IsGuest,
			Managed: x.managed, ManagingPartyUUID: optionalUUID(x.party),
			Disabled: sum.Disabled, DisabledAt: x.disabledAt, DisabledReason: optionalText(x.reason),
			CreatedAt: x.createdAt,
		}
		if sum.PrimaryEmail != "" {
			email := sum.PrimaryEmail
			v.Email = &email
		}
		out = append(out, v)
	}
	return out, nil
}

// lookupPrincipal resolves a principal uuid to its summary. An unknown uuid,
// an archived entity and a principal that is neither a user nor a service
// account are all apiresp.ErrNotFound.
func (s *UserService) lookupPrincipal(ctx context.Context, id uuid.UUID) (PrincipalSummary, error) {
	ent, err := s.coreQ.GetEntityByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrincipalSummary{}, apiresp.ErrNotFound
	}
	if err != nil {
		return PrincipalSummary{}, fmt.Errorf("users: load entity: %w", err)
	}
	if ent.ArchivedAt != nil {
		return PrincipalSummary{}, apiresp.ErrNotFound
	}
	sums, err := ReadPrincipalSummaries(ctx, s.db, []int64{ent.ID})
	if err != nil {
		return PrincipalSummary{}, err
	}
	sum, ok := sums[ent.ID]
	if !ok || (sum.Kind != PrincipalKindUser && sum.Kind != PrincipalKindServiceAccount) {
		return PrincipalSummary{}, apiresp.ErrNotFound
	}
	return sum, nil
}

// authorizeMasked authorizes op on the principal and reports a plain refusal
// as apiresp.ErrNotFound, so a principal the caller may not read is
// indistinguishable from one that does not exist. An action-required refusal
// (a step-up) is returned as is.
func (s *UserService) authorizeMasked(ctx context.Context, op string, principalID int64) error {
	if s.az == nil {
		return apiresp.ErrForbidden
	}
	err := s.az.Authorize(ctx, op, &principalID)
	if err == nil {
		return nil
	}
	var action *apiresp.ActionRequiredError
	if errors.As(err, &action) {
		return err
	}
	if errors.Is(err, apiresp.ErrForbidden) {
		return apiresp.ErrNotFound
	}
	return err
}

// readablePrincipal resolves uuid and authorizes `read` on it (masked).
func (s *UserService) readablePrincipal(ctx context.Context, id uuid.UUID) (PrincipalSummary, error) {
	if _, err := adminCaller(ctx); err != nil {
		return PrincipalSummary{}, err
	}
	sum, err := s.lookupPrincipal(ctx, id)
	if err != nil {
		return PrincipalSummary{}, err
	}
	if err := s.authorizeMasked(ctx, opSlugRead, sum.PrincipalID); err != nil {
		return PrincipalSummary{}, err
	}
	return sum, nil
}

// GetPrincipal returns one principal (GET /v1/principals/{uuid}). A principal
// that does not exist, or that the caller may not read, is apiresp.ErrNotFound.
func (s *UserService) GetPrincipal(ctx context.Context, id uuid.UUID) (PrincipalView, error) {
	sum, err := s.readablePrincipal(ctx, id)
	if err != nil {
		return PrincipalView{}, err
	}
	return s.principalView(ctx, s.db, sum.PrincipalID)
}

func (s *UserService) principalView(ctx context.Context, db usersdb.DBTX, principalID int64) (PrincipalView, error) {
	views, err := principalViews(ctx, db, []int64{principalID})
	if err != nil {
		return PrincipalView{}, err
	}
	if len(views) == 0 {
		return PrincipalView{}, apiresp.ErrNotFound
	}
	return views[0], nil
}

// writablePrincipal resolves uuid for a state change: `read` (masked 404), then
// `update` (403), then the freshness gate (409 users.step_up_required). That
// order keeps the error precedence 401, 400, 403, 404 or 409 for a caller that
// can read the principal.
func (s *UserService) writablePrincipal(ctx context.Context, id uuid.UUID) (PrincipalSummary, opctx.AuthContext, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return PrincipalSummary{}, opctx.AuthContext{}, err
	}
	sum, err := s.readablePrincipal(ctx, id)
	if err != nil {
		return PrincipalSummary{}, opctx.AuthContext{}, err
	}
	if err := s.az.Authorize(ctx, opSlugUpdate, &sum.PrincipalID); err != nil {
		return PrincipalSummary{}, opctx.AuthContext{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return PrincipalSummary{}, opctx.AuthContext{}, err
	}
	return sum, ac, nil
}

// principalAudit is the observer payload for a disable or enable.
func principalAudit(id uuid.UUID, disabled bool, reason *string) map[string]any {
	m := map[string]any{"uuid": id.String(), "disabled": disabled}
	if reason != nil {
		m["reason"] = *reason
	}
	return m
}

// principalResource is the observer resource for a principal of kind.
func principalResource(kind string) string {
	if kind == PrincipalKindServiceAccount {
		return resourceServiceAccount
	}
	return resourceUser
}

// DisablePrincipal disables a principal (POST /v1/principals/{uuid}/disable):
// it sets principals.disabled_at and revokes every session of the principal
// with reason `principal_disabled`, so the next request of each is refused.
// It needs `update` on the principal and a fresh authentication. Disabling a
// disabled principal changes nothing. reason is optional free text recorded on
// the principal. Observations: `update` on `user` (`service_account` for a
// service account) and `revoke` `session` for each session ended.
func (s *UserService) DisablePrincipal(ctx context.Context, id uuid.UUID, reason string) (PrincipalView, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > maxDisableReasonRunes {
		return PrincipalView{}, invalidField("reason", "users.disable_reason_too_long", "reason is too long")
	}
	sum, _, err := s.writablePrincipal(ctx, id)
	if err != nil {
		return PrincipalView{}, err
	}
	var view PrincipalView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.disablePrincipalInTx(ctx, tx, sum, reason); err != nil {
			return err
		}
		view, err = s.principalView(ctx, tx, sum.PrincipalID)
		return err
	})
	if err != nil {
		return PrincipalView{}, err
	}
	return view, nil
}

// disablePrincipalInTx disables the principal in the caller's transaction,
// without authorization: sets principals.disabled_at, revokes every session of
// the principal with reason `principal_disabled` and observes `update` on the
// principal's resource. Disabling a disabled principal changes nothing. Disabling
// the last effective operator administrator is refused (ErrLastOperatorAdmin).
// The
// profile departure cleanup of a managed user's last profile uses it too.
func (s *UserService) disablePrincipalInTx(ctx context.Context, tx pgx.Tx, sum PrincipalSummary, reason string) error {
	if s.sessions == nil {
		return errors.New("users: no session service configured for principal administration")
	}
	// Lock order: platform_settings (inside the guard) before the principal row.
	// The guard refuses disabling the last effective operator administrator.
	if err := requireNotLastOperatorAdminForDisable(ctx, tx, sum.PrincipalID); err != nil {
		return err
	}
	cq := s.coreQuerier(tx)
	cur, err := cq.GetPrincipalByEntityID(ctx, sum.PrincipalID)
	if err != nil {
		return fmt.Errorf("users: load principal: %w", err)
	}
	if cur.DisabledAt != nil {
		return nil
	}
	var reasonText pgtype.Text
	var reasonPtr *string
	if reason != "" {
		reasonText = pgtype.Text{String: reason, Valid: true}
		reasonPtr = &reason
	}
	if _, err := cq.SetPrincipalDisabled(ctx, coredb.SetPrincipalDisabledParams{
		EntityID: sum.PrincipalID, DisabledReason: reasonText,
	}); err != nil {
		return fmt.Errorf("users: disable principal: %w", err)
	}
	if _, err := s.sessions.RevokePrincipalSessionsInTx(ctx, tx, sum.PrincipalID, SessionRevokePrincipalDisabled); err != nil {
		return err
	}
	return observeInTx(ctx, s.obs, tx, opUpdate, principalResource(sum.Kind), sum.PrincipalID,
		principalAudit(sum.PrincipalUUID, false, nil), principalAudit(sum.PrincipalUUID, true, reasonPtr))
}

// EnablePrincipal re-enables a disabled principal
// (POST /v1/principals/{uuid}/enable). It needs `update` on the principal and
// a fresh authentication. Enabling an enabled principal changes nothing. The
// sessions revoked by the disable stay revoked. Observation: `update` on
// `user` (`service_account` for a service account).
func (s *UserService) EnablePrincipal(ctx context.Context, id uuid.UUID) (PrincipalView, error) {
	sum, _, err := s.writablePrincipal(ctx, id)
	if err != nil {
		return PrincipalView{}, err
	}
	var view PrincipalView
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		cq := s.coreQuerier(tx)
		cur, err := cq.GetPrincipalByEntityID(ctx, sum.PrincipalID)
		if err != nil {
			return fmt.Errorf("users: load principal: %w", err)
		}
		if cur.DisabledAt != nil {
			if _, err := cq.ClearPrincipalDisabled(ctx, sum.PrincipalID); err != nil {
				return fmt.Errorf("users: enable principal: %w", err)
			}
			var reasonPtr *string
			if cur.DisabledReason.Valid {
				r := cur.DisabledReason.String
				reasonPtr = &r
			}
			if err := observeInTx(ctx, s.obs, tx, opUpdate, principalResource(sum.Kind), sum.PrincipalID,
				principalAudit(sum.PrincipalUUID, true, reasonPtr), principalAudit(sum.PrincipalUUID, false, nil)); err != nil {
				return err
			}
		}
		view, err = s.principalView(ctx, tx, sum.PrincipalID)
		return err
	})
	if err != nil {
		return PrincipalView{}, err
	}
	return view, nil
}
