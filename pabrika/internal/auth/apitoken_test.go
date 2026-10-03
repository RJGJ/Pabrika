package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
)

func TestGenerate(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		secret, hash, prefix := auth.Generate()
		if !regexp.MustCompile(`^pb_[0-9a-f]{64}$`).MatchString(secret) {
			t.Fatalf("secret shape: %q", secret)
		}
		sum := sha256.Sum256([]byte(secret))
		if hash != hex.EncodeToString(sum[:]) || hash != auth.HashToken(secret) {
			t.Fatalf("hash mismatch")
		}
		if prefix != secret[:8] || !strings.HasPrefix(prefix, "pb_") || len(prefix) != 8 {
			t.Fatalf("prefix = %q", prefix)
		}
		if strings.Contains(hash, secret[3:]) || seen[secret] {
			t.Fatal("hash leaks secret or secret repeated")
		}
		seen[secret] = true
	}
}

func TestParseBearerTable(t *testing.T) {
	hex64 := strings.Repeat("ab12", 16)
	good := "pb_" + hex64
	cases := []struct {
		name, header string
		ok           bool
	}{
		{"canonical", "Bearer " + good, true},
		{"lowercase scheme", "bearer " + good, true},
		{"uppercase scheme", "BEARER " + good, true},
		{"63 hex", "Bearer pb_" + hex64[:63], false},
		{"65 hex", "Bearer pb_" + hex64 + "a", false},
		{"uppercase hex", "Bearer pb_" + strings.ToUpper(hex64), false},
		{"non hex", "Bearer pb_" + hex64[:63] + "g", false},
		{"wrong prefix", "Bearer px_" + hex64, false},
		{"no prefix", "Bearer " + hex64 + "abcd", false},
		{"basic scheme", "Basic " + good, false},
		{"no scheme", good, false},
		{"empty", "", false},
		{"scheme only", "Bearer", false},
		{"scheme and space only", "Bearer ", false},
		{"double space", "Bearer  " + good, false},
		{"trailing space", "Bearer " + good + " ", false},
		{"trailing newline", "Bearer " + good + "\n", false},
		{"tab separator", "Bearer\t" + good, false},
		{"10 KB header", "Bearer " + strings.Repeat("a", 10*1024), false},
		{"10 KB junk scheme", strings.Repeat("B", 10*1024) + " " + good, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := auth.ParseBearer(c.header)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && got != good {
				t.Fatalf("token = %q", got)
			}
			if !ok && got != "" {
				t.Fatalf("token leaked on failure: %q", got)
			}
		})
	}
}

func TestIsTokenSecret(t *testing.T) {
	secret, _, _ := auth.Generate()
	if !auth.IsTokenSecret(secret) {
		t.Fatal("generated secret rejected")
	}
	if auth.IsTokenSecret(secret[:len(secret)-1]) || auth.IsTokenSecret(secret+"0") || auth.IsTokenSecret("") {
		t.Fatal("bad length accepted")
	}
}
