package config_test

import (
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := config.Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Port: 8080, DBPath: "./data/pabrika.db", BaseURL: "http://localhost:8080",
		AllowSignup: true, CookieSecure: true, TrustProxy: false}
	if c != want {
		t.Fatalf("got %+v want %+v", c, want)
	}
}

func TestOverrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"PORT": "9000", "DB_PATH": "/data/x.db", "BASE_URL": "https://pabrika.example.com/",
		"ALLOW_SIGNUP": "false", "COOKIE_SECURE": "0", "TRUST_PROXY": "true", "UNKNOWN": "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Port: 9000, DBPath: "/data/x.db", BaseURL: "https://pabrika.example.com"}
	want.TrustProxy = true
	if c != want {
		t.Fatalf("got %+v want %+v", c, want)
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		varn string
	}{
		{"port zero", map[string]string{"PORT": "0"}, "PORT"},
		{"port high", map[string]string{"PORT": "65536"}, "PORT"},
		{"port text", map[string]string{"PORT": "abc"}, "PORT"},
		{"bool signup", map[string]string{"ALLOW_SIGNUP": "maybe"}, "ALLOW_SIGNUP"},
		{"bool secure", map[string]string{"COOKIE_SECURE": "x"}, "COOKIE_SECURE"},
		{"bool proxy", map[string]string{"TRUST_PROXY": "x"}, "TRUST_PROXY"},
		{"base path", map[string]string{"BASE_URL": "http://a.com/app"}, "BASE_URL"},
		{"base query", map[string]string{"BASE_URL": "http://a.com?x=1"}, "BASE_URL"},
		{"base empty query", map[string]string{"BASE_URL": "http://a.com/?"}, "BASE_URL"},
		{"base fragment", map[string]string{"BASE_URL": "http://a.com/#f"}, "BASE_URL"},
		{"base no scheme", map[string]string{"BASE_URL": "a.com"}, "BASE_URL"},
		{"base ftp", map[string]string{"BASE_URL": "ftp://a.com"}, "BASE_URL"},
		{"base no host", map[string]string{"BASE_URL": "http://"}, "BASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(env(tt.env))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.varn) {
				t.Fatalf("error %q does not name %s", err, tt.varn)
			}
		})
	}
}

func TestOrigin(t *testing.T) {
	tests := []struct{ base, want string }{
		{"HTTP://Example.com:80/", "http://example.com"},
		{"https://Example.com:443", "https://example.com"},
		{"https://x:8443", "https://x:8443"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://localhost:5173", "http://localhost:5173"},
		{"http://[::1]:8080", "http://[::1]:8080"},
	}
	for _, tt := range tests {
		c, err := config.Load(env(map[string]string{"BASE_URL": tt.base}))
		if err != nil {
			t.Fatalf("%s: %v", tt.base, err)
		}
		if got := c.Origin(); got != tt.want {
			t.Errorf("Origin(%s) = %s, want %s", tt.base, got, tt.want)
		}
	}
}
