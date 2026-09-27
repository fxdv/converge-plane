# Converge — Deployment

Converge is a small, self-hostable system: one Go binary (the API), one
Next.js app (the web client), one PostgreSQL 16 database. This guide covers
running it with `docker compose` on a single host, hardening it, and
operating it day to day.

## Contents

- [Single host](#single-host)
- [Environment reference](#environment-reference)
- [Mail](#mail)
- [TLS and reverse proxy](#tls-and-reverse-proxy)
- [Client IP and trusted proxies](#client-ip-and-trusted-proxies)
- [Security behavior](#security-behavior)
- [Coding agents (MCP)](#coding-agents-mcp)
- [Production checklist](#production-checklist)
- [Backups](#backups)
- [Upgrades](#upgrades)
- [Operations](#operations)
- [Metrics](#metrics)
- [Scaling](#scaling)

## Single host

```sh
git clone <repo> converge && cd converge
cp .env.example .env
# mandatory outside dev mode: a real session secret
CONVERGE_SESSION_SECRET=$(openssl rand -hex 32)   # put it in .env
docker compose up --build
```

The API fails closed: outside dev mode it refuses to start without a
session secret of at least 32 characters, and it refuses dev mode unless
`CONVERGE_PUBLIC_URL` and `CONVERGE_WEB_ORIGIN` are both localhost.

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
| `CONVERGE_PUBLIC_URL`      | `http://localhost:3001`             | Public base URL; also an allowed request `Origin`    |
| `CONVERGE_WEB_ORIGIN`      | `http://localhost:3000`             | Web client origin; CORS, SSE headers, magic-link URLs, allowed request `Origin` |
| `CONVERGE_LOG_LEVEL`       | `info`                              | `debug` \| `info` \| `warn` \| `error`               |
| `CONVERGE_HTTP_TIMEOUT`    | `30s`                               | Per-request read timeout                              |
| `CONVERGE_DB_MIN_CONNS`    | `2` (compose) / `1` (binary)        | Connection pool floor                                 |
| `CONVERGE_DB_MAX_CONNS`    | `20`                                | Connection pool ceiling                               |
| `CONVERGE_RATE_LIMIT_RPS`  | `10`                                | Per-account sustained request rate; `0` disables     |
| `CONVERGE_RATE_LIMIT_BURST`| `50`                                | Per-account burst above the sustained rate           |
| `CONVERGE_SECURE_COOKIES`  | `true` iff `CONVERGE_WEB_ORIGIN` is https | `Secure` cookie flag; override only if the derivation is wrong for your layout |
| `CONVERGE_SESSION_SECRET`  | — (required outside dev mode)       | HMAC key for session tokens, ≥ 32 chars; the published dev value is refused |
| `CONVERGE_ACCESS_TOKEN_TTL`| `1h`                                | Access-token lifetime; also the longest a signed-out token can survive a restart |
| `CONVERGE_REFRESH_TOKEN_TTL` | `720h`                            | Refresh-token lifetime (sign-in session length)      |
| `CONVERGE_DEV_MODE`        | `true` (compose)                    | Magic link in sign-in response; localhost only — **off in prod** |
| `CONVERGE_AUTO_MIGRATE`    | on                                | Apply schema migrations at startup (`false` disables) |
| `CONVERGE_TRUSTED_PROXIES` | loopback + private ranges         | CIDRs/IPs whose `X-Forwarded-For` is believed; `none` trusts no proxy ([below](#client-ip-and-trusted-proxies)) |
| `CONVERGE_METRICS_ADDR`    | `""` (off)                        | Prometheus listener, e.g. `127.0.0.1:9464` ([Metrics](#metrics)) |
| `CONVERGE_MAIL_LOG_BODIES` | dev mode only                     | Log driver writes message bodies, **including sign-in links** |
| `CONVERGE_SMTP_HOST`       | `""`                              | SMTP server for sign-in and invitation mail; unset = log driver |
| `CONVERGE_SMTP_PORT`       | `587`                             | SMTP port                                             |
| `CONVERGE_SMTP_USER`       | `""`                              | SMTP auth username                                    |
| `CONVERGE_SMTP_PASS`       | `""`                              | SMTP auth password                                    |
| `CONVERGE_SMTP_FROM`       | `no-reply@converge.local`         | From: address on outgoing mail                        |
| `CONVERGE_SMTP_TLS`        | `starttls`                        | `starttls` \| `off` \| `implicit`                     |

The agent runtime and model-fleet variables (`CONVERGE_RUNTIME*`,
`CONVERGE_SWARM_REVIEW_INTERVAL`, `CONVERGE_LLM*`) are covered in the
[operations runbook](../docs/release/runbook.md). The authoritative list with
defaults is the comment on `Load` in `server/internal/config/config.go`.

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

## Mail

Sign-in links and workspace invitations are sent through the
`CONVERGE_SMTP_*` variables (stdlib SMTP; `starttls` by default,
`implicit` for port-465-style endpoints, `off` for a local relay). An
invitation carries a sign-in link; opening it signs the invitee in and
lands on the accept/decline invitation screen.

When `CONVERGE_SMTP_HOST` is unset the API runs the **log driver**. It
logs recipient and subject; it logs the body — which carries the sign-in
link, a bearer credential — only in dev mode or with
`CONVERGE_MAIL_LOG_BODIES=true`. So **a production deployment without SMTP
cannot sign anyone in** unless you accept links in the log (then treat the
log as secret). In dev mode the sign-in response also carries the link
directly.

Mail is best-effort: the sign-in code or invitation is committed before
mail is attempted, a delivery failure is logged and dropped, and the
recovery is "resend" or a re-invite. Mail is sent in the background after
the response.

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
	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;   # client IP
	proxy_buffering off;      # SSE must stream
	proxy_cache off;
	proxy_read_timeout 1h;    # keep idle streams alive
}
location / {
	proxy_pass http://web:3000;
	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
```

The API itself already sends `X-Accel-Buffering: no` and a 15 s heartbeat
event on the stream; the `flush_interval` / `proxy_buffering off` lines are
what keeps the proxy from holding the bytes.

Set in `.env`:

```sh
CONVERGE_WEB_ORIGIN=https://converge.example.com
CONVERGE_PUBLIC_URL=https://converge.example.com
# Secure cookies follow from the https origin; no flag needed.
```

Rebuild web with `NEXT_PUBLIC_BASE_HOST=https://converge.example.com` (the
`NEXT_PUBLIC_BACKEND_HOST` default stays `http://localhost:3001` for local
use; in this layout set it to the public origin as well if you also publish
the API port directly).

## Client IP and trusted proxies

Auth rate limits, logs, and the session audit use the client IP. The API
takes it from `X-Forwarded-For` by walking the chain from the right and
skipping hops in `CONVERGE_TRUSTED_PROXIES`; the first untrusted hop is the
client. Without a trusted proxy in front, the TCP peer address is used and
the header is ignored.

- The default (loopback + private ranges) fits compose: the `web` proxy and
  a reverse proxy on the Docker network or the host are trusted, and both
  append their hop (`web` does so for `/api/*`).
- Every proxy in front must append `X-Forwarded-For` (Caddy does by default;
  nginx needs the line shown above). Otherwise all users share the proxy's
  address and one auth rate-limit bucket.
- If clients can reach the API port directly, narrow the list to your
  proxies' addresses (or `none` with no proxy). With the default, a client
  whose connection arrives from a private address — including Docker's
  gateway in some port-publishing setups — chooses its own "client IP".

## Security behavior

What operators and integrators notice (wire details in
[docs/spec/08](../docs/spec/08-api-and-event-contracts.md#as-built-v1-server)):

- **Sessions.** Each sign-in is a row in `sessions`. Refresh tokens are
  single-use and rotate; replaying an old one revokes the session
  (`revoked_reason = 'reuse'`) and signs that browser out. Sign-out revokes
  the row immediately. An already-issued access token is refused after
  sign-out by the API instance that handled it, but survives a restart (or
  another instance) until `CONVERGE_ACCESS_TOKEN_TTL` expires.
- **Writes from a browser need your Origin.** A state-changing request whose
  `Origin` (else `Referer`) names any other site gets `403 CROSS_ORIGIN`,
  signed in or not; so does one with session cookies and neither header.
  Only `CONVERGE_WEB_ORIGIN` and `CONVERGE_PUBLIC_URL` are allowed, so open
  the app at exactly that origin (`127.0.0.1` is not `localhost`). Scripts
  that reuse browser cookies must send `Origin`; Bearer-token clients
  (agents) and clients that send no `Origin` without cookies are unaffected.
- **API responses** are `nosniff` and `Cache-Control: no-store`; a
  client-supplied `X-Request-Id` is kept only if it is at most 128 characters
  of `[A-Za-z0-9._:-]`.
- **Agent writes need `If-Match`.** Issue update/move/delete/handoff with an
  API token must send the issue `version` the agent read: `428` without it,
  `412` when stale.
- **Scoped agent tokens.** An agent token can be limited to scopes
  (`issues:read`, `issues:write`, `comments:read`, `comments:write`, `work`,
  `sync:read`) and to some of the agent's teams. Everything else is refused
  with `403` (another team's issues answer `404`). Hard delete and
  administration are never available to a scoped token. Tokens without scopes
  keep the agent's full authority. Give external tools the narrowest token
  that works.
- **External agents.** An agent created with `"driver": "external"` is worked
  by your own process through the work API (`GET /api/v1/agent/queue`, then
  claim, heartbeat and release an issue). The built-in runtime leaves such an
  agent alone. A claim that stops heartbeating lapses within its TTL (30–900
  s) and is swept within 10 s, even with `CONVERGE_RUNTIME=false`. Contract:
  [docs/spec/08](../docs/spec/08-api-and-event-contracts.md#as-built-v1-server).
- **Agent runs.** Each claim records a run: model, token and cost totals, a
  step trace (at most 1000 entries per run), evidence links, and an outcome.
  Cost and tokens are what the agent reports; Converge does not price or
  verify them, and the UI labels them as agent-reported. Links are shown
  only when they are http(s). Runs and their traces are kept with the issue
  and deleted with it; there is no separate retention job.
- **MCP.** `POST /api/v1/mcp` accepts agent API tokens only. Each tool call
  runs as the matching REST request under the same token, so scopes, team
  grants and `If-Match` apply unchanged, and one tool call counts as one
  request against the rate limit. Setup: [Coding agents
  (MCP)](#coding-agents-mcp).
- **Auth rate limits.** `/api/auth/*`: per IP, burst 30 then one request per
  2 s; sign-in codes per email, 5 then one per 3 min. Excess gets `429` with
  `Retry-After`. The `email/exists` endpoint is gone (it let anyone test
  which addresses have accounts).
- **Magic links** are single-use and consumed atomically; resending cannot
  revive a consumed code.
- **Web.** Security headers on every page (`frame-ancestors 'none'`,
  `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Permissions-Policy`);
  the Next.js image optimizer is disabled (`/_next/image` is a 404); the
  post-sign-in redirect only follows same-origin paths; the web container
  runs as the unprivileged `node` user.

`server/tools/authprobe/run.sh` replays the auth pentest checklist against a
production-configured server (it runs in CI); results and scope are in
[docs/release/auth-pentest.md](../docs/release/auth-pentest.md).

**Incident levers.** To end one user's sessions:
`update sessions set revoked_at = now(), revoked_reason = 'admin' where account_id = '<id>' and revoked_at is null;`
— their refresh stops at once, and their access token expires within the
TTL. To end every human session at once, rotate `CONVERGE_SESSION_SECRET`
and restart. That invalidates every session token immediately; agent API
tokens are stored as hashes, independent of the secret, and keep working.
Suspend an agent in Settings → Members to stop it.

## Coding agents (MCP)

An external coding agent (Claude Code, Cursor, Codex, or anything that
speaks MCP over streamable HTTP) works Converge issues through the MCP
endpoint at `https://<your-domain>/api/v1/mcp`.

1. Create an agent with the `external` driver and a narrow token: scopes
   `work`, `issues:write`, `comments:write`, limited to the teams it should
   work on. The Add agent dialog does not set the driver or scopes yet, so
   for now a workspace owner or admin runs this in the browser console while
   signed in to Converge (the session cookie authenticates it):
   ```js
   await (await fetch('/api/v1/workspaces/<workspaceId>/agents', {
     method: 'POST', headers: {'Content-Type': 'application/json'},
     body: JSON.stringify({name: 'coder', teamIds: ['<teamId>'], driver: 'external',
       token: {scopes: ['work', 'issues:write', 'comments:write'], teamIds: ['<teamId>'], ttlHours: 720}}),
   })).json()
   ```
   The response shows the token once. Keep it in an environment variable
   (`CONVERGE_TOKEN` below), not in a config file.
2. Point the client at the endpoint:
   - Claude Code:
     `claude mcp add --transport http converge https://<your-domain>/api/v1/mcp --header "Authorization: Bearer $CONVERGE_TOKEN"`
   - Cursor (`~/.cursor/mcp.json` or `.cursor/mcp.json`):
     ```json
     {"mcpServers": {"converge": {"url": "https://<your-domain>/api/v1/mcp",
       "headers": {"Authorization": "Bearer ${env:CONVERGE_TOKEN}"}}}}
     ```
   - Codex (`~/.codex/config.toml`):
     ```toml
     [mcp_servers.converge]
     url = "https://<your-domain>/api/v1/mcp"
     bearer_token_env_var = "CONVERGE_TOKEN"
     ```

The tools are `get_queue`, `claim_issue`, `heartbeat`, `report_progress`,
`release_claim`, `update_issue`, and `add_comment`; the server's
instructions tell the agent the workflow. Serve no OAuth discovery
documents (`/.well-known/oauth-*`) on the API's domain: some clients then
try OAuth instead of sending the configured header.

## Production checklist

- [ ] `CONVERGE_SESSION_SECRET` set to `openssl rand -hex 32` output (the API refuses to start otherwise)
- [ ] `CONVERGE_DEV_MODE=false` (dev mode hands out sign-in links; refused off localhost anyway)
- [ ] `CONVERGE_WEB_ORIGIN` / `CONVERGE_PUBLIC_URL` are the public https origins (Secure cookies and the Origin check follow from them)
- [ ] `CONVERGE_SMTP_HOST` set — **without it nobody can sign in** (sign-in links are mailed)
- [ ] `CONVERGE_TRUSTED_PROXIES` matches your proxy chain; every proxy appends `X-Forwarded-For`
- [ ] `COMPOSE_PROFILES` line removed from `.env` (no demo workspace)
- [ ] Postgres not reachable off the host (compose binds `127.0.0.1`; keep it that way)
- [ ] Reverse proxy terminates TLS and streams `/api/*` without buffering
- [ ] `CONVERGE_METRICS_ADDR`, if set, is not reachable through the public proxy
- [ ] Agent scripts send `If-Match` (see above)
- [ ] Tokens handed to external tools are scoped and team-limited (see above)
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

### Upgrading to the run-ledger release (migration 0022)

- **One run per existing claim.** The migration creates a run for every
  claim already on record, without usage figures; new claims record their
  own.
- **Web clients re-download their workspace once** after the upgrade, so
  they pick up the runs.
- **Rolling back past 0022 is safe.** Older builds ignore the run tables;
  runs recorded meanwhile stay in the database and reappear on upgrade.

### Upgrading to the work-API release (migration 0021)

- **Nothing changes for existing agents.** Their tokens keep full authority
  and their driver is `runtime`, so the built-in swarm keeps working them.
- **Rolling back past 0021 widens scoped tokens.** Older builds ignore scopes
  and team grants, so a narrowed token regains the agent's full authority,
  and they also run external agents through the built-in runtime. Before
  rolling back, revoke narrowed tokens and switch external agents to
  `runtime` (or suspend them).

### Upgrading to the session-rotation release (migration 0020)

- **Everyone signs in once.** The migration revokes existing sessions
  (`revoked_reason = 'upgrade'`), and tokens from older builds carry no
  session id, so they are refused. Agents are unaffected (API tokens).
  Rolling back also costs one sign-in: older builds do not accept the new
  token format.
- **Agent clients must send `If-Match`** on issue update/move/delete/handoff,
  or they get `428`. Read `version` from the Issue payload (or the `ETag`
  of the previous write). `server/tools/swarm` shows the pattern.
- **Cookie-authenticated scripts must send `Origin`** (see
  [Security behavior](#security-behavior)).
- **Production needs SMTP** (or `CONVERGE_MAIL_LOG_BODIES=true`) for anyone
  to sign in, since sign-in links are mailed and no longer logged by default.
- **Proxies must append `X-Forwarded-For`**, or the new auth rate limits
  treat every user as one client.

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
  buffer per subscriber. A subscriber that falls further behind is
  disconnected rather than silently skipped
  (`converge_realtime_slow_disconnects_total`). The client applies events
  strictly in sequence, so on reconnect it resumes from its last contiguous
  position via the delta endpoint. See [Scaling](#scaling).
- **Sessions table:** one row per sign-in; rows expired or revoked more than
  30 days ago are pruned on each sign-in. `revoked_reason` records why a
  session ended (`signout`, `reuse`, `upgrade`, or whatever an operator sets).
- **Outbox retention:** the last 2000 change records per workspace are
  retained; clients whose watermark is older than the retained window get
  `stale` and take a full bootstrap. This bounds the change feed regardless
  of team size.

## Metrics

Set `CONVERGE_METRICS_ADDR` (e.g. `127.0.0.1:9464`, or `:9464` inside a
container) to serve Prometheus text format at `GET /metrics` on a separate
listener. It is off by default, carries no authentication, and never shares
the public port — keep it off the public proxy. In compose, set
`CONVERGE_METRICS_ADDR=:9464` and scrape `api:9464` from a Prometheus on the
same Docker network, or publish it as `127.0.0.1:9464:9464`.

| Metric | Type | Meaning |
| --- | --- | --- |
| `converge_http_requests_total{method,route,code}` | counter | Requests by chi route pattern (bounded; unmatched → `unmatched`) |
| `converge_http_request_duration_seconds{method,route}` | histogram | Latency |
| `converge_http_requests_in_flight` | gauge | Concurrent requests (SSE streams included) |
| `converge_auth_events_total{event}` | counter | `signin`, `refresh`, `refresh_race`, `refresh_reuse`, `signout`, `rate_limited_ip`, `rate_limited_email`, `cross_origin_refused` |
| `converge_issue_precondition_failures_total{reason}` | counter | `missing` (428) / `stale` (412) |
| `converge_realtime_subscribers` | gauge | Open SSE streams on this instance |
| `converge_realtime_slow_disconnects_total` | counter | Streams closed for falling behind |
| `converge_db_pool_connections{state}` | gauge | `acquired` / `idle` / `constructing` |
| `converge_db_pool_max_connections` | gauge | Pool ceiling |
| `converge_db_pool_acquires_total`, `converge_db_pool_empty_acquires_total`, `converge_db_pool_acquire_seconds_total` | counter | Pool pressure: empty acquires waited for a connection |
| `converge_build_info{version,commit,goversion}` | gauge | Build stamp |
| `go_goroutines`, `go_memstats_heap_alloc_bytes`, `go_memstats_sys_bytes`, `go_gc_cycles_total`, `process_start_time_seconds` | mixed | Runtime |

Signals worth alerting on: any `refresh_reuse` (token theft, or a client
bug replaying tokens); a sustained rise in `rate_limited_ip` (spraying, or a
proxy not forwarding client IPs); `empty_acquires` growing with latency (pool
too small); `slow_disconnects` climbing (overloaded clients or network).

## Scaling

v1 is a single API instance by design:

- Refresh and sign-out are checked against the `sessions` table, so any
  instance can serve any user. Two things are per-instance: the auth rate
  limiters and the cache of revoked sessions. With several instances, a
  signed-out access token stays usable on the other instances until it
  expires (`CONVERGE_ACCESS_TOKEN_TTL`).
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
