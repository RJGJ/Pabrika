#!/usr/bin/env bash
# Build the Pabrika image and (re)create the container. Works with Docker or Podman.
#
#   ENGINE=docker ./deploy/run-container.sh        # default
#   ENGINE=podman ./deploy/run-container.sh
#   DRY_RUN=1 ./deploy/run-container.sh            # print the commands, run nothing
#
# Run it from anywhere; it builds from the Go module root (the folder with the Dockerfile).
# It is idempotent: rerunning it is also how you upgrade (git pull, then run it again).
# State lives in the named volume, so recreating the container keeps your data.
#
# Optional environment:
#   ENV_FILE    env file passed to the container        (default /etc/pabrika.env)
#   NAME        container name                           (default pabrika)
#   IMAGE       image name                               (default pabrika)
#   VOLUME      named volume mounted at /data            (default pabrika-data)
#   HOST_PORT   loopback port the proxy connects to      (default 8080)
#   MEMORY      container memory limit                   (default 512m)
#   VERSION     value for `pabrika version`              (default: git describe)
#   SKIP_BUILD  1 = skip the build (image already built or loaded with `docker load`)
set -euo pipefail

ENGINE="${ENGINE:-docker}"
ENV_FILE="${ENV_FILE:-/etc/pabrika.env}"
NAME="${NAME:-pabrika}"
IMAGE="${IMAGE:-pabrika}"
VOLUME="${VOLUME:-pabrika-data}"
HOST_PORT="${HOST_PORT:-8080}"
MEMORY="${MEMORY:-512m}"
SKIP_BUILD="${SKIP_BUILD:-0}"
DRY_RUN="${DRY_RUN:-0}"

case "$ENGINE" in
  docker | podman) ;;
  *) echo "ENGINE must be docker or podman (got: $ENGINE)" >&2; exit 2 ;;
esac

# The module root is the parent of the folder this script lives in.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"

# run prints the command in dry-run mode and executes it otherwise.
run() {
  if [ "$DRY_RUN" = 1 ]; then
    printf '+'
    printf ' %q' "$@"
    printf '\n'
  else
    "$@"
  fi
}

if [ ! -f "$ENV_FILE" ]; then
  if [ "$DRY_RUN" = 1 ]; then
    echo "note: $ENV_FILE does not exist (ignored in a dry run)" >&2
  else
    echo "env file not found: $ENV_FILE" >&2
    echo "copy deploy/pabrika.env.example to $ENV_FILE and set BASE_URL first" >&2
    exit 1
  fi
fi

if [ "$SKIP_BUILD" != 1 ]; then
  run "$ENGINE" build --build-arg "VERSION=$VERSION" -t "$IMAGE" "$ROOT"
fi

# Replace any existing container. stop sends SIGTERM and the app shuts down gracefully.
if [ "$DRY_RUN" = 1 ]; then
  run "$ENGINE" stop "$NAME"
  run "$ENGINE" rm "$NAME"
else
  "$ENGINE" stop "$NAME" >/dev/null 2>&1 || true
  "$ENGINE" rm "$NAME" >/dev/null 2>&1 || true
fi

# Publish on loopback only: the reverse proxy on this host is the single public entry point.
# (Docker's published ports bypass ufw, so never publish 8080 on all interfaces.)
run "$ENGINE" run -d \
  --name "$NAME" \
  --restart unless-stopped \
  --memory "$MEMORY" \
  -p "127.0.0.1:${HOST_PORT}:8080" \
  -v "${VOLUME}:/data" \
  --env-file "$ENV_FILE" \
  "$IMAGE"

if [ "$DRY_RUN" = 1 ]; then
  exit 0
fi

if [ "$ENGINE" = podman ]; then
  echo "note: --restart does not bring a rootless Podman container back after a reboot."
  echo "      Use the systemd Quadlet unit in deploy/pabrika.container for that."
fi

# Wait up to 30 s for the app to answer, then show the result.
if command -v curl >/dev/null 2>&1; then
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${HOST_PORT}/healthz" >/dev/null 2>&1; then
      echo "pabrika is up: http://127.0.0.1:${HOST_PORT}/healthz answered"
      exit 0
    fi
    sleep 1
  done
  echo "pabrika did not answer on /healthz within 30 s; recent logs:" >&2
  "$ENGINE" logs --tail 30 "$NAME" >&2 || true
  exit 1
fi
echo "started; check it with: $ENGINE logs $NAME"
