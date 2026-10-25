package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	usersdb "github.com/moduleforge/mod-users/model/db"
)

// fakeChallengeQ is an in-memory single-challenge store with the semantics of
// the real queries: open = unconsumed, attempts claimed by increment.
type fakeChallengeQ struct {
	usersdb.Querier
	mu       sync.Mutex
	row      usersdb.AuthChallenge
	hasRow   bool
	consumed int
	created  []usersdb.CreateChallengeWithCooldownParams
	cooldown bool
}

func newOpenChallenge(t *testing.T, kind, code string) *fakeChallengeQ {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeChallengeQ{hasRow: true, row: usersdb.AuthChallenge{ID: 1, Kind: kind, CodeHash: string(hash)}}
}

func (f *fakeChallengeQ) GetLatestOpenChallenge(context.Context, usersdb.GetLatestOpenChallengeParams) (usersdb.AuthChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasRow || f.row.ConsumedAt != nil {
		return usersdb.AuthChallenge{}, pgx.ErrNoRows
	}
	return f.row, nil
}

func (f *fakeChallengeQ) IncrementChallengeFailedAttempts(context.Context, int64) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.FailedAttempts++
	return f.row.FailedAttempts, nil
}

func (f *fakeChallengeQ) ConsumeChallenge(context.Context, int64) (usersdb.AuthChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row.ConsumedAt != nil {
		return usersdb.AuthChallenge{}, pgx.ErrNoRows
	}
	now := time.Now()
	f.row.ConsumedAt = &now
	f.consumed++
	return f.row, nil
}

func (f *fakeChallengeQ) CreateChallengeWithCooldown(_ context.Context, a usersdb.CreateChallengeWithCooldownParams) (usersdb.AuthChallenge, error) {
	f.created = append(f.created, a)
	if f.cooldown {
		return usersdb.AuthChallenge{}, pgx.ErrNoRows
	}
	return usersdb.AuthChallenge{ID: 2, Kind: a.Kind, CodeHash: a.CodeHash, ExpiresAt: a.ExpiresAt}, nil
}

func (f *fakeChallengeQ) ConsumeChallengeByCodeHash(_ context.Context, a usersdb.ConsumeChallengeByCodeHashParams) (usersdb.AuthChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasRow || f.row.ConsumedAt != nil || f.row.Kind != a.Kind || f.row.CodeHash != a.CodeHash {
		return usersdb.AuthChallenge{}, pgx.ErrNoRows
	}
	now := time.Now()
	f.row.ConsumedAt = &now
	return f.row, nil
}

func TestVerifyCode(t *testing.T) {
	t.Parallel()
	subj := Subject{PrincipalID: 5, IdentifierID: 6}

	t.Run("right code consumes the challenge once", func(t *testing.T) {
		t.Parallel()
		q := newOpenChallenge(t, ChallengeEmailCode, "123456")
		s := NewChallengeService(q, nil)
		ch, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"})
		if err != nil {
			t.Fatalf("VerifyCode: %v", err)
		}
		if ch.ConsumedAt == nil || q.consumed != 1 {
			t.Errorf("challenge not consumed: %+v consumed=%d", ch, q.consumed)
		}
		if _, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"}); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("replay: got %v, want ErrInvalidCode", err)
		}
	})

	t.Run("wrong codes spend the budget and exhaust the challenge", func(t *testing.T) {
		t.Parallel()
		q := newOpenChallenge(t, ChallengeEmailCode, "123456")
		s := NewChallengeService(q, nil)
		for i := 1; i <= MaxCodeAttempts; i++ {
			if _, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "000000"}); !errors.Is(err, ErrInvalidCode) {
				t.Fatalf("attempt %d: got %v, want ErrInvalidCode", i, err)
			}
		}
		if q.consumed != 1 {
			t.Fatalf("the final wrong guess must consume the challenge, consumed=%d", q.consumed)
		}
		// Even the right code fails once the challenge is exhausted.
		if _, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"}); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("after exhaustion: got %v, want ErrInvalidCode", err)
		}
	})

	t.Run("the right code on the last attempt still succeeds", func(t *testing.T) {
		t.Parallel()
		q := newOpenChallenge(t, ChallengeEmailCode, "123456")
		s := NewChallengeService(q, nil)
		for i := 1; i < MaxCodeAttempts; i++ {
			_, _ = s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "000000"})
		}
		if _, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"}); err != nil {
			t.Fatalf("got %v, want success on attempt %d", err, MaxCodeAttempts)
		}
	})

	t.Run("a spent counter refuses even the right code", func(t *testing.T) {
		t.Parallel()
		q := newOpenChallenge(t, ChallengeEmailCode, "123456")
		q.row.FailedAttempts = MaxCodeAttempts
		s := NewChallengeService(q, nil)
		if _, err := s.VerifyCode(context.Background(), VerifyCodeInput{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("got %v, want ErrInvalidCode", err)
		}
	})

	t.Run("no open challenge and bad kinds are indistinguishable", func(t *testing.T) {
		t.Parallel()
		s := NewChallengeService(&fakeChallengeQ{}, nil)
		for _, in := range []VerifyCodeInput{
			{Kind: ChallengeEmailCode, Subject: subj, Code: "123456"},
			{Kind: ChallengeLoginHandoff, Subject: subj, Code: "123456"},
			{Kind: ChallengeEmailCode, Subject: subj, Code: ""},
		} {
			if _, err := s.VerifyCode(context.Background(), in); !errors.Is(err, ErrInvalidCode) {
				t.Errorf("%+v: got %v, want ErrInvalidCode", in, err)
			}
		}
	})
}

