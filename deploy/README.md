# Converge — Deployment

Converge is a small, self-hostable system: one Go binary (the API), one
Next.js app (the web client), one PostgreSQL 16 database. This guide covers
running it with `docker compose` on a single host, hardening it, and
operating it day to day.

## Contents

- [Single host](#single-host)
- [Environment reference](#environment-reference)
- [TLS and reverse proxy](#tls-and-reverse-proxy)
- [Production checklist](#production-checklist)
- [Backups](#backups)
- [Upgrades](#upgrades)
- [Operations](#operations)
- [Scaling](#scaling)

## Single host

```sh
git clone <repo> converge && cd converge
cp .env.example .env
# mandatory: a real session secret
CONVERGE_SESSION_SECRET=$(openssl rand -hex 32)   # put it in .env
docker compose up --build
```

Containers:

| Service    | Role                                                        | Port   |
| ---------- | ----------------------------------------------------------- | ------ |
| `web`      | Next.js app; serves the UI and proxies `/api/*` to the API  | 3000   |
| `api`      | Go API: HTTP, auth, domain, SSE, migrations                 | 3001   |
| `postgres` | Database; published on `127.0.0.1` only                     | 5432   |
| `seed`     | One-shot demo-data seeder (the `seed` profile)             | —      |

The seeder creates the **Acme** demo workspace (sign in with
`demo@converge.dev`) on a fresh database. It is idempotent and re-runs on
every `up`. It is enabled by default via `COMPOSE_PROFILES=seed` in
`.env`; remove that line for a production database that should start empty.

On first run the API applies all schema migrations automatically
(`CONVERGE_AUTO_MIGRATE` defaults to on).

## Environment reference

Everything is configured via environment variables; there is no file-based
configuration. Docker compose reads `.env` in the repository root.

### API (server)

| Variable                   | Default                             | Purpose                                              |
| -------------------------- | ----------------------------------- | ---------------------------------------------------- |
| `CONVERGE_HTTP_ADDR`       | `:3001`                             | Listen address                                       |
| `CONVERGE_DATABASE_URL`    | — (required)                        | Postgres DSN                                        |
| `CONVERGE_PUBLIC_URL`      | `http://localhost:3001`             | Public base URL; logged at startup                   |
| `CONVERGE_WEB_ORIGIN`      | `http://localhost:3000`             | Web client origin; CORS, SSE headers, magic-link URLs |
| `CONVERGE_LOG_LEVEL`       | `info`                              | `debug` \| `info` \| `warn` \| `error`               |
| `CONVERGE_HTTP_TIMEOUT`    | `30s`                               | Per-request read timeout                              |
| `CONVERGE_DB_MIN_CONNS`    | `2` (compose) / `1` (binary)        | Connection pool floor                                 |
| `CONVERGE_DB_MAX_CONNS`    | `20`                                | Connection pool ceiling                               |
| `CONVERGE_SECURE_COOKIES`  | `false`                             | `Secure` cookie flag; set `true` behind TLS          |
| `CONVERGE_SESSION_SECRET`  | dev fallback                        | HMAC key for session tokens — **set a real value**   |
| `CONVERGE_ACCESS_TOKEN_TTL`| `1h`                                | Client access-token lifetime                          |
| `CONVERGE_REFRESH_TOKEN_TTL` | `720h`                            | Refresh-token lifetime (sign-in session length)      |
| `CONVERGE_DEV_MODE`        | `true` (compose)                    | Magic link in sign-in response — **off in prod**     |
| `CONVERGE_AUTO_MIGRATE`    | on                                | Apply schema migrations at startup                    |

### Web (build-time, baked into the image)

| Variable                     | Default                          | Purpose                                            |
| ---------------------------- | -------------------------------- | -------------------------------------------------- |
| `NEXT_PUBLIC_BACKEND_HOST`   | `http://localhost:3001`          | Where the **browser** connects for the SSE stream  |
| `NEXT_PUBLIC_BASE_HOST`      | `http://localhost:3000`          | Base origin used by the client                     |
| `NEXT_PUBLIC_VERSION`        | `0.1.0`                          | Version stamp                                      |
| `NEXT_PUBLIC_NODE_ENV`       | `production` (in the image)      | —                                                  |

### Web (runtime)

| Variable           | Purpose                                                            |
| ------------------ | ------------------------------------------------------------------ |
| `API_PROXY_TARGET` | Upstream for the in-app `/api/*` proxy. Compose sets `http://api:3001`. |

### Compose

| Variable                        | Purpose                                             |
| ------------------------------- | --------------------------------------------------- |
| `POSTGRES_USER`/`PASSWORD`/`DB` | Postgres credentials and database name              |
| `COMPOSE_PROFILES`              | `seed` runs the one-shot demo seeder                |

`NEXT_PUBLIC_*` values are baked in at build time, so changing them requires
rebuilding the web image. In the common single-domain layout (below) the
browser reaches everything through one origin, so the default build args keep
working without rebuilds.

## TLS and reverse proxy

All session cookies are `SameSite=Lax`; the SSE stream and AJAX use a mix of
same-origin (proxied through `web`) and direct-`NEXT_PUBLIC_BACKEND_HOST`
(SSE) connections. Two layouts work:

### Pattern A — two origins (simplest, no proxy config)

Publish both ports and point the web build at the API's public origin:

```
NEXT_PUBLIC_BACKEND_HOST=https://api.converge.example.com   # build arg
CONVERGE_WEB_ORIGIN=https://app.converge.example.com
CONVERGE_PUBLIC_URL=https://api.converge.example.com
```

The SSE stream connects cross-origin, so the API answers it with explicit CORS
headers for `CONVERGE_WEB_ORIGIN`. Works with any TLS-terminating proxy in
front of each port (just forward TCP; no special headers).

### Pattern B — one domain (recommended)

Put both containers behind one TLS-terminating reverse proxy. Everything is
then same-origin: CORS is never exercised, and the default build args are
already correct — no image rebuild needed.

Caddyfile:

```caddy
converge.example.com {
	reverse_proxy /api/* api:3001 {
		flush_interval -1   # stream SSE immediately; never buffer
	}
	reverse_proxy web:3000
}
```

nginx:

```nginx
location /api/ {
	proxy_pass http://api:3001;
	proxy_http_version 1.1;
	proxy_set_header Connection "";
	proxy_buffering off;      # SSE must stream
	proxy_cache off;
	proxy_read_timeout 1h;    # keep idle streams alive
}
location / {
	proxy_pass http://web:3000;
}
```

The API itself already sends `X-Accel-Buffering: no` and 15 s keep-alive
pings on the stream; the `flush_interval` / `proxy_buffering off` lines are
what keeps the proxy from holding the bytes.

Set in `.env`:

```sh
CONVERGE_WEB_ORIGIN=https://converge.example.com
CONVERGE_PUBLIC_URL=https://converge.example.com
CONVERGE_SECURE_COOKIES=true
```

Rebuild web with `NEXT_PUBLIC_BASE_HOST=https://converge.example.com` (the
`NEXT_PUBLIC_BACKEND_HOST` default stays `http://localhost:3001` for local
use; in this layout set it to the public origin as well if you also publish
the API port directly).

## Production checklist

- [ ] `CONVERGE_SESSION_SECRET` set to `openssl rand -hex 32` output
- [ ] `CONVERGE_DEV_MODE=false` (dev mode leaks the magic link into the API)
- [ ] `CONVERGE_SECURE_COOKIES=true` (TLS only)
- [ ] `COMPOSE_PROFILES` line removed from `.env` (no demo workspace)
- [ ] Postgres not reachable off the host (compose binds `127.0.0.1`; keep it that way)
- [ ] Reverse proxy terminates TLS and streams `/api/*` without buffering
- [ ] Backup job in place (below)

## Backups

Everything is in one database:

```sh
docker compose exec -T postgres \
  pg_dump -U converge -d converge --format=custom -f /tmp/converge.dump
docker cp converge-postgres:/tmp/converge.dump backups/converge-$(date +%F).dump
```

Restore with `pg_restore -U converge -d converge --clean`. The `sync_outbox`
table is bounded (trimmed to 2000 rows per workspace on each write) and is
not worth preserving in archives — deltas older than the retained window
fall back to a full bootstrap automatically.

## Upgrades

```sh
git pull
docker compose up --build -d
```

Migrations run at API startup (forward only, additive-only in v1) before the
HTTP server accepts traffic; the compose healthcheck only reports `web`
dependencies after the database is ready. Take the web container down first
if you want a clean cutover:

```sh
docker compose up --build -d api postgres   # migrate + restart API
docker compose up -d web                    # then the app
```

Rolling back the *application* is a re-build of the previous tag; v1 schema
changes are additive, so an old image remains compatible with a newer
database.

## Operations

- **Liveness/readiness:** `GET /healthz` (process) and `GET /readyz`
  (process + database) on the API; `GET /version` reports the build.
- **Logs:** structured JSON on stdout from both containers
  (`docker compose logs -f api`).
- **Request tracing:** every request logs a `request_id`; the ID is echoed in
  the `X-Request-Id` response header and is accepted on inbound requests.
- **Pool sizing:** the default ceiling of 20 connections is comfortable for a
  single-team deployment. A rule of thumb: `max = (api instances) × 20`,
  kept below Postgres's `max_connections` (100). The pool has a 1 h
  connection lifetime, 30 m idle timeout, and 1 m health checks built in.
- **Realtime:** streams are in-process per API instance with a 64-message
  buffer per subscriber; slow subscribers are dropped and resynchronize via
  the delta endpoint on reconnect. See [Scaling](#scaling).
- **Outbox retention:** the last 2000 change records per workspace are
  retained; clients whose watermark is older than the retained window get
  `stale` and take a full bootstrap. This bounds the change feed regardless
  of team size.

## Scaling

v1 is a single API instance by design:

- Sessions are stateless (HMAC tokens + a DB audit row), so multiple API
  instances can serve traffic.
- The realtime fan-out is an **in-process** pub/sub. With more than one
  instance, each stream only receives its own mutations; cross-instance
  delivery needs a shared broker (NATS/Redis pub-sub) behind the same
  interface in `server/internal/broadcast`. The delta endpoint remains
  authoritative regardless, so a multi-instance deployment stays correct —
  realtime degrades to reconnect-rate freshness at worst.
- The database is the scaling bottleneck to watch first; the read paths
  are index-backed (set-based single queries, no N+1 in the sync feed).

The v1 feature surface is complete; horizontal fan-out (shared broker
behind the `Broadcaster` seam) is the next infrastructure step, not a
feature dependency.
