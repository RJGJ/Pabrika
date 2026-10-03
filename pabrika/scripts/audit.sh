#!/bin/sh
# Phase 6 security audit: the grep and tool checks of docs/security-audit.md.
# Run from the Go module root (the directory with go.mod): `make audit` or `sh scripts/audit.sh`.
#
# One PASS/FAIL/SKIP/INFO line per check; exit status 1 if any check FAILs.
# Environment:
#   AUDIT_SKIP_TESTS=1   skip `go test` and `bun run test` (when the gate already ran)
#   AUDIT_OFFLINE=1      skip govulncheck and bun audit (they need the network)

cd "$(dirname "$0")/.." || exit 2

fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }
skip() { echo "SKIP  $1"; }

# nohits NAME: stdin is the list of offending lines; PASS when empty.
nohits() {
	out=$(cat)
	if [ -z "$out" ]; then pass "$1"; else fail "$1"; echo "$out" | sed 's/^/        /'; fi
}

# not_comment drops grep -n lines whose code starts with //.
not_comment() { grep -vE '^[^:]*:[0-9]+:[[:space:]]*//'; }

# --- Go build, vet, tests ---------------------------------------------------------------
if go vet ./...; then pass "go vet ./... (A9)"; else fail "go vet ./... (A9)"; fi
if [ -n "$AUDIT_SKIP_TESTS" ]; then
	skip "CGO_ENABLED=0 go test ./... (A9)"
elif CGO_ENABLED=0 go test ./... >/dev/null; then pass "CGO_ENABLED=0 go test ./... (A9)"; else fail "CGO_ENABLED=0 go test ./... (A9)"; fi
[ -z "$(gofmt -l .)" ] && pass "gofmt clean" || fail "gofmt clean"

# --- server configuration (B5) -----------------------------------------------------------
grep -rnE '(^|[^A-Za-z])WriteTimeout[[:space:]]*[:=]' cmd internal --include=*.go | grep -v '_test.go' | not_comment \
	| nohits "no http.Server WriteTimeout set (B5)"
grep -rniE 'gzip|compress' cmd internal --include=*.go | grep -v '_test.go' | not_comment \
	| nohits "no compression in the app (SSE and /mcp must not be compressed)"

# --- SQL and secrets (S3, S4, S7) ----------------------------------------------------------
grep -rniE 'fmt\.Sprintf\(.*(select|insert|update|delete) ' internal --include=*.go | grep -v '_test.go' \
	| nohits "no SQL built with fmt.Sprintf (S7)"

# Secret-vs-secret comparisons must use crypto/subtle. Every remaining hit of the loose regex is a
# method, length, nil or actor-type comparison; the filter lists exactly those, so a new kind of
# hit fails and must be reviewed.
grep -rnE '(==|!=) *[a-zA-Z_.]*([Hh]ash|[Tt]oken|[Ss]ecret)' internal/auth internal/httpapi internal/mcpserver --include=*.go \
	| grep -v '_test.go' | not_comment \
	| grep -vE 'Method [!=]= |Token [!=]= nil|len\(s\) [!=]= |ActorAPIToken|svcDeletedToken|Token\.Scope' \
	| nohits "no unreviewed secret/hash comparison with == or != (S3)"
grep -rn 'subtle.ConstantTimeCompare' internal/auth --include=*.go | grep -v '_test.go' | grep -q . \
	&& pass "constant-time compare is used for password and token hashes (S3, S4)" \
	|| fail "constant-time compare is used for password and token hashes (S3, S4)"
grep -rn 'argon2.IDKey' internal --include=*.go | grep -v '_test.go' | not_comment | grep -v 'internal/auth/password.go' \
	| nohits "argon2.IDKey only called from internal/auth/password.go (S4)"

# --- logging (L2) --------------------------------------------------------------------------
grep -rniE 'slog\.|log\.(Print|Fatal)' internal cmd --include=*.go | grep -iE 'authorization|cookie|password|secret|token\)' \
	| grep -v '_test.go' | nohits "no log call mentions authorization, cookie, password, secret (L2)"

# --- web sources and bundle (H1, M2) ---------------------------------------------------------
if [ -d web/src ]; then
	vhtml=$(grep -rln 'v-html' web/src | grep -v 'MarkdownView.vue')
	[ -z "$vhtml" ] && grep -rq 'v-html' web/src/components/ticket/MarkdownView.vue \
		&& pass "v-html only in MarkdownView.vue (M2)" || fail "v-html only in MarkdownView.vue (M2)"
	grep -rn 'innerHTML' web/src | nohits "no innerHTML in web/src (M2)"
fi
if [ -f web/dist/index.html ]; then
	grep -rlE 'eval\(|new Function' web/dist/assets 2>/dev/null | nohits "no eval( or new Function in the bundle (H1)"
	# Only external scripts: every <script tag must carry src=.
	inline=$(grep -o '<script[^>]*>' web/dist/index.html | grep -v 'src=')
	[ -z "$inline" ] && pass "index.html has only <script src=...> tags, no inline script (H1)" \
		|| { fail "index.html has inline <script> (H1)"; echo "$inline" | sed 's/^/        /'; }
else
	skip "bundle checks (web/dist/index.html missing: run make web)"
fi

# --- web tests and dependency audits ---------------------------------------------------------
if [ -n "$AUDIT_SKIP_TESTS" ]; then
	skip "bun run test (M3)"
elif command -v bun >/dev/null 2>&1 && [ -d web/node_modules ]; then
	if (cd web && bun run test >/dev/null 2>&1); then pass "bun run test (M3)"; else fail "bun run test (M3)"; fi
else
	skip "bun run test (bun or web/node_modules missing)"
fi
if [ -n "$AUDIT_OFFLINE" ]; then
	skip "govulncheck and bun audit (AUDIT_OFFLINE)"
else
	if go run golang.org/x/vuln/cmd/govulncheck@latest ./... >/dev/null 2>&1; then pass "govulncheck ./... (A9)"; else fail "govulncheck ./... reported findings or could not run (A9)"; fi
	if command -v bun >/dev/null 2>&1 && [ -d web/node_modules ]; then
		if (cd web && bun audit >/dev/null 2>&1); then pass "bun audit (A9)"; else fail "bun audit reported findings or could not run (A9)"; fi
	else
		skip "bun audit (bun or web/node_modules missing)"
	fi
fi

echo
if [ "$fails" -eq 0 ]; then echo "audit: all checks passed"; else echo "audit: $fails check(s) FAILED"; exit 1; fi
