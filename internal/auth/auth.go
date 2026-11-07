package auth

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"crypto/rand"

	"github.com/alexedwards/argon2id"
)

func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

func CheckPasswordHash(password, hash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(password, hash)
}

func GetBearerToken(headers http.Header) (string, error) {
	h := strings.TrimSpace(headers.Get("Authorization"))
	if h == "" {
		return "", errors.New("missing authorization header")
	}

	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", errors.New("malformed authorization header")
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("missing bearer token")
	}

	return token, nil
}

func MakeRefreshToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)

	return hex.EncodeToString(buf)
}

func GetAPIKey(headers http.Header) (string, error) {
	h := strings.TrimSpace(headers.Get("Authorization"))
	if h == "" {
		return "", errors.New("missing authorization header")
	}

	scheme, key, ok := strings.Cut(h, " ")
	if !ok || scheme != "ApiKey" {
		return "", errors.New("malformed authorization header")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("missing api key")
	}

	return key, nil
}
