package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// TokenPrefix starts every API token secret.
	TokenPrefix = "pb_"
	// tokenSecretLen is len("pb_") + 64 hex characters.
	tokenSecretLen = len(TokenPrefix) + 64
	// StoredPrefixLen is how many leading characters of the secret are kept for display.
	StoredPrefixLen = 8
)

// Generate makes a new API token secret: "pb_" + 64 lowercase hex (32 random bytes). It
// returns the secret (shown to the user once), its SHA-256 hex (the only thing stored) and
// the display prefix (first 8 characters, including "pb_").
func Generate() (secret, hash, prefix string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	secret = TokenPrefix + hex.EncodeToString(b)
	return secret, HashToken(secret), secret[:StoredPrefixLen]
}

// HashToken is the SHA-256 hex of a token secret or session cookie value.
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IsTokenSecret reports whether s has exactly the shape pb_ + 64 lowercase hex. It is the
// gate that runs before any database lookup.
func IsTokenSecret(s string) bool {
	if len(s) != tokenSecretLen || !strings.HasPrefix(s, TokenPrefix) {
		return false
	}
	for i := len(TokenPrefix); i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ParseBearer extracts the token from an Authorization header value. The scheme is matched
// case-insensitively, exactly one space separates it from the token, and the token must pass
// IsTokenSecret; anything else is ("", false).
func ParseBearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !IsTokenSecret(token) {
		return "", false
	}
	return token, true
}
