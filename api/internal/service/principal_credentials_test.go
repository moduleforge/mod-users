package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
)

// An impersonating session is refused on every credential-administration
// method before any lookup, so a service with no collaborators is enough.
func TestPrincipalCredentials_ImpersonationRefusedBeforeAnyLookup(t *testing.T) {
	t.Parallel()
	imp := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: 1, ProfileID: 2, SessionID: 3, ImpersonatorPrincipalID: 9,
	})
	anon := context.Background()
	id := uuid.New()
	s := &UserService{}

	if _, err := s.credentialsTarget(imp, id); !errors.Is(err, ErrImpersonationNotAllowed) {
		t.Errorf("credentialsTarget err = %v, want ErrImpersonationNotAllowed", err)
	}
	if _, err := s.credentialsTarget(anon, id); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Errorf("credentialsTarget without an actor err = %v, want ErrUnauthenticated", err)
	}
	if _, err := s.ListPrincipalCredentials(imp, id); !errors.Is(err, ErrImpersonationNotAllowed) {
		t.Errorf("ListPrincipalCredentials err = %v, want ErrImpersonationNotAllowed", err)
	}
	if err := s.RevokePrincipalCredential(imp, id, uuid.New()); !errors.Is(err, ErrImpersonationNotAllowed) {
		t.Errorf("RevokePrincipalCredential err = %v, want ErrImpersonationNotAllowed", err)
	}
}
