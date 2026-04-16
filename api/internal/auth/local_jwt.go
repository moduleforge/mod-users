package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	db "github.com/moduleforge/users-module/model/db"
)

// localClaims extends the registered JWT claims with application-specific fields.
type localClaims struct {
	jwt.RegisteredClaims
	Roles          []string `json:"roles"`
	AssumedUserUUID string  `json:"assumed_user_uuid,omitempty"`
}

// IssueLocalJWT mints an HS256-signed JWT for a locally-authenticated user.
// The token is valid for 24 hours.
func IssueLocalJWT(user db.User, isAdmin bool, secret, issuer string) (string, error) {
	roles := []string{}
	if isAdmin {
		roles = append(roles, "admin")
	}

	now := time.Now()
	claims := localClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.Uuid.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Roles: roles,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign: %w", err)
	}
	return signed, nil
}

// IssueAssumeJWT mints a JWT that carries assumed-user context for an admin.
func IssueAssumeJWT(adminUser db.User, assumedUser db.User, secret, issuer string) (string, error) {
	now := time.Now()
	claims := localClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   adminUser.Uuid.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		Roles:           []string{"admin"},
		AssumedUserUUID: assumedUser.Uuid.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("local_jwt: sign assume: %w", err)
	}
	return signed, nil
}
