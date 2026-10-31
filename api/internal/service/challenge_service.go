package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/email"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Challenge kinds (mod_users.auth_challenges.kind).
const (
	ChallengeEmailCode        = "email_code"
	ChallengeVerifyIdentifier = "verify_identifier"
	ChallengePasswordReset    = "password_reset"
	ChallengeStepUp           = "step_up"
	ChallengeLoginHandoff     = "login_handoff"
)

// Challenge parameters.
const (
	// MaxCodeAttempts bounds the guesses allowed against one numeric code.
	MaxCodeAttempts = 5
	// CodeCooldown is the minimum interval between minted codes for the same
	// subject (kind, principal, identifier, session), measured from the newest
	// challenge in any state.
	CodeCooldown = 60 * time.Second
	// CodeTTL is the default lifetime of an emailed numeric code.
	CodeTTL = 5 * time.Minute
	// VerifyIdentifierTTL is the lifetime of a verify_identifier code.
	VerifyIdentifierTTL = 15 * time.Minute
	// PasswordResetTTL is the lifetime of a password_reset token.
	PasswordResetTTL = time.Hour
	// HandoffTTL is the lifetime of a login_handoff code (decision U7).
	HandoffTTL = 60 * time.Second
)

// codeKinds are the kinds issued as low-entropy numeric codes (bcrypt-hashed,
// attempt-bounded); tokenKinds are issued as high-entropy opaque tokens
// (SHA-256-hashed, looked up by hash).
var (
	codeKinds  = map[string]bool{ChallengeEmailCode: true, ChallengeVerifyIdentifier: true, ChallengeStepUp: true}
	tokenKinds = map[string]bool{ChallengePasswordReset: true, ChallengeLoginHandoff: true}
)

// Challenge is the service-layer view of an auth_challenges row. Ids of
// related rows are 0 when the column is NULL.
type Challenge struct {
	ID           int64
	UUID         uuid.UUID
	Kind         string
	PrincipalID  int64
	IdentifierID int64
	CredentialID int64
	SessionID    int64
	// BindingHash is the verifier challenge of a login_handoff; "" for any other
	// kind.
	BindingHash    string
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
	FailedAttempts int32
}

func toChallenge(r usersdb.AuthChallenge) Challenge {
	return Challenge{
		ID: r.ID, UUID: r.Uuid, Kind: r.Kind,
		PrincipalID: r.PrincipalID.Int64, IdentifierID: r.IdentifierID.Int64,
		CredentialID: r.CredentialID.Int64, SessionID: r.SessionID.Int64,
		BindingHash: r.BindingHash.String,
		ExpiresAt:   r.ExpiresAt.Time, ConsumedAt: r.ConsumedAt, FailedAttempts: r.FailedAttempts,
	}
}

// IssuedChallenge is a freshly created challenge with its secret. Code is the
// six-digit code (IssueCodeInTx) or the opaque token (IssueTokenInTx); it is
// returned only now, the database keeps its hash.
type IssuedChallenge struct {
	Challenge Challenge
	Code      string
}

// ChallengeService creates, verifies and consumes one-time challenges: emailed
// numeric codes with a failed-attempt bound, and single-use opaque tokens.
// Challenges are not credentials and are not observed.
type ChallengeService struct {
	q      usersdb.Querier
	sender email.Sender
	newQ   func(pgx.Tx) usersdb.Querier
	now    func() time.Time
}

// NewChallengeService constructs a ChallengeService. q must be backed by the
// connection pool, not a transaction: VerifyCode commits its attempt counter
// independently of any caller transaction. sender delivers emailed codes and
// may be nil when only tokens are used.
func NewChallengeService(q usersdb.Querier, sender email.Sender) *ChallengeService {
	return &ChallengeService{q: q, sender: sender, now: time.Now}
}

