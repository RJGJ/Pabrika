# syntax=docker/dockerfile:1
# Build context is this directory (the Go module root, next to go.mod):
#   docker build --build-arg VERSION=$(git describe --tags --always) -t pabrika .

# Stage 1: build the Vue app with Bun (build time only; no Bun or Node in the final image).
FROM oven/bun:1 AS web
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

# Stage 2: build the Go binary with the UI embedded (web/embed.go: go:embed all:dist).
FROM golang:1.23 AS server
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Must come after "COPY . ." so the built bundle is what go:embed sees.
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /pabrika ./cmd/pabrika \
 && mkdir /data

# Stage 3: minimal runtime (no shell, no package manager, CA certs and tzdata included).
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/RJGJ/Pabrika" \
      org.opencontainers.image.licenses="MIT"
COPY --from=server /pabrika /pabrika
# A named volume mounted on an empty /data inherits this ownership (UID 65532).
COPY --from=server --chown=nonroot:nonroot /data /data
ENV DB_PATH=/data/pabrika.db PORT=8080
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/pabrika", "healthcheck"]
# Exec form: the binary is PID 1 and receives SIGTERM directly. No CMD on purpose;
# run CLI commands with "docker exec" or "docker run --entrypoint /pabrika".
ENTRYPOINT ["/pabrika", "serve"]
