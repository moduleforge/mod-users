package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	db "github.com/moduleforge/mod-users/model/db"
)

// localClaims extends the registered JWT claims with application-specific fields.
type localClaims struct {
	jwt.RegisteredClaims
	Roles        []string `json:"roles"`
	SudoUserUUID string   `json:"sudo_user_uuid,omitempty"`
	IsAnonymous  bool     `json:"is_anonymous,omitempty"`
}

// IssueLocalJWTWithSigner mints a JWT, signed by signer, for a locally-authenticated user account.
// The token is valid for 24 hours.
func IssueLocalJWTWithSigner(ua db.UserAccount, signer *Signer, issuer string) (string, error) {
	now := time.Now()
	claims := localClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   ua.Uuid.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Roles: []string{},
	}

	signed, err := signer.Sign(claims)
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign: %w", err)
	}
	return signed, nil
}

// IssueAnonymousJWTWithSigner mints a JWT, signed by signer, for an anonymous user account.
// The token carries is_anonymous=true and no email claim. The token is valid
// for 24 hours.
func IssueAnonymousJWTWithSigner(ua db.UserAccount, signer *Signer, issuer string) (string, error) {
	now := time.Now()
	claims := localClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   ua.Uuid.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Roles:       []string{},
		IsAnonymous: true,
	}

	signed, err := signer.Sign(claims)
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign anonymous: %w", err)
	}
	return signed, nil
}

// IssueAssumeJWTWithSigner mints a signer-signed JWT that carries sudo-user context for an admin.
func IssueAssumeJWTWithSigner(sudoUA db.UserAccount, actorUA db.UserAccount, signer *Signer, issuer string) (string, error) {
	now := time.Now()
	claims := localClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sudoUA.Uuid.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Roles:        []string{"admin"},
		SudoUserUUID: actorUA.Uuid.String(),
	}

	signed, err := signer.Sign(claims)
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign assume: %w", err)
	}
	return signed, nil
}

// IssueLocalJWT mints an HS256-signed JWT for a locally-authenticated user
// account, keyed by secret, with no "kid" header. It is the historical entry
// point; byte-for-byte equivalent to IssueLocalJWTWithSigner with
// NewHS256Signer(secret, "").
func IssueLocalJWT(ua db.UserAccount, secret, issuer string) (string, error) {
	return IssueLocalJWTWithSigner(ua, NewHS256Signer(secret, ""), issuer)
}

// IssueAnonymousJWT is the HS256/secret form of IssueAnonymousJWTWithSigner.
func IssueAnonymousJWT(ua db.UserAccount, secret, issuer string) (string, error) {
	return IssueAnonymousJWTWithSigner(ua, NewHS256Signer(secret, ""), issuer)
}

// IssueAssumeJWT is the HS256/secret form of IssueAssumeJWTWithSigner.
func IssueAssumeJWT(sudoUA db.UserAccount, actorUA db.UserAccount, secret, issuer string) (string, error) {
	return IssueAssumeJWTWithSigner(sudoUA, actorUA, NewHS256Signer(secret, ""), issuer)
}