func TestIssueCodeInTx(t *testing.T) {
	t.Parallel()

	t.Run("mints a six digit code through the cooldown function", func(t *testing.T) {
		t.Parallel()
		q := &fakeChallengeQ{}
		s := NewChallengeService(q, nil)
		s.newQ = func(pgx.Tx) usersdb.Querier { return q }
		got, err := s.IssueCodeInTx(context.Background(), nil, IssueCodeInput{
			Kind: ChallengeVerifyIdentifier, Subject: Subject{PrincipalID: 5, IdentifierID: 6},
		})
		if err != nil {
			t.Fatalf("IssueCodeInTx: %v", err)
		}
		if len(got.Code) != 6 {
			t.Errorf("code = %q, want 6 digits", got.Code)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(q.created[0].CodeHash), []byte(got.Code)); err != nil {
			t.Errorf("stored hash does not verify the code: %v", err)
		}
		if q.created[0].CodeHash == got.Code {
			t.Error("the code must be stored hashed")
		}
		if q.created[0].CooldownSecs != int32(CodeCooldown/time.Second) {
			t.Errorf("cooldown = %d", q.created[0].CooldownSecs)
		}
		if !q.created[0].PrincipalID.Valid || q.created[0].SessionID.Valid {
			t.Errorf("subject = %+v", q.created[0])
		}
	})

	t.Run("a cooldown hit is ErrChallengeCooldown", func(t *testing.T) {
		t.Parallel()
		q := &fakeChallengeQ{cooldown: true}
		s := NewChallengeService(q, nil)
		s.newQ = func(pgx.Tx) usersdb.Querier { return q }
		_, err := s.IssueCodeInTx(context.Background(), nil, IssueCodeInput{Kind: ChallengeEmailCode, Subject: Subject{PrincipalID: 5}})
		if !errors.Is(err, ErrChallengeCooldown) {
			t.Fatalf("got %v, want ErrChallengeCooldown", err)
		}
	})

	t.Run("token kinds are not codes", func(t *testing.T) {
		t.Parallel()
		s := NewChallengeService(&fakeChallengeQ{}, nil)
		if _, err := s.IssueCodeInTx(context.Background(), nil, IssueCodeInput{Kind: ChallengePasswordReset}); err == nil {
			t.Error("password_reset must not be issuable as a numeric code")
		}
	})
}

