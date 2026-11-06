package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const issuer = "chirpy-access"

func MakeJWT(subject uuid.UUID, secret string, lifetime time.Duration) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   subject.String(),
			IssuedAt:  jwt.NewNumericDate(now.UTC()),
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime).UTC()),
		},
	)

	return token.SignedString([]byte(secret))
}

func ValidateJWT(token, secret string) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims
	tok, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(issuer),
	)
	if err != nil {
		return uuid.Nil, err
	}

	s, err := tok.Claims.GetSubject()
	if err != nil {
		return uuid.Nil, err
	}

	return uuid.Parse(s)
}
