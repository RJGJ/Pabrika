# Running behind a reverse proxy

Pabrika speaks plain HTTP on port 8080 and has no built-in TLS. Put a TLS-terminating proxy in front of it, because the session cookie and bearer tokens must not travel over plain HTTP. Caddy and nginx examples follow.

## Rules

- **TLS ends at the proxy.** Do not expose 8080 publicly. Publish it on loopback only (`-p 127.0.0.1:8080:8080`; Docker's published ports bypass host firewalls such as ufw), or put the proxy on the same Docker network and do not publish the port at all.
- **`BASE_URL`** must be the public HTTPS URL exactly: scheme, host and port, no path, no trailing slash (for example `https://pabrika.example.com`). It drives the Origin check and the links in MCP results. A mismatch shows up as 403 `origin_mismatch` on every login.
- **`COOKIE_SECURE`** stays `true` (the default). The app never infers it from the connection, because behind a proxy it only sees plain HTTP.
- **`TRUST_PROXY=true`** so the rate limiter and logs see the real client IP. Without it every client shares the proxy's IP and the 5-per-minute login limit applies to everyone together. The app uses the **first** `X-Forwarded-For` hop, so the proxy must **overwrite** the header and not append to what the client sent (both snippets below do). Leave it `false` if the container is reachable without a proxy, because clients could then spoof the header. A CDN in front of the proxy is not covered (the first hop would be the CDN).
- **HSTS:** set `Strict-Transport-Security: max-age=31536000` at the proxy. The app also sends it when `BASE_URL` starts with `https://`; the snippets make sure it is sent once.
- **Compression belongs to the proxy and must exclude the event stream.** A buffering compressor stalls it. The app does no compression itself and sends `Cache-Control: no-transform` on the stream.
- **Do not buffer** `/api/v1/projects/*/events` (live updates) or `/mcp` (some responses are streamed). Read timeouts on those paths must exceed the 25 s keepalive; the nginx snippet uses 1 h.
- **HTTP/2** at the proxy is recommended. Browsers allow about 6 HTTP/1.1 connections per origin and every open board holds one stream, so with HTTP/1.1 the 7th open tab hangs. Caddy enables HTTP/2 by default.
- **Memory:** password hashing uses 64 MiB per hash and up to 4 run at once. Give the container at least 512 MB (`--memory 512m`).
- Run a single container per volume (the event hub is in memory).

Example container for a proxy on the same host:

```bash
docker run -d --name pabrika --restart unless-stopped --memory 512m \
  -p 127.0.0.1:8080:8080 -v pabrika-data:/data \
  -e BASE_URL=https://pabrika.example.com -e TRUST_PROXY=true pabrika
```

## Caddy

Caddy streams by default; `flush_interval -1` is an explicit safe setting. Compression applies to everything except the events path.

```caddyfile
pabrika.example.com {
	@compressible not path_regexp ^/api/v1/projects/[^/]+/events$
	encode @compressible gzip zstd
	header Strict-Transport-Security "max-age=31536000"
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

For a local trial use the site address `pabrika.localhost` and add `tls internal` inside the site block (`*.localhost` resolves to loopback). If Caddy runs in Docker, use `host.docker.internal:8080` instead of `127.0.0.1:8080`.

## nginx

`proxy_buffering off` on the events path and `/mcp`; the app's own HSTS header is hidden so it is sent once.

```nginx
server {
    listen 443 ssl;
    http2 on;                      # nginx 1.25.1+; on older versions use: listen 443 ssl http2;
    server_name pabrika.example.com;
    # ssl_certificate / ssl_certificate_key ...

    client_max_body_size 1m;
    proxy_hide_header Strict-Transport-Security;
    add_header Strict-Transport-Security "max-age=31536000" always;

    gzip on;
    gzip_types text/css application/javascript application/json image/svg+xml;

    location ~ ^/api/v1/projects/[^/]+/events$ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        gzip off;
    }

    location = /mcp {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_buffering off;
        proxy_read_timeout 3600s;
        gzip off;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

`add_header` inside a `location` replaces inherited ones; these locations add none, so the server-level HSTS applies everywhere.

## Verify your proxy

Replace the host, email and password, and `WEB` with one of your project keys. Log in with a cookie jar, then stream:

```bash
curl -sk -c jar.txt -H 'Content-Type: application/json' -H 'Origin: https://pabrika.example.com' \
  -d '{"email":"you@example.com","password":"..."}' https://pabrika.example.com/api/v1/auth/login
curl -skN -b jar.txt https://pabrika.example.com/api/v1/projects/WEB/events \
  | while IFS= read -r line; do echo "$(date +%T) $line"; done
```

Expected: `retry: 3000` and `: connected` immediately, a `: keepalive` line about every 25 s, and an `event: ticket.created` frame the moment a ticket is created in another window. Lines arriving in bursts or late mean something is buffering.

Check `/mcp` through the proxy (stateless mode needs no `initialize` first; if the SDK version in use insists on one, send `initialize` before it):

```bash
curl -sk -X POST https://pabrika.example.com/mcp \
  -H 'Authorization: Bearer pb_...' -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Check that HSTS is sent exactly once:

```bash
curl -skI https://pabrika.example.com/ | grep -ci '^strict-transport-security'
```

It should print `1`.
