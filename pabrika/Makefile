# Every target is a plain go one-liner, so nothing depends on GNU make being installed.
# Pinned tool versions: sqlc v1.31.1 (run through `go run`, never added to go.mod).
# The gate is CGO_ENABLED=0; the race detector needs cgo, so test-race is optional and separate.

SQLC = go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1

.PHONY: build test vet fmt generate test-race

build:
	CGO_ENABLED=0 go build ./...

test:
	CGO_ENABLED=0 go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

generate:
	$(SQLC) generate

test-race:
	CGO_ENABLED=1 go test -race ./...
