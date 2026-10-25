package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
)

func TestValidatePolicyInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		in       PutPartyAuthnPolicyInput
		wantProv string
		wantCode string
	}{
		{"any", PutPartyAuthnPolicyInput{CredentialRule: "any", MinACR: 1}, "", ""},
		{"authorized with acr 3", PutPartyAuthnPolicyInput{CredentialRule: "authorized", MinACR: 3}, "", ""},
		{"provider", PutPartyAuthnPolicyInput{CredentialRule: "provider", RequiredOIDCProviderID: "corp", MinACR: 2}, "corp", ""},
		{"no rule", PutPartyAuthnPolicyInput{MinACR: 1}, "", "users.credential_rule_required"},
		{"unknown rule", PutPartyAuthnPolicyInput{CredentialRule: "sometimes", MinACR: 1}, "", "users.credential_rule_invalid"},
		{"acr 0", PutPartyAuthnPolicyInput{CredentialRule: "any"}, "", "users.min_acr_invalid"},
		{"acr 4", PutPartyAuthnPolicyInput{CredentialRule: "any", MinACR: 4}, "", "users.min_acr_invalid"},
		{"provider rule without provider", PutPartyAuthnPolicyInput{CredentialRule: "provider", MinACR: 1}, "", "users.provider_required"},
		{"provider without the provider rule", PutPartyAuthnPolicyInput{CredentialRule: "authorized", RequiredOIDCProviderID: "corp", MinACR: 1}, "", "users.provider_unexpected"},
		{"provider id too long", PutPartyAuthnPolicyInput{CredentialRule: "provider", RequiredOIDCProviderID: strings.Repeat("p", 101), MinACR: 1}, "", "users.provider_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validatePolicyInput(tc.in)
			if tc.wantCode == "" {
				if err != nil || got.String != tc.wantProv || got.Valid != (tc.wantProv != "") {
					t.Fatalf("validatePolicyInput = %+v, %v; want provider %q", got, err, tc.wantProv)
				}
				return
			}
			if !errors.Is(err, apiresp.ErrInvalidInput) || !strings.Contains(strings.Join(fieldCodes(err), ","), tc.wantCode) {
				t.Fatalf("err = %v (codes %v), want %s", err, fieldCodes(err), tc.wantCode)
			}
		})
	}
}

func TestWriteCaller(t *testing.T) {
	t.Parallel()
	if _, err := writeCaller(context.Background()); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Errorf("no auth context: %v, want unauthenticated", err)
	}
	ok := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, ProfileID: 2})
	if _, err := writeCaller(ok); err != nil {
		t.Errorf("plain caller: %v", err)
	}
	imp := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, ProfileID: 2, ImpersonatorPrincipalID: 9, SessionID: 3})
	if _, err := writeCaller(imp); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("impersonating caller: %v, want forbidden", err)
	}
}
