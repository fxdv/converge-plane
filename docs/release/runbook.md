# Converge operations runbook

The day-to-day for running a Converge instance with a swarm attached.
Everything below is the local dogfood topology; the same knobs scale to a
server.

## Stack

| Piece | Local | Production |
|---|---|---|
| Database | `converge-postgres` container (postgres:16, `127.0.0.1:5432`) | managed or containerized Postgres 16 |
| API | `/tmp/converge-api` on `:3001` | the built binary (`go build ./cmd/converge`) behind TLS |
| Web | `next dev` on `:3000` | `next build && next start`, origin = `CONVERGE_WEB_ORIGIN` |
| Swarm brain | 4 self-hosted 27B instances on `:8000-8003` | any OpenAI-compatible endpoints |

## Environment (the 12 knobs)

| Variable | Meaning |
|---|---|
| `CONVERGE_DATABASE_URL` | Postgres DSN |
| `CONVERGE_HTTP_ADDR` | API listen address (`:3001`) |
| `CONVERGE_PUBLIC_URL` | externally reachable API URL (links, absolute URLs) |
| `CONVERGE_WEB_ORIGIN` | CORS/origin allowlist for the client |
| `CONVERGE_SESSION_SECRET` | session cookie signing — rotate on leak |
| `CONVERGE_DEV_MODE` | dev magic-link sign-in — **off in production** |
| `CONVERGE_LOG_LEVEL` | `info` for production |
| `CONVERGE_LLM` | `true` to attach the model fleet |
| `CONVERGE_LLM_URLS` | comma-separated OpenAI-compatible endpoints (sharded per agent) |
| `CONVERGE_LLM_MODEL` | model name sent in requests |
| `CONVERGE_LLM_TIMEOUT` | per-decision deadline (90s) |
| `CONVERGE_LLM_MAX_TOKENS` | completion budget per decision |

Migrations run at boot, forward-only. The swarm runtime starts with the API;
topology and foreman can then be overridden per workspace from the Swarm
page without a restart.

## Swarm operations

- **Brain health.** If every `CONVERGE_LLM_URLS` endpoint is down, decisions
  fall back to the deterministic floor — the swarm keeps moving but without
  judgment. Watch the log for `llm policy fell back` and
  `llm endpoint ... connection refused`.
- **Remote GPU fleets.** Tunnels (`ssh -f -N -L 8000:localhost:8000 ...`) die
  with the machine that opened them (sleep, reboot, session end). Re-establish
  before letting the swarm do LLM work; verify each endpoint with
  `GET /v1/models`.
- **Steering.** Swarm page → Fleet settings: topology (foreman/flat) + foreman
  designation. Only workspace owners/admins can save; agents get a 422. The
  change is effective on the swarm's next decision — no restart.
- **Roster.** Settings → Members: create, suspend, delete agents. Suspended
  agents are excluded from the fleet immediately; a suspended designated
  foreman degrades to the auto rule (oldest agent).
- **Human Review column.** No agent works cards there. The resume gesture is a
  human move back into the workflow; the model reads human comments on resume
  (fenced as data, never as instructions). A card that cannot be finished is
  left there with its "where things stand" note until a human decides.
- **Needs-human queue.** Swarm page and the board panel list paused cards with
  the short reason; the full note lives in the card's handoff comment.

## Monitoring (log lines that matter)

| Line | Meaning |
|---|---|
| `agent action` | every swarm step (advance / pause / handoff / comment) |
| `issue paused for human review (escalation)` | honest escalation — a human should look |
| `issue paused by runtime guard` | policy limit (token window / quiet guard), not a failure |
| `llm policy fell back to the deterministic policy` | the brain is offline or the model's proposal failed validation — the floor is driving |
| `llm policy decided` (debug) | model verdict + token spend |
| `agent runtime started ... policy:` | which brain is active at boot |

The Swarm page's roster shows the live view: per-agent busy / open / paused /
tokens24h, the effective foreman, and the work-waiting-on-you queue.

## Releasing

1. Cut the release from a clean tree at the milestone's last commit.
2. Write `docs/release/vX.Y.Z.md` (what shipped, known limitations) and the
   runbook updates.
3. `git tag -a vX.Y.Z -m "..."` — the tag is the release pointer.
4. Redeploy: server binary first, then web; the boot log must show
   `migrations applied` and the intended `policy:` line.

## Rollback

Redeploy the previous tag. Migrations are forward-only; v1.x migrations are
additive (new tables/columns, no destructive rewrites), so an older binary
runs on a newer database for the v1 series. Agent runtime state is derived
from the issues table, so a rollback changes the swarm's brain, not its work.
