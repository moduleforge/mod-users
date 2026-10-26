package usersservice

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestSSHKeyNotAuthorizedParty(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	wrapped := fmt.Errorf("transport: %w", &SSHKeyNotAuthorizedError{PartyUUID: id})
	if !errors.Is(wrapped, ErrSSHKeyNotAuthorized) {
		t.Fatal("wrapped refusal does not satisfy ErrSSHKeyNotAuthorized")
	}
	if got, ok := SSHKeyNotAuthorizedParty(wrapped); !ok || got != id {
		t.Errorf("party = %s, %v; want %s, true", got, ok, id)
	}
	if _, ok := SSHKeyNotAuthorizedParty(&SSHKeyNotAuthorizedError{}); ok {
		t.Error("a refusal without a party reported one")
	}
	if _, ok := SSHKeyNotAuthorizedParty(errors.New("other")); ok {
		t.Error("an unrelated error reported a party")
	}
}
