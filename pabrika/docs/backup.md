# Backups, restore and upgrades

## What to back up

One file: `pabrika.db` in `/data`. The app runs SQLite in WAL mode: recent writes live in `pabrika.db-wal` (with a `-shm` index), so **do not `cp` the file while the app runs**; you would get a torn or stale copy. Two safe ways:

- SQLite's online backup (below).
- Stop the container with `docker stop` (graceful shutdown checkpoints the WAL, so the `.db` file alone is then complete) and copy the file.

Backups contain argon2id password hashes and API token hashes. Treat them as sensitive and encrypt them if they leave the machine.

## Online backup

On a VPS, [`deploy/backup.sh`](../deploy/backup.sh) with the systemd timer next to it automates this for Docker or Podman (see [deploy-vps.md](deploy-vps.md#10-backups)). The commands below are what it runs.

The Pabrika image has no shell and no `sqlite3`, so run the backup from a throwaway container that mounts the same volume while Pabrika is running (the sqlite3 CLI and the app share the WAL index through the volume; this works for local Docker volumes, not network filesystems). This helper-container form is the one to use on Docker Desktop for Windows and Mac:

```bash
docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
  sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/pabrika.db ".backup /backup/pabrika-$(date +%F).db"'
```

`alpine` is only an example of an image you can install sqlite3 into; any image with sqlite3 works. Run the helper only while Pabrika is running: as root it could otherwise create root-owned `-wal` and `-shm` files that the app cannot open. To back up a stopped instance, copy the single `.db` file instead.

On Windows with Git Bash, paths such as `/data` and `/backup` can be rewritten into Windows paths. If the command fails with a mount or path error, prefix it with `MSYS_NO_PATHCONV=1` and use `-v "$(pwd -W)":/backup`, or run it from PowerShell with `-v "${PWD}:/backup"`.

On a Linux host where the volume path is readable you can instead run, directly:

```bash
sqlite3 /var/lib/docker/volumes/pabrika-data/_data/pabrika.db ".backup '/backups/pabrika-$(date +%F).db'"
```

That path does not exist on Docker Desktop; use the helper container there.

## Check a backup

```bash
sqlite3 pabrika-YYYY-MM-DD.db "PRAGMA integrity_check; SELECT count(*) FROM tickets; SELECT count(*) FROM users;"
```

It should print `ok` and plausible counts. Do this once at setup and periodically; an untested backup is not a backup. Practise one restore on a scratch volume (for example `pabrika-restore`).

## Daily backups (cron example)

```cron
0 3 * * * cd /srv/backups && docker run --rm -v pabrika-data:/data -v /srv/backups:/backup alpine sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/pabrika.db ".backup /backup/pabrika-$(date +%F).db"' && find . -name 'pabrika-*.db' -mtime +14 -delete
```

## Restore

Stop and remove the container, replace the file in the volume, delete stale `-wal` and `-shm`, fix ownership, start:

```bash
docker stop pabrika && docker rm pabrika
docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
  sh -c 'rm -f /data/pabrika.db-wal /data/pabrika.db-shm && cp /backup/pabrika-YYYY-MM-DD.db /data/pabrika.db && chown 65532:65532 /data/pabrika.db'
docker run -d --name pabrika ...   # same run command as before
```

Migrations run at startup, so restoring an older backup into a newer image works (the schema is upgraded). Restoring a newer database into an older image is not supported.

## Bind mounts

With `-v /srv/pabrika:/data` instead of a named volume, the host directory must be owned by UID 65532:

```bash
sudo chown 65532:65532 /srv/pabrika
```

Named volumes get this automatically from the image. On Docker Desktop for Windows or Mac prefer a named volume: WAL needs a real local filesystem with working shared memory and file locking. Host folders shared through the VM, NFS and SMB can corrupt or lock the database.

## Continuous replication with Litestream (optional)

Litestream is not shipped in the image and there is no compose file. Run it as a separate process or sidecar container that mounts the same volume and replicates `/data/pabrika.db` to S3-compatible storage. Example `litestream.yml`:

```yaml
dbs:
  - path: /data/pabrika.db
    replicas:
      - url: s3://my-bucket/pabrika
```

```bash
litestream replicate -config litestream.yml
```

Restore with Pabrika stopped, then fix ownership as above:

```bash
litestream restore -config litestream.yml -o /data/pabrika.db /data/pabrika.db
```

## Upgrade

Back up first, then pull or rebuild the image and recreate the container on the same volume:

```bash
docker pull <image>        # or: docker build --build-arg VERSION=... -t pabrika .
docker stop pabrika
docker rm pabrika
docker run -d --name pabrika ...   # same run command, same volume
```

Migrations apply on start. Rollback means restoring the backup taken before the upgrade.
