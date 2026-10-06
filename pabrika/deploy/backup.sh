#!/usr/bin/env bash
# Online backup of the Pabrika database, with an integrity check and old-backup cleanup.
# Works with Docker or Podman. Run it while the app is running (see docs/backup.md for why).
#
#   ENGINE=docker ./deploy/backup.sh               # default
#   ENGINE=podman ./deploy/backup.sh
#   DRY_RUN=1 ./deploy/backup.sh                   # print the commands, run nothing
#
# The Pabrika image has no shell or sqlite3, so the backup runs in a throwaway container that
# mounts the same volume. Backups contain password and token hashes: keep them private and
# encrypt them if they leave the machine.
#
# Optional environment:
#   BACKUP_DIR   where backups go                       (default /var/backups/pabrika)
#   KEEP_DAYS    delete backups older than this         (default 14)
#   VOLUME       the Pabrika data volume                (default pabrika-data)
#   NAME         the running container's name           (default pabrika)
#   HELPER_IMAGE image used to run sqlite3              (default docker.io/library/alpine:3.20)
set -euo pipefail

ENGINE="${ENGINE:-docker}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/pabrika}"
KEEP_DAYS="${KEEP_DAYS:-14}"
VOLUME="${VOLUME:-pabrika-data}"
NAME="${NAME:-pabrika}"
HELPER_IMAGE="${HELPER_IMAGE:-docker.io/library/alpine:3.20}"
DRY_RUN="${DRY_RUN:-0}"

case "$ENGINE" in
  docker | podman) ;;
  *) echo "ENGINE must be docker or podman (got: $ENGINE)" >&2; exit 2 ;;
esac

OUT="pabrika-$(date +%F-%H%M%S).db"

run() {
  if [ "$DRY_RUN" = 1 ]; then
    printf '+'
    printf ' %q' "$@"
    printf '\n'
  else
    "$@"
  fi
}

# The helper runs as root and could create root-owned -wal/-shm files that the app cannot open
# if Pabrika is not running, so refuse to run against a stopped container.
if [ "$DRY_RUN" != 1 ]; then
  if [ "$("$ENGINE" inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null || true)" != true ]; then
    echo "container '$NAME' is not running; start it first (or copy the stopped .db file by hand)" >&2
    exit 1
  fi
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$BACKUP_DIR"
fi

# The inner script prints "ok" when SQLite's integrity check passes.
INNER="apk add --no-cache sqlite >/dev/null && sqlite3 /data/pabrika.db \".backup /backup/$OUT\" && sqlite3 /backup/$OUT 'PRAGMA integrity_check;'"

if [ "$DRY_RUN" = 1 ]; then
  run "$ENGINE" run --rm -v "${VOLUME}:/data" -v "${BACKUP_DIR}:/backup" "$HELPER_IMAGE" sh -c "$INNER"
  run find "$BACKUP_DIR" -name 'pabrika-*.db' -mtime "+${KEEP_DAYS}" -delete
  exit 0
fi

RESULT="$("$ENGINE" run --rm -v "${VOLUME}:/data" -v "${BACKUP_DIR}:/backup" "$HELPER_IMAGE" sh -c "$INNER")"
if [ "$RESULT" != ok ]; then
  echo "integrity check failed for $BACKUP_DIR/$OUT: $RESULT" >&2
  exit 1
fi
chmod 600 "$BACKUP_DIR/$OUT"
echo "backup written: $BACKUP_DIR/$OUT"

find "$BACKUP_DIR" -name 'pabrika-*.db' -mtime "+${KEEP_DAYS}" -delete
