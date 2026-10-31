//go:build integration

package authz_test

// challenges_integration_test.go proves mod_users.create_challenge_with_cooldown
// against a real Postgres, writing through SQL only: one row per subject per
// cooldown window, including under concurrency.

import (
	"context"
	"sync"
	"testing"
)

const challengeConcurrency = 16

// mintChallenge calls create_challenge_with_cooldown and reports whether a row
// was inserted.
func mintChallenge(ctx context.Context, kind string, principal, identifier *int64, hash string, cooldownSecs int) (bool, error) {
	rows, err := integPool.Query(ctx,
		`SELECT id FROM mod_users.create_challenge_with_cooldown($1, $2, $3, NULL, $4, now() + interval '5 minutes', $5)`,
		kind, principal, identifier, hash, cooldownSecs)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	inserted := false
	for rows.Next() {
		inserted = true
	}
	return inserted, rows.Err()
}

func challengeCount(t *testing.T, kind string, principal int64) int {
	t.Helper()
	var n int
	if err := integPool.QueryRow(context.Background(),
		`SELECT count(*) FROM mod_users.auth_challenges WHERE kind = $1 AND principal_id = $2`, kind, principal).Scan(&n); err != nil {
		t.Fatalf("count challenges: %v", err)
	}
	return n
}

func TestInteg_Cooldown_ConcurrentCallsYieldOneRow(t *testing.T) {
	u, _ := seedUserEntity(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	inserted, errs := 0, []error{}
	for i := 0; i < challengeConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := mintChallenge(context.Background(), "password_reset", &u, nil, fxTag("hash"), 60)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if ok {
				inserted++
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		t.Errorf("create_challenge_with_cooldown: %v", err)
	}
	if inserted != 1 {
		t.Errorf("concurrent calls inserted %d rows, want 1", inserted)
	}
	if n := challengeCount(t, "password_reset", u); n != 1 {
		t.Errorf("stored challenges = %d, want 1", n)
	}
}

func TestInteg_Cooldown_SubjectsAndWindow(t *testing.T) {
	ctx := context.Background()
	u, _ := seedUserEntity(t)
	v, _ := seedUserEntity(t)

	if ok, err := mintChallenge(ctx, "password_reset", &u, nil, fxTag("h"), 60); err != nil || !ok {
		t.Fatalf("first mint: inserted=%v err=%v, want inserted", ok, err)
	}
	if ok, err := mintChallenge(ctx, "password_reset", &u, nil, fxTag("h"), 60); err != nil || ok {
		t.Errorf("second mint inside the window: inserted=%v err=%v, want no row", ok, err)
	}
	// A consumed challenge still holds the cooldown.
	fxExec(t, integPool, "consume challenge",
		`UPDATE mod_users.auth_challenges SET consumed_at = now() WHERE kind = 'password_reset' AND principal_id = $1`, u)
	if ok, err := mintChallenge(ctx, "password_reset", &u, nil, fxTag("h"), 60); err != nil || ok {
		t.Errorf("mint after consumption inside the window: inserted=%v err=%v, want no row", ok, err)
	}
	// Another kind and another principal are separate subjects.
	if ok, err := mintChallenge(ctx, "verify_identifier", &u, nil, fxTag("h"), 60); err != nil || !ok {
		t.Errorf("other kind: inserted=%v err=%v, want inserted", ok, err)
	}
	if ok, err := mintChallenge(ctx, "password_reset", &v, nil, fxTag("h"), 60); err != nil || !ok {
		t.Errorf("other principal: inserted=%v err=%v, want inserted", ok, err)
	}
	// A zero-second window never blocks.
	if ok, err := mintChallenge(ctx, "password_reset", &u, nil, fxTag("h"), 0); err != nil || !ok {
		t.Errorf("zero-second window: inserted=%v err=%v, want inserted", ok, err)
	}
}
