package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeBootstrapIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "does", "not", "exist", "p.db")
	env := func(k string) string {
		if k == "DB_PATH" {
			return dbPath
		}
		return ""
	}
	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		if code := run([]string{"serve"}, env, &out, &errb); code != 0 {
			t.Fatalf("run %d exit %d: %s", i, code, errb.String())
		}
		if !strings.Contains(errb.String(), "schema_version=1") {
			t.Fatalf("log missing schema version: %s", errb.String())
		}
	}
}

func TestServeBadConfig(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"serve"}, func(k string) string {
		if k == "PORT" {
			return "nope"
		}
		return ""
	}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "PORT") {
		t.Fatal(code, errb.String())
	}
}

func TestVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if run([]string{"version"}, func(string) string { return "" }, &out, &errb) != 0 || strings.TrimSpace(out.String()) != version {
		t.Fatal(out.String())
	}
	if run(nil, nil, &out, &errb) != 2 || run([]string{"bogus"}, nil, &out, &errb) != 2 {
		t.Fatal("unknown command should exit 2")
	}
}
