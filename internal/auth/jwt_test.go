package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJWT(t *testing.T) {
	id := uuid.New()
	secret := "secret"

	valid, err := MakeJWT(id, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	expired, err := MakeJWT(id, secret, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		token  string
		secret string
		fail   bool
	}{
		{"valid", valid, secret, false},
		{"expired", expired, secret, true},
		{"wrong", valid, "other", true},
		{"garbage", "not.a.token", secret, true},
		{"empty", "", secret, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ValidateJWT(c.token, c.secret)
			if c.fail {
				if err == nil {
					t.Fatal("expected error")
				}
				if got != uuid.Nil {
					t.Fatalf("expected nil uuid, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != id {
				t.Fatalf("got %v, want %v", got, id)
			}
		})
	}
}
