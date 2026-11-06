package auth

import (
	"net/http"
	"testing"
)

func TestGetBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		err    bool
	}{
		{"valid", "Bearer abc.def", "abc.def", false},
		{"extra whitespace", "Bearer   abc  ", "abc", false},
		{"missing", "", "", true},
		{"wrong scheme", "Basic abc", "", true},
		{"no token", "Bearer", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.header != "" {
				h.Set("Authorization", tt.header)
			}

			got, err := GetBearerToken(h)
			if (err != nil) != tt.err || got != tt.want {
				t.Fatalf("got (%q, %v), want (%q, err=%v)", got, err, tt.want, tt.err)
			}
		})
	}
}
