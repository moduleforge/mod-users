package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// CodePolicyMismatchConfirmationRequired is the 409 detail code returned when a
// profile would join a party although its holder's current credentials cannot
// meet the party's authentication policy and the caller has not confirmed it.
const CodePolicyMismatchConfirmationRequired = "users.policy_mismatch_confirmation_required"

// Policy mismatch reasons, the lock reasons of mod_users.profile_availability_for
// that an outside collaborator's confirmation concerns.
const (
	MismatchCredentialNotAuthorized = "credential_not_authorized"
	MismatchProviderRequired        = "provider_required"
	MismatchACRTooLow               = "acr_too_low"
)

// PolicyMismatch describes why a holder's current credentials cannot meet a
// party's authentication policy (mod_users.holder_policy_mismatch).
type PolicyMismatch struct {
	// Reason is one of the Mismatch* constants: the first failing rule.
	Reason string
	// CredentialRule is the party's credential rule and RequiredProviderID its
	// required OIDC provider ("" unless the rule is `provider`).
	CredentialRule     string
	RequiredProviderID string
	// MinACR is the party's minimum acr; HolderACR the best acr the holder's
	// usable interactive credentials can reach (0 when they have none).
	MinACR, HolderACR int16
}

// detectPolicyMismatch reports whether holderID's current credentials fail the
// authentication policy of partyID. The rule itself lives in SQL
// (mod_users.holder_policy_mismatch) next to profile_availability_for, so the
// invitation check and the per-request gate cannot drift. It returns nil when
// the holder can meet the policy. inviteTime relaxes the 'authorized' credential
// rule to any usable interactive credential, since an invitee has no profile yet
// to authorize a credential for; the outside-collaborator path passes false, as
// a collaborator never gains a profile in the party and so needs an explicit
// authorization or a confirmation.
func detectPolicyMismatch(ctx context.Context, q usersdb.Querier, holderID, partyID int64, inviteTime bool) (*PolicyMismatch, error) {
	row, err := q.HolderPolicyMismatch(ctx, usersdb.HolderPolicyMismatchParams{HolderID: holderID, PartyID: partyID, InviteTime: inviteTime})
	if err != nil {
		return nil, fmt.Errorf("users: check party policy: %w", err)
	}
	if !row.Reason.Valid || row.Reason.String == "" {
		return nil, nil
	}
	return &PolicyMismatch{
		Reason:             row.Reason.String,
		CredentialRule:     row.CredentialRule.String,
		RequiredProviderID: row.RequiredProviderID.String,
		MinACR:             row.MinAcr.Int16,
		HolderACR:          row.HolderAcr.Int16,
	}, nil
}

// Conflict is the 409 returned when the mismatch is not confirmed. The first
// detail carries CodePolicyMismatchConfirmationRequired; the second names the
// failing rule, what the party requires and what the holder has.
func (m *PolicyMismatch) Conflict() error {
	rule := "credential_rule"
	var msg string
	switch m.Reason {
	case MismatchCredentialNotAuthorized:
		msg = "the party requires a credential authorized for it; the account has no usable credential that is"
	case MismatchProviderRequired:
		msg = fmt.Sprintf("the party requires an OIDC credential from provider %q; the account has none", m.RequiredProviderID)
	case MismatchACRTooLow:
		rule = "min_acr"
		if m.HolderACR == 0 {
			msg = fmt.Sprintf("the party requires assurance level %d; the account has no usable credential", m.MinACR)
		} else {
			msg = fmt.Sprintf("the party requires assurance level %d; the account's credentials reach at most %d", m.MinACR, m.HolderACR)
		}
	default:
		msg = "the account's credentials do not meet the party's authentication policy"
	}
	return apiresp.Conflict(
		apiresp.FieldError{
			Field: "confirm_policy_mismatch", Code: CodePolicyMismatchConfirmationRequired,
			Message: "the account's credentials do not meet the party's authentication policy; confirm to proceed",
		},
		apiresp.FieldError{Field: rule, Code: "users." + m.Reason, Message: msg},
	)
}

// recordPolicyConfirmation records, idempotently, that confirmedBy confirmed
// profileID's membership in partyID despite a policy mismatch. An existing
// confirmation is left unchanged (its confirmer and time stand).
func recordPolicyConfirmation(ctx context.Context, cq coredb.Querier, profileID, partyID, confirmedBy int64) error {
	_, err := cq.GetProfilePolicyConfirmation(ctx, coredb.GetProfilePolicyConfirmationParams{ProfileID: profileID, PartyID: partyID})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("users: read policy confirmation: %w", err)
	}
	if _, err := cq.UpsertProfilePolicyConfirmation(ctx, coredb.UpsertProfilePolicyConfirmationParams{
		ProfileID: profileID, PartyID: partyID, ConfirmedByProfileID: confirmedBy,
	}); err != nil {
		return fmt.Errorf("users: record policy confirmation: %w", err)
	}
	return nil
}
