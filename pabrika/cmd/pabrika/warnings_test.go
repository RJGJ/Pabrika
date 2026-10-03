package main

import (
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/config"
)

func TestConfigWarnings(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want []string // substrings, one per expected warning
	}{
		{"default local dev", config.Config{BaseURL: "http://localhost:8080", CookieSecure: true}, nil},
		{"insecure cookie on localhost", config.Config{BaseURL: "http://localhost:5173"}, nil},
		{"127.0.0.1 secure cookie", config.Config{BaseURL: "http://127.0.0.1:8080", CookieSecure: true}, nil},
		{"app.localhost", config.Config{BaseURL: "http://pabrika.localhost:8080", CookieSecure: true}, nil},
		{"proper https", config.Config{BaseURL: "https://board.example.com", CookieSecure: true}, nil},
		{"http remote with secure cookie", config.Config{BaseURL: "http://board.example.com", CookieSecure: true}, []string{"COOKIE_SECURE=true"}},
		{"http remote insecure cookie", config.Config{BaseURL: "http://board.example.com"}, nil},
		{"https without secure cookie", config.Config{BaseURL: "https://board.example.com"}, []string{"COOKIE_SECURE=false"}},
		{"trust proxy", config.Config{BaseURL: "https://board.example.com", CookieSecure: true, TrustProxy: true}, []string{"TRUST_PROXY=true"}},
		{"both", config.Config{BaseURL: "https://b.example.com", TrustProxy: true}, []string{"COOKIE_SECURE=false", "TRUST_PROXY=true"}},
		{"unparsable base url", config.Config{BaseURL: "://bad"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := configWarnings(tc.cfg)
			if len(got) != len(tc.want) {
				t.Fatalf("warnings = %q, want %d containing %q", got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("warning %d = %q, want it to contain %q", i, got[i], w)
				}
			}
		})
	}
}