func (s *ChallengeService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

// Subject identifies what a challenge is about. A zero id is NULL.
type Subject struct {
	PrincipalID  int64
	IdentifierID int64
	SessionID    int64
}

func nullIfZero(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// IssueCodeInput is the input of IssueCodeInTx.
type IssueCodeInput struct {
	Kind    string // email_code, verify_identifier or step_up
	Subject Subject
	// TTL is the code lifetime; zero means the kind's default.
	TTL time.Duration
	// Cooldown is the minimum interval since the subject's newest challenge;
	// zero means CodeCooldown.
	Cooldown time.Duration
}

func defaultCodeTTL(kind string) time.Duration {
	if kind == ChallengeVerifyIdentifier {
		return VerifyIdentifierTTL
	}
	return CodeTTL
}

// IssueCodeInTx mints a six-digit code for the subject through
// mod_users.create_challenge_with_cooldown (atomic per subject). A challenge of
// the same subject created within the cooldown window, in any state, yields
// ErrChallengeCooldown and no row, so exhausting a code cannot reset its guess
// budget by minting a fresh one early. The caller delivers IssuedChallenge.Code
// (DeliverCode) after the transaction commits.
func (s *ChallengeService) IssueCodeInTx(ctx context.Context, tx pgx.Tx, in IssueCodeInput) (IssuedChallenge, error) {
	if !codeKinds[in.Kind] {
		return IssuedChallenge{}, invalidField("kind", "users.challenge_kind_invalid", "unsupported challenge kind")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = defaultCodeTTL(in.Kind)
	}
	cooldown := in.Cooldown
	if cooldown <= 0 {
		cooldown = CodeCooldown
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return IssuedChallenge{}, fmt.Errorf("challenges: generate code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return IssuedChallenge{}, fmt.Errorf("challenges: hash code: %w", err)
	}

	row, err := s.usersQ(tx).CreateChallengeWithCooldown(ctx, usersdb.CreateChallengeWithCooldownParams{
		Kind:         in.Kind,
		PrincipalID:  nullableInt8(nullIfZero(in.Subject.PrincipalID)),
		IdentifierID: nullableInt8(nullIfZero(in.Subject.IdentifierID)),
		SessionID:    nullableInt8(nullIfZero(in.Subject.SessionID)),
		CodeHash:     string(hash),
		ExpiresAt:    timestamptz(s.now().Add(ttl)),
		CooldownSecs: int32(cooldown / time.Second),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IssuedChallenge{}, ErrChallengeCooldown
		}
		return IssuedChallenge{}, fmt.Errorf("challenges: create challenge: %w", err)
	}
	return IssuedChallenge{Challenge: toChallenge(row), Code: code}, nil
}

// IssueTokenInput is the input of IssueTokenInTx.
type IssueTokenInput struct {
	Kind    string // password_reset or login_handoff
	Subject Subject
	// TTL is the token lifetime. A login_handoff token always lives HandoffTTL
	// (60 s, decision U7) whatever TTL says; zero means PasswordResetTTL for a
	// password_reset.
	TTL time.Duration
	// BindingHash is the browser's handoff challenge (base64url SHA-256 of its
	// verifier). Required for, and only used by, a login_handoff token.
	BindingHash string
}

// IssueTokenInTx mints a 256-bit opaque token. A login_handoff token is
// single use and lives 60 s; it is created without a cooldown (one per OIDC
// callback) and carries in.BindingHash, the challenge its redeemer must answer
// with a verifier (ExchangeHandoff); a login_handoff without a valid challenge
// is refused. A password_reset token goes through the cooldown function with
// CodeCooldown (ErrChallengeCooldown inside the window).
func (s *ChallengeService) IssueTokenInTx(ctx context.Context, tx pgx.Tx, in IssueTokenInput) (IssuedChallenge, error) {
	if !tokenKinds[in.Kind] {
		return IssuedChallenge{}, invalidField("kind", "users.challenge_kind_invalid", "unsupported challenge kind")
	}
	if in.Kind == ChallengeLoginHandoff && !localauth.ValidHandoffChallenge(in.BindingHash) {
		return IssuedChallenge{}, invalidField("binding_hash", "users.handoff_challenge_invalid", "a handoff requires a valid challenge")
	}
	token, err := randomHex(32)
	if err != nil {
		return IssuedChallenge{}, fmt.Errorf("challenges: generate token: %w", err)
	}
	q := s.usersQ(tx)
	hash := HashToken(token)

	var row usersdb.AuthChallenge
	if in.Kind == ChallengeLoginHandoff {
		row, err = q.CreateChallenge(ctx, usersdb.CreateChallengeParams{
			Kind:         in.Kind,
			PrincipalID:  nullableInt8(nullIfZero(in.Subject.PrincipalID)),
			IdentifierID: nullableInt8(nullIfZero(in.Subject.IdentifierID)),
			SessionID:    nullableInt8(nullIfZero(in.Subject.SessionID)),
			CodeHash:     hash,
			ExpiresAt:    timestamptz(s.now().Add(HandoffTTL)),
			BindingHash:  pgtype.Text{String: in.BindingHash, Valid: true},
		})
	} else {
		ttl := in.TTL
		if ttl <= 0 {
			ttl = PasswordResetTTL
		}
		row, err = q.CreateChallengeWithCooldown(ctx, usersdb.CreateChallengeWithCooldownParams{
			Kind:         in.Kind,
			PrincipalID:  nullableInt8(nullIfZero(in.Subject.PrincipalID)),
			IdentifierID: nullableInt8(nullIfZero(in.Subject.IdentifierID)),
			SessionID:    nullableInt8(nullIfZero(in.Subject.SessionID)),
			CodeHash:     hash,
			ExpiresAt:    timestamptz(s.now().Add(ttl)),
			CooldownSecs: int32(CodeCooldown / time.Second),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return IssuedChallenge{}, ErrChallengeCooldown
		}
	}
	if err != nil {
		return IssuedChallenge{}, fmt.Errorf("challenges: create challenge: %w", err)
	}
	return IssuedChallenge{Challenge: toChallenge(row), Code: token}, nil
}

// ConsumeTokenInTx atomically finds and consumes the open challenge of kind
// whose token hashes to HashToken(token). It is single use: a replay, an
// expired token and an unknown token are all ErrInvalidCode. Because the
// consumption is part of the caller's transaction, a failure after it rolls
// the token back (tokens are unguessable, so no attempt counter is lost).
func (s *ChallengeService) ConsumeTokenInTx(ctx context.Context, tx pgx.Tx, kind, token string) (Challenge, error) {
	if !tokenKinds[kind] || token == "" {
		return Challenge{}, ErrInvalidCode
	}
	row, err := s.usersQ(tx).ConsumeChallengeByCodeHash(ctx, usersdb.ConsumeChallengeByCodeHashParams{
		Kind:     kind,
		CodeHash: HashToken(token),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Challenge{}, ErrInvalidCode
		}
		return Challenge{}, fmt.Errorf("challenges: consume token: %w", err)
	}
	return toChallenge(row), nil
}

// VerifyCodeInput is the input of VerifyCode.
type VerifyCodeInput struct {
	Kind    string
	Subject Subject
	Code    string
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// burnCompare spends the bcrypt time of a real comparison, so a missing,
// exhausted or expired challenge is indistinguishable by timing from a wrong
// guess.
func burnCompare(code string) {
	dummyHashOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-challenge-code"), bcrypt.DefaultCost)
	})
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(code))
}

// VerifyCode checks a numeric code against the newest open challenge of the
// subject and consumes the challenge once. Every failure (no open challenge,
// wrong code, attempt budget spent, concurrent consumption) is ErrInvalidCode.
//
// An attempt is claimed atomically before the comparison
// (IncrementChallengeFailedAttempts), so concurrent guesses cannot exceed
// MaxCodeAttempts; the final wrong guess consumes the challenge. The counter
// and the consumption commit on their own (the service's pool-backed querier),
// deliberately independent of any transaction the caller later opens: a failed
// verification must keep its attempt even when the caller's transaction rolls
// back. A successful attempt also counts toward the counter.
func (s *ChallengeService) VerifyCode(ctx context.Context, in VerifyCodeInput) (Challenge, error) {
	if !codeKinds[in.Kind] || in.Code == "" {
		burnCompare(in.Code)
		return Challenge{}, ErrInvalidCode
	}
	open, err := s.q.GetLatestOpenChallenge(ctx, usersdb.GetLatestOpenChallengeParams{
		Kind:         in.Kind,
		PrincipalID:  nullableInt8(nullIfZero(in.Subject.PrincipalID)),
		IdentifierID: nullableInt8(nullIfZero(in.Subject.IdentifierID)),
		SessionID:    nullableInt8(nullIfZero(in.Subject.SessionID)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnCompare(in.Code)
			return Challenge{}, ErrInvalidCode
		}
		return Challenge{}, fmt.Errorf("challenges: look up challenge: %w", err)
	}

	attempts, err := s.q.IncrementChallengeFailedAttempts(ctx, open.ID)
	if err != nil {
		return Challenge{}, fmt.Errorf("challenges: claim attempt: %w", err)
	}
	if attempts > MaxCodeAttempts {
		burnCompare(in.Code)
		return Challenge{}, ErrInvalidCode
	}

	if err := bcrypt.CompareHashAndPassword([]byte(open.CodeHash), []byte(in.Code)); err != nil {
		if attempts >= MaxCodeAttempts {
			if _, cerr := s.q.ConsumeChallenge(ctx, open.ID); cerr != nil && !errors.Is(cerr, pgx.ErrNoRows) {
				return Challenge{}, fmt.Errorf("challenges: exhaust challenge: %w", cerr)
			}
		}
		return Challenge{}, ErrInvalidCode
	}

	consumed, err := s.q.ConsumeChallenge(ctx, open.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Challenge{}, ErrInvalidCode // consumed concurrently
		}
		return Challenge{}, fmt.Errorf("challenges: consume challenge: %w", err)
	}
	return toChallenge(consumed), nil
}

// DeliverCode emails a numeric code. Call it after the transaction that
// created the challenge commits. The recipient is not echoed in errors.
func (s *ChallengeService) DeliverCode(ctx context.Context, to, kind, code string) error {
	if s.sender == nil {
		return errors.New("challenges: no email sender configured")
	}
	if !codeKinds[kind] {
		return fmt.Errorf("challenges: kind %q is not delivered as a code", kind)
	}
	subject := "Your login code"
	ttl := CodeTTL
	switch kind {
	case ChallengeVerifyIdentifier:
		subject = "Verify your email address"
		ttl = VerifyIdentifierTTL
	case ChallengeStepUp:
		subject = "Your confirmation code"
	}
	body := fmt.Sprintf("Your verification code is: %s\n\nThis code expires in %d minutes.", code, int(ttl/time.Minute))
	if err := s.sender.Send(ctx, to, subject, body); err != nil {
		return fmt.Errorf("challenges: send code: %w", err)
	}
	return nil
}

// DeleteExpired drops challenges that expired before cutoff and reports how
// many (housekeeping).
func (s *ChallengeService) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteExpiredChallenges(ctx, timestamptz(cutoff))
	if err != nil {
		return 0, fmt.Errorf("challenges: delete expired: %w", err)
	}
	return n, nil
}
