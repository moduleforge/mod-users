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

// IssueLocalJWT mints an HS256-signed JWT for a locally-authenticated user account.
// The token is valid for 24 hours.
func IssueLocalJWT(ua db.UserAccount, secret, issuer string) (string, error) {
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

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign: %w", err)
	}
	return signed, nil
}

// IssueAnonymousJWT mints an HS256-signed JWT for an anonymous user account.
// The token carries is_anonymous=true and no email claim. The token is valid
// for 24 hours.
func IssueAnonymousJWT(ua db.UserAccount, secret, issuer string) (string, error) {
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

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign anonymous: %w", err)
	}
	return signed, nil
}

// IssueAssumeJWT mints a JWT that carries sudo-user context for an admin.
func IssueAssumeJWT(sudoUA db.UserAccount, actorUA db.UserAccount, secret, issuer string) (string, error) {
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

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign assume: %w", err)
	}
	return signed, nil
}
