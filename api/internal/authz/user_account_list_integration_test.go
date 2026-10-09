//go:build integration

package authz_test

// user_account_list_integration_test.go wires the real UserAccountService to
// the real grants-table Authorizer and a real db.Querier, and proves that List
// is authorized by a wildcard grant or by a list (or manage) grant on
// natural_person's type entity, and by nothing else. It also pins the
// documented exposure: an authorized type-level holder receives every
// account, with its email.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/types"
	"github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

func newIntegListService(t *testing.T) *service.UserAccountService {
	t.Helper()
	typeRes := types.NewFromMap(map[string]int64{"natural_person": typeIDForSlug(t, "natural_person")})
	return service.NewUserAccountService(nil, db.New(integPool), nil, integAZ, &observer.ObserverGroup{}, nil, typeRes, nil)
}

func listAllAccounts(ctx context.Context, svc *service.UserAccountService) ([]service.UserAccount, error) {
	return svc.List(ctx, service.ListUserAccountsInput{Limit: 200})
}

func requireListForbidden(t *testing.T, actor int64, why string) {
	t.Helper()
	rows, err := listAllAccounts(actorCtx(actor), newIntegListService(t))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("List (%s): got %v, want ErrForbidden", why, err)
	}
	if len(rows) != 0 {
		t.Errorf("List (%s): leaked %d rows on denial", why, len(rows))
	}
}

// requireListSeesEmails asserts List succeeds and returns every email in want.
func requireListSeesEmails(t *testing.T, actor int64, why string, want ...string) {
	t.Helper()
	rows, err := listAllAccounts(actorCtx(actor), newIntegListService(t))
	if err != nil {
		t.Errorf("List (%s): got %v, want nil", why, err)
		return
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Email != nil {
			seen[*r.Email] = true
		}
	}
	for _, e := range want {
		if !seen[e] {
			t.Errorf("List (%s): result does not contain %q; got %d rows", why, e, len(rows))
		}
	}
}

// seedCorporationAccount inserts a user_accounts row whose account_holder is a
// corporation (entity -> legal_entity -> corporation -> user_account) and
// returns the corporation's entity ID.
func seedCorporationAccount(t *testing.T, legalName, email string) int64 {
	t.Helper()
	corpID := seedUnownedCorporation(t, legalName)
	const uaSQL = `INSERT INTO user_accounts (account_holder, email) VALUES ($1, $2)`
	if _, err := integPool.Exec(context.Background(), uaSQL, corpID, email); err != nil {
		t.Fatalf("seedCorporationAccount: insert user_account: %v", err)
	}
	return corpID
}

func TestInteg_UserAccountService_List_TypeLevelAuthorization(t *testing.T) {
	npEnt := typeEntityIDForSlug(t, "natural_person")

	// Other users whose emails the result must expose to a type-level holder.
	otherA := seedUser(t, "typelist-other-a@example.com", false)
	seedUser(t, "typelist-other-b@example.com", false)
	// An account held by a corporation is exposed too: the search does not
	// filter by holder type.
	seedCorporationAccount(t, "Typelist Corp", "typelist-corp-held@example.com")
	emails := []string{"typelist-other-a@example.com", "typelist-other-b@example.com", "typelist-corp-held@example.com"}

	t.Run("list on natural_person type entity", func(t *testing.T) {
		u := seedUser(t, "typelist-direct-list@example.com", false)
		targetedGrant(t, u, npEnt, "list")
		requireListSeesEmails(t, u, "direct list grant", emails...)
	})

	t.Run("manage on natural_person type entity", func(t *testing.T) {
		u := seedUser(t, "typelist-direct-manage@example.com", false)
		targetedGrant(t, u, npEnt, "manage")
		requireListSeesEmails(t, u, "direct manage grant", emails...)
	})

	t.Run("list through type-only target group and actor group", func(t *testing.T) {
		u := seedUser(t, "typelist-group@example.com", false)
		actorGroup := seedActorGroup(t, "typelist-actors")
		addActorGroupMember(t, actorGroup, u)
		targetGroup := seedTargetGroup(t, "typelist-targets")
		addTargetGroupMember(t, targetGroup, npEnt)
		targetedGrant(t, actorGroup, targetGroup, "list")
		requireListSeesEmails(t, u, "grant through groups", emails...)
	})

	t.Run("create only", func(t *testing.T) {
		u := seedUser(t, "typelist-create-only@example.com", false)
		targetedGrant(t, u, npEnt, "create")
		requireListForbidden(t, u, "create does not imply list")
	})

	t.Run("list on legal_entity type entity", func(t *testing.T) {
		u := seedUser(t, "typelist-legal@example.com", false)
		targetedGrant(t, u, typeEntityIDForSlug(t, "legal_entity"), "list")
		requireListForbidden(t, u, "exact-type: legal_entity does not cover natural_person")
	})

	t.Run("list on corporation type entity", func(t *testing.T) {
		u := seedUser(t, "typelist-corp@example.com", false)
		targetedGrant(t, u, typeEntityIDForSlug(t, "corporation"), "list")
		requireListForbidden(t, u, "exact-type: corporation does not cover natural_person")
	})

	t.Run("list and read on another user's instance", func(t *testing.T) {
		u := seedUser(t, "typelist-instance@example.com", false)
		targetedGrant(t, u, otherA, "list")
		targetedGrant(t, u, otherA, "read")
		requireListForbidden(t, u, "instance grants do not satisfy the type-level check")
	})

	t.Run("ordinary user with no grants", func(t *testing.T) {
		u := seedUser(t, "typelist-ordinary@example.com", false)
		requireListForbidden(t, u, "owns only their own entity")
	})

	t.Run("wildcard manage", func(t *testing.T) {
		u := seedUser(t, "typelist-wildcard@example.com", true)
		requireListSeesEmails(t, u, "wildcard manage", emails...)
	})
}
