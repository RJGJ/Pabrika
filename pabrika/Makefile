# Every target is a plain command line, so nothing depends on GNU make beyond running them in
# order. On Windows without make, run the commands by hand (they are the same in Git Bash).
# Pinned tool versions: sqlc v1.31.1 (run through `go run`, never added to go.mod).
# The gate is CGO_ENABLED=0; the race detector needs cgo, so test-race is optional and separate.

SQLC = go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1

# Injected into the binary as main.version (see cmd/pabrika/main.go); `pabrika version` prints it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: web build compile run docker test test-go test-web test-race vet fmt generate audit

# web builds the Vue bundle into web/dist (the Go build embeds it). `bun run build` also
# restores web/dist/.gitkeep.
web:
	cd web && bun install --frozen-lockfile && bun run build

# build produces the single binary bin/pabrika with the UI embedded and the version set.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pabrika ./cmd/pabrika

# compile only type-checks every package (no bundle, no binary); it is what the old `build` did.
compile:
	CGO_ENABLED=0 go build ./...

# run serves on :8080 with a local database in ./data (plain-HTTP cookie for localhost).
run:
	COOKIE_SECURE=false go run -ldflags "-X main.version=$(VERSION)" ./cmd/pabrika serve

# docker needs a running Docker daemon; it is documented but not exercised by the test suite.
docker:
	docker build --build-arg VERSION=$(VERSION) -t pabrika .

test: test-go test-web

test-go:
	CGO_ENABLED=0 go test ./...

test-web:
	cd web && bun run typecheck && bun run test

vet:
	go vet ./...

fmt:
	gofmt -l .

generate:
	$(SQLC) generate

test-race:
	CGO_ENABLED=1 go test -race ./...

# audit runs the Phase 6 security grep and tool checks (docs/security-audit.md). It prints one
# PASS/FAIL/SKIP line per check and exits non-zero on any FAIL. Needs a POSIX shell.
audit:
	sh scripts/audit.sh