func TestConsumeTokenInTx(t *testing.T) {
	t.Parallel()
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	q := &fakeChallengeQ{hasRow: true, row: usersdb.AuthChallenge{ID: 3, Kind: ChallengeLoginHandoff, CodeHash: HashToken(token)}}
	s := NewChallengeService(q, nil)
	s.newQ = func(pgx.Tx) usersdb.Querier { return q }

	if _, err := s.ConsumeTokenInTx(context.Background(), nil, ChallengeLoginHandoff, "wrong"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("wrong token: got %v", err)
	}
	if _, err := s.ConsumeTokenInTx(context.Background(), nil, ChallengePasswordReset, token); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("wrong kind: got %v", err)
	}
	if _, err := s.ConsumeTokenInTx(context.Background(), nil, ChallengeLoginHandoff, token); err != nil {
		t.Fatalf("right token: %v", err)
	}
	if _, err := s.ConsumeTokenInTx(context.Background(), nil, ChallengeLoginHandoff, token); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replay: got %v, want ErrInvalidCode", err)
	}
}

func TestIssueTokenInTx_HandoffIsSixtySeconds(t *testing.T) {
	t.Parallel()
	var captured usersdb.CreateChallengeParams
	q := &handoffQ{onCreate: func(a usersdb.CreateChallengeParams) { captured = a }}
	s := NewChallengeService(q, nil)
	s.newQ = func(pgx.Tx) usersdb.Querier { return q }
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	got, err := s.IssueTokenInTx(context.Background(), nil, IssueTokenInput{
		Kind: ChallengeLoginHandoff, Subject: Subject{PrincipalID: 5, SessionID: 9}, TTL: time.Hour,
		BindingHash: testHandoffChallenge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !captured.BindingHash.Valid || captured.BindingHash.String != testHandoffChallenge {
		t.Errorf("binding_hash = %+v, want the verifier challenge", captured.BindingHash)
	}
	if want := fixed.Add(HandoffTTL); !captured.ExpiresAt.Time.Equal(want) {
		t.Errorf("expires_at = %v, want %v (a handoff lives 60 s whatever TTL says)", captured.ExpiresAt.Time, want)
	}
	if captured.CodeHash != HashToken(got.Code) || len(got.Code) != 64 {
		t.Errorf("token/hash mismatch: %q", got.Code)
	}
}

// testHandoffChallenge is the challenge of the RFC 7636 example verifier.
const testHandoffChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

func TestIssueTokenInTx_HandoffRequiresAValidChallenge(t *testing.T) {
	t.Parallel()
	q := &handoffQ{onCreate: func(usersdb.CreateChallengeParams) { t.Error("an unbound handoff was inserted") }}
	s := NewChallengeService(q, nil)
	s.newQ = func(pgx.Tx) usersdb.Querier { return q }
	for _, bad := range []string{"", "short", testHandoffChallenge + "="} {
		_, err := s.IssueTokenInTx(context.Background(), nil, IssueTokenInput{
			Kind: ChallengeLoginHandoff, Subject: Subject{PrincipalID: 5, SessionID: 9}, BindingHash: bad,
		})
		if err == nil {
			t.Errorf("challenge %q: want a refusal", bad)
		}
	}
}

type handoffQ struct {
	usersdb.Querier
	onCreate func(usersdb.CreateChallengeParams)
}

func (h *handoffQ) CreateChallenge(_ context.Context, a usersdb.CreateChallengeParams) (usersdb.AuthChallenge, error) {
	h.onCreate(a)
	return usersdb.AuthChallenge{ID: 1, Kind: a.Kind, ExpiresAt: a.ExpiresAt}, nil
}

type recordingSender struct{ to, subject, body string }

func (r *recordingSender) Send(_ context.Context, to, subject, body string) error {
	r.to, r.subject, r.body = to, subject, body
	return nil
}

func TestDeliverCode(t *testing.T) {
	t.Parallel()
	snd := &recordingSender{}
	s := NewChallengeService(nil, snd)
	if err := s.DeliverCode(context.Background(), "a@b.example", ChallengeVerifyIdentifier, "654321"); err != nil {
		t.Fatal(err)
	}
	if snd.to != "a@b.example" || snd.subject == "" || !strings.Contains(snd.body, "654321") {
		t.Errorf("sent = %+v", snd)
	}
	if err := NewChallengeService(nil, nil).DeliverCode(context.Background(), "a@b.example", ChallengeEmailCode, "1"); err == nil {
		t.Error("no sender must be an error")
	}
	if err := s.DeliverCode(context.Background(), "a@b.example", ChallengeLoginHandoff, "1"); err == nil {
		t.Error("a token kind must not be delivered as a code")
	}
}
