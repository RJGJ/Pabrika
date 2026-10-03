package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
)

// testEnv builds an env over a temp-file database with cheap argon2 parameters.
func testEnv(t *testing.T, stdin string, extra map[string]string) (e env, out, errb *bytes.Buffer, dbPath string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "data", "p.db")
	vars := map[string]string{"DB_PATH": dbPath}
	for k, v := range extra {
		vars[k] = v
	}
	out, errb = &bytes.Buffer{}, &bytes.Buffer{}
	e = env{
		getenv:     func(k string) string { return vars[k] },
		stdin:      strings.NewReader(stdin),
		stdout:     out,
		stderr:     errb,
		hashParams: auth.TestParams,
	}
	return
}

func TestVersionAndUsage(t *testing.T) {
	e, out, errb, _ := testEnv(t, "", nil)
	ctx := context.Background()
	if run(ctx, []string{"version"}, e) != 0 || strings.TrimSpace(out.String()) != version {
		t.Fatal(out.String())
	}
	if run(ctx, nil, e) != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatal("no args should print usage and exit 2")
	}
	if run(ctx, []string{"bogus"}, e) != 2 {
		t.Fatal("unknown command should exit 2")
	}
	out.Reset()
	if run(ctx, []string{"help"}, e) != 0 || !strings.Contains(out.String(), "reset-password") {
		t.Fatal("help")
	}
	if run(ctx, []string{"serve", "extra"}, e) != 2 {
		t.Fatal("serve takes no arguments")
	}
	if run(ctx, []string{"user"}, e) != 2 || run(ctx, []string{"user", "nope"}, e) != 2 {
		t.Fatal("user needs a known subcommand")
	}
}

func TestServeBadConfig(t *testing.T) {
	e, _, errb, _ := testEnv(t, "", map[string]string{"PORT": "nope"})
	if code := run(context.Background(), []string{"serve"}, e); code != 1 || !strings.Contains(errb.String(), "PORT") {
		t.Fatal(code, errb.String())
	}
}
