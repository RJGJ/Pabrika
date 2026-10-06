# Deploy Pabrika on a VPS

From a blank Ubuntu 24.04 or Debian 12 server to Pabrika running at `https://your-domain`, using **Docker or Podman** for the app and **Caddy or nginx** for TLS. Pick one of each; the guide marks which steps belong to which.

> **Status:** the application and its Linux-independent behaviour were tested on Windows (see [FINDINGS.md](FINDINGS.md)). The container build, the scripts' real runs, Caddy, nginx, certbot and Quadlet were **not run on a server**: the commands below come from the specs and the projects' own documentation, and the scripts were only syntax-checked and dry-run. Expect to adjust a detail or two, and tell us what you hit.

The ready-to-copy files are in [`deploy/`](../deploy/):

| File | Purpose |
|---|---|
| `pabrika.env.example` | Settings (`BASE_URL`, `ALLOW_SIGNUP`, `TRUST_PROXY`) |
| `run-container.sh` | Build the image and (re)create the container (Docker or Podman); also the upgrade command |
| `pabrika.container` | Podman Quadlet unit (systemd keeps it running, restarts it after a reboot) |
| `Caddyfile` | Caddy reverse proxy with automatic TLS |
| `nginx-pabrika-http.conf`, `nginx-pabrika.conf` | nginx: bootstrap for the certificate, then the full TLS site |
| `backup.sh`, `pabrika-backup.service`, `pabrika-backup.timer` | Daily database backup |

## 0. What you need

- A VPS with **1 vCPU and 1 GB RAM or more**. Password hashing uses 64 MiB per hash, and the container is limited to 512 MB. Building the image on a 1 GB server can run out of memory, so either add swap (step 3) or build elsewhere (step 4b).
- About **2 GB of free disk** for the build, then a few hundred MB at rest.
- A **domain name** with an `A` record (and `AAAA` if the server has IPv6) pointing at the server's IP. TLS certificates need it to resolve before you start the proxy.
- SSH access, and ports **22, 80 and 443** reachable.

Throughout, replace `pabrika.example.com` with your domain and `you@example.com` with your email.

## 1. Prepare the server

Log in as root once and create a normal user. (Skip what your provider already did.)

```bash
adduser deploy
usermod -aG sudo deploy
rsync --archive --chown=deploy:deploy ~/.ssh /home/deploy      # reuse your SSH key
```

Open a **second** terminal and confirm `ssh deploy@your-server` works before continuing. Then, as `deploy`:

```bash
sudo apt update && sudo apt -y upgrade
sudo apt -y install git curl ufw unattended-upgrades

sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

Optionally disable password logins (`PasswordAuthentication no` in `/etc/ssh/sshd_config`, then `sudo systemctl restart ssh`) once your key works.

**Docker bypasses ufw.** A published container port is opened by Docker's own firewall rules, whatever ufw says. That is why Pabrika is published on `127.0.0.1` only: the reverse proxy on the same host is the single public entry point. Never publish 8080 on all interfaces.

## 2. Install a container engine (pick one)

**Docker.** The quick way is the distribution package; the official repository gives newer versions ([instructions](https://docs.docker.com/engine/install/ubuntu/)).

```bash
sudo apt -y install docker.io
sudo systemctl enable --now docker
```

**Podman** (rootless, no daemon). Ubuntu 24.04 ships Podman 4.9; Debian 12 ships 4.3, which is too old for the Quadlet unit (see step 5, "Debian 12").

```bash
sudo apt -y install podman
```

For rootless Podman also run `loginctl enable-linger "$USER"` now, so your containers keep running after you log out.

## 3. Optional: swap (small servers)

Compiling the Go binary and the web bundle can run out of memory on 1 GB.

```bash
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

## 4. Get the app onto the server

Nothing is published to a container registry, so you build the image yourself. The repository is public; clone it to `/opt/pabrika`. The Go module (and the `Dockerfile`) is in its `pabrika/` subfolder.

```bash
sudo git clone https://github.com/RJGJ/Pabrika.git /opt/pabrika
sudo chown -R "$USER": /opt/pabrika
cd /opt/pabrika/pabrika
```

