package service

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
)

func validBootstrapInput() BootstrapInput {
	return BootstrapInput{
		Operator: BootstrapOperator{Kind: BootstrapOperatorOrganization, LegalName: "Operator Inc", LegalForm: "llc"},
		Admin:    BootstrapAdmin{Email: "admin@example.test", Password: "correct-horse-battery", GivenName: "Ada", FamilyName: "Admin"},
	}
}

func TestValidateBootstrapInput(t *testing.T) {
	if err := ValidateBootstrapInput(validBootstrapInput()); err != nil {
		t.Fatalf("valid organization input: %v", err)
	}
	np := validBootstrapInput()
	np.Operator = BootstrapOperator{Kind: BootstrapOperatorNaturalPerson}
	if err := ValidateBootstrapInput(np); err != nil {
		t.Fatalf("valid natural_person input: %v", err)
	}

	cases := map[string]func(*BootstrapInput){
		"unknown kind":       func(in *BootstrapInput) { in.Operator.Kind = "guild" },
		"missing legal name": func(in *BootstrapInput) { in.Operator.LegalName = " " },
		"bad legal form":     func(in *BootstrapInput) { in.Operator.LegalForm = "guild" },
		"missing email":      func(in *BootstrapInput) { in.Admin.Email = "" },
		"bad email":          func(in *BootstrapInput) { in.Admin.Email = "nope" },
		"short password":     func(in *BootstrapInput) { in.Admin.Password = "short" },
		"missing given":      func(in *BootstrapInput) { in.Admin.GivenName = "" },
		"missing family":     func(in *BootstrapInput) { in.Admin.FamilyName = "" },
	}
	for name, mutate := range cases {
		in := validBootstrapInput()
		mutate(&in)
		if err := ValidateBootstrapInput(in); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
}

// TestIsOperatorAdmin_RequiresSession: without a non-impersonating session
// acting as a profile the check answers false before touching the database.
func TestIsOperatorAdmin_RequiresSession(t *testing.T) {
	s := NewBootstrapService(nil, nil, nil, nil, nil, nil, nil)
	for name, ctx := range map[string]context.Context{
		"no auth context":    context.Background(),
		"anonymous":          opctx.WithAuthContext(context.Background(), opctx.AuthContext{Anonymous: true, ProfileID: 1, PrincipalID: 1}),
		"client credentials": opctx.WithAuthContext(context.Background(), opctx.AuthContext{ProfileID: 1, PrincipalID: 1, CredentialID: 3}),
		"impersonating":      opctx.WithAuthContext(context.Background(), opctx.AuthContext{ProfileID: 1, PrincipalID: 1, SessionID: 2, ImpersonatorPrincipalID: 9}),
		"no acting profile":  opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, SessionID: 2}),
	} {
		ok, err := s.IsOperatorAdmin(ctx)
		if ok || err != nil {
			t.Errorf("%s: IsOperatorAdmin = %v, %v; want false, nil", name, ok, err)
		}
	}
}
