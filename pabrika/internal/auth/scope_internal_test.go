package auth

import (
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestTokenScopeFailsClosed(t *testing.T) {
	cases := map[string]service.Scope{
		"read": service.ScopeRead, "comment": service.ScopeComment, "write": service.ScopeWrite,
		"": service.ScopeRead, "admin": service.ScopeRead, "WRITE": service.ScopeRead, " write": service.ScopeRead,
	}
	for in, want := range cases {
		if got := tokenScope(in); got != want {
			t.Errorf("tokenScope(%q) = %q, want %q", in, got, want)
		}
	}
}