**4a. Build on the server.** `run-container.sh` (step 5) does this for you. Nothing else to do.

**4b. Build on your own machine and send the image** (best for small servers). On your computer, from the `pabrika/` folder:

```bash
docker build --platform linux/amd64 --build-arg VERSION=$(git describe --tags --always) -t pabrika .
docker save pabrika | gzip | ssh deploy@your-server 'gunzip | sudo docker load'
```

Use `--platform linux/arm64` for an ARM VPS, and `podman`/`sudo podman load` if you use Podman. Then run `run-container.sh` with `SKIP_BUILD=1` in step 5. You still clone the repository on the server, because it holds the `deploy/` files.

## 5. Configure and start Pabrika

Create the settings file and set your public URL:

```bash
sudo install -m 640 -o root -g root deploy/pabrika.env.example /etc/pabrika.env
sudo nano /etc/pabrika.env          # set BASE_URL=https://pabrika.example.com
```

`BASE_URL` must be the exact public HTTPS address (no path, no trailing slash). `TRUST_PROXY=true` is right here because a proxy on the same host sits in front. Leave `COOKIE_SECURE` unset. All settings are explained in the [README](../README.md#configuration).

### Docker

```bash
sudo ENGINE=docker ./deploy/run-container.sh
```

That builds the image (add `SKIP_BUILD=1` if you loaded it in 4b), recreates the container, publishes it on `127.0.0.1:8080`, mounts the named volume `pabrika-data` at `/data`, limits memory to 512 MB and sets `--restart unless-stopped`, so Docker restarts it after a reboot. It waits up to 30 seconds for `GET /healthz` and prints recent logs if the app doesn't answer. Add `DRY_RUN=1` to print the commands instead.

### Podman (rootless, with systemd)

Use your own settings file, because the unit runs as your user:

```bash
install -m 600 deploy/pabrika.env.example ~/.config/pabrika.env
nano ~/.config/pabrika.env                         # set BASE_URL
podman build --build-arg VERSION=$(git describe --tags --always) -t pabrika .

mkdir -p ~/.config/containers/systemd
cp deploy/pabrika.container ~/.config/containers/systemd/
systemctl --user daemon-reload
systemctl --user start pabrika                     # Quadlet generates pabrika.service
journalctl --user -u pabrika -f                    # logs
```

The unit's `[Install]` section makes it start at boot, and `loginctl enable-linger` (step 2) keeps it alive without a login session.

Podman notes:

- **Debian 12 (Podman 4.3, no Quadlet).** Either install a newer Podman, or run `ENGINE=podman ENV_FILE=$HOME/.config/pabrika.env ./deploy/run-container.sh` and then generate a unit with `podman generate systemd --new --name pabrika --files` (deprecated but present in 4.3), move the file into `~/.config/systemd/user/` and `systemctl --user enable --now container-pabrika`.
- **"short-name resolution enforced" during the build.** Podman refuses to guess which registry `oven/bun` and `golang` come from. Add `unqualified-search-registries = ["docker.io"]` to `/etc/containers/registries.conf.d/50-pabrika.conf`, or run `podman build` once in an interactive terminal and choose `docker.io`.
- **Healthcheck.** Podman ignores the image's `HEALTHCHECK` for OCI-format images. Monitor `GET /healthz` yourself.
- **Bind mounts** (instead of the named volume) need `podman unshare chown 65532:65532 /path`, and on SELinux hosts `:Z` on the mount. The named volume avoids both.

### Check that it runs

```bash
curl -s http://127.0.0.1:8080/healthz                       # 200 and a short JSON body
sudo ss -ltnp | grep 8080                                   # must show 127.0.0.1:8080, not 0.0.0.0
```

## 6. Put a TLS reverse proxy in front (pick one)

The session cookie and API tokens must never travel over plain HTTP. Why each setting matters (no buffering for the live-update stream and `/mcp`, `X-Forwarded-For` overwritten, HSTS sent once) is explained in [proxy.md](proxy.md).

### Caddy (simplest: certificates are automatic)

```bash
sudo apt -y install debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt -y install caddy
```

(These are the commands from [Caddy's install page](https://caddyserver.com/docs/install#debian-ubuntu-raspbian); check it if they have changed.)

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
sudo nano /etc/caddy/Caddyfile            # replace pabrika.example.com
sudo systemctl reload caddy
```

Caddy fetches and renews the Let's Encrypt certificate on its own. Watch with `journalctl -u caddy -f` if the first request is slow.

### nginx (with certbot)

```bash
sudo apt -y install nginx certbot
sudo mkdir -p /var/www/certbot

# 1. Port-80 bootstrap so certbot can prove the domain is yours.
sudo cp deploy/nginx-pabrika-http.conf /etc/nginx/sites-available/pabrika
sudo nano /etc/nginx/sites-available/pabrika          # replace pabrika.example.com
sudo ln -sf /etc/nginx/sites-available/pabrika /etc/nginx/sites-enabled/pabrika
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx

# 2. Get the certificate; the deploy hook reloads nginx on every automatic renewal.
sudo certbot certonly --webroot -w /var/www/certbot -d pabrika.example.com \
     --deploy-hook "systemctl reload nginx"

# 3. Switch to the full TLS site.
sudo cp deploy/nginx-pabrika.conf /etc/nginx/sites-available/pabrika
sudo nano /etc/nginx/sites-available/pabrika          # replace pabrika.example.com (three places)
sudo nginx -t && sudo systemctl reload nginx
```

The Ubuntu `certbot` package installs a systemd timer for renewals; check it with `systemctl list-timers | grep certbot` and test with `sudo certbot renew --dry-run`. `nginx-pabrika.conf` uses `listen 443 ssl http2`, which works on nginx before 1.25.1 (Ubuntu 24.04 and Debian 12); its comments say what to change on newer versions.

## 7. Create the first user and lock down signup

Open `https://pabrika.example.com`. You can sign up in the browser, or create the account from the command line (the password never goes on the command line):

```bash
# Docker
printf '%s\n' 'a-long-password' | sudo docker exec -i pabrika /pabrika user create \
  --email you@example.com --name "You" --password-stdin
# Podman (rootless)
printf '%s\n' 'a-long-password' | podman exec -i pabrika /pabrika user create \
  --email you@example.com --name "You" --password-stdin
```

Passwords need at least 10 characters. Once your own accounts exist, **turn public signup off** so strangers can't register: set `ALLOW_SIGNUP=false` in the env file and recreate the container (Docker: `sudo ENGINE=docker SKIP_BUILD=1 ./deploy/run-container.sh`; Podman: `systemctl --user restart pabrika`). From then on add users with the command above. More CLI details (password reset, flags) are in the [README](../README.md#create-the-first-user-from-the-cli).

## 8. Verify

```bash
curl -sI https://pabrika.example.com/ | head -5                       # HTTP/2 200, one strict-transport-security
curl -s  https://pabrika.example.com/healthz
curl -skI https://pabrika.example.com/ | grep -ci '^strict-transport-security'   # prints 1
```

Then, in the browser: sign in, create a project and a ticket, and open the board in a second tab. A change in one tab should appear in the other within a second. If updates arrive in bursts or late, something is buffering the stream; see the checks in [proxy.md](proxy.md#verify-your-proxy). From another machine, `http://YOUR-IP:8080` must **not** connect.

## 9. Connect Claude Code (MCP)

In the web app open Settings, create an API token (use `read` scope, or limit it to one project, for agents that read tickets written by other people) and copy it: it is shown once.

```bash
claude mcp add --transport http pabrika https://pabrika.example.com/mcp \
  --header "Authorization: Bearer pb_your_token_here"
```

Ticket text is untrusted and can contain instructions aimed at your agent; see the safety notes in the [README](../README.md#connect-an-agent-over-mcp).

## 10. Backups

Everything lives in one SQLite file in the `pabrika-data` volume. Don't copy it while the app runs; use `backup.sh`, which uses SQLite's online backup in a throwaway container, checks the result and deletes backups older than 14 days. Background and restore steps: [backup.md](backup.md).

```bash
sudo ENGINE=docker ./deploy/backup.sh                  # one backup now, written to /var/backups/pabrika
sudo cp deploy/pabrika-backup.service deploy/pabrika-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pabrika-backup.timer       # daily at about 03:15
systemctl list-timers | grep pabrika
```

For rootless Podman, install the two unit files under `~/.config/systemd/user/`, set `Environment=ENGINE=podman` in the service, and use `systemctl --user` (details are in the unit's header comment).

Then:

- **Copy backups off the server** (another machine or object storage). A backup on the same disk is not a backup if the disk dies. Backups contain password and token hashes: encrypt them before they leave the machine.
- **Test one restore** into a scratch volume now, following [backup.md](backup.md#restore), so you know it works before you need it.

## 11. Upgrade

```bash
cd /opt/pabrika
sudo systemctl start pabrika-backup.service            # back up first
git pull
cd pabrika
sudo ENGINE=docker ./deploy/run-container.sh           # rebuilds, recreates, waits for /healthz
```

Podman with Quadlet: `git pull`, `podman build -t pabrika .` in `pabrika/`, then `systemctl --user restart pabrika`. Database migrations apply automatically at start. To roll back, restore the backup you took before the upgrade (a newer database can't be opened by an older version) and check out the previous tag.

## 12. Hardening checklist

- [ ] `ALLOW_SIGNUP=false` after your accounts exist.
- [ ] Port 8080 is not reachable from outside (`ss -ltnp` shows `127.0.0.1:8080`; connecting to `IP:8080` from elsewhere fails).
- [ ] Only 22, 80 and 443 are open in ufw, and SSH uses keys.
- [ ] Automatic security updates are on (`unattended-upgrades`).
- [ ] Backups run daily, are copied off the server, and one restore has been tested.
- [ ] API tokens are as narrow as possible (read-only or limited to one project) and are revoked when no longer needed.
- [ ] One container per data volume: don't run two Pabrika containers on the same volume (the live-update hub is in memory).

## Troubleshooting

| Symptom | Likely cause and fix |
|---|---|
| Every login fails with 403 `origin_mismatch` | `BASE_URL` doesn't exactly match the address in the browser (scheme, host, port, no trailing slash). Fix it in the env file and recreate the container. |
| Login appears to succeed, then you're signed out | The session cookie was dropped: you're on `http://` instead of `https://`, or the proxy isn't serving HTTPS. `COOKIE_SECURE` should stay at its default. |
| 502 Bad Gateway from the proxy | The container isn't running or isn't on `127.0.0.1:8080`. `sudo docker ps`, `sudo docker logs pabrika`, `curl http://127.0.0.1:8080/healthz`. |
| 429 "too many attempts" for everyone | `TRUST_PROXY` is `false` (all users share the proxy's IP), or the proxy appends to `X-Forwarded-For` instead of overwriting it. Use the provided configs and `TRUST_PROXY=true`. |
| Live updates delayed or arriving in bursts | The proxy buffers the stream (`/api/v1/projects/*/events`) or compresses it. Use the provided configs; see [proxy.md](proxy.md). |
| `permission denied` on `/data` in the logs | A bind mount owned by the wrong user. Use the named volume, or `chown 65532:65532` the folder (Podman: `podman unshare chown ...`). |
| Image build killed or "out of memory" | Add swap (step 3) or build on your own machine (step 4b). |
| Browser says the certificate is invalid | The domain didn't resolve when the certificate was requested. Check DNS, then `journalctl -u caddy` or rerun `certbot certonly`. |
| Container shows as unhealthy | Rootless Podman ignores the image healthcheck. Check `curl http://127.0.0.1:8080/healthz` yourself. |
| "UI not built" page | The image was built without the web bundle. Rebuild from a checkout where `web/dist` isn't in the way (the `Dockerfile` builds it itself). |

See also the [README troubleshooting section](../README.md#troubleshooting).
