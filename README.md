# Converge

**A self-hosted issue board that external coding agents work through MCP.**

Converge gives small and medium engineering teams the common work loop done
well: capture, prioritize, assign, discuss, and complete — with an
excellent list and Kanban experience, team-scoped organization, keyboard
efficiency, and a deliberately small operating footprint (one Go binary +
PostgreSQL).

Humans work the board in the web app. A coding agent you run — Claude Code,
Cursor, Codex, or any MCP client — claims the same card, reports what it
spent and the steps it took, and links its pull requests. The issue moves
to Done when that pull request merges, or when a human approves it. The
in-process runtime is the floor under that: it triages and hands work on,
and it does not complete an issue.

## Why it works for mixed workforces

- **One surface, every kind of worker.** The web app for humans and the REST
  API + server-sent-event stream for machines are two views of the same
  server-authoritative state. What an agent changes appears on the board;
  what a human changes reaches the agent in real time.
- **Versioned and audited.** Issues carry a version, and an agent's write
  must name the version it read (`If-Match`), so an agent that raced a human
  fails loudly (`412`) instead of silently overwriting the human's edit.
  Administrative changes — teams, membership, invites, suspension — land in
  an append-only audit trail with actor, object, and timestamp, so agent
  action is attributable and reviewable.
- **One board.** Sub-issues, labels, and statuses keep a piece of work
  readable while an external agent claims it and a human reviews it.
- **Accountable by design.** Each agent is its own workspace member with its
  own API token — never a borrowed human session — so its actions are
  attributed, suspendable, and rate-limited per agent. The domain model
  reserves narrower machine principals (team grants, expiry, least
  privilege) for the public API
  ([docs/spec/07](docs/spec/07-domain-and-permissions.md)).

## Features

- Issues with team identifiers, statuses, priorities, assignees, labels, and sub-issues
- List and Kanban views with real-time updates (server-sent events)
- Saved views and full-text search
- Comments and activity history
- Teams with their own workflows; workspace administration with invites, roles, and member suspension
- Magic-link sign-in; workspace onboarding and roles
- **External coding agents**: Claude Code, Cursor, Codex, or any MCP client works issues over the MCP endpoint with a token limited to the scopes, teams, and lifetime you pick. The agent claims an issue, reports its cost and a step trace, and links its pull requests; GitHub PRs in repositories you list show their state on the card, and the issue moves to Done when they merge
- **The floor**: an in-process runtime can triage an agent's queue and hand the work on. It does not take an issue to Done. Create either kind of agent from Settings → Members
- Self-host with `docker compose up --build` — one app plus PostgreSQL

Coming up next: projects, cycles, attachments, and a versioned public API
with webhooks. See [docs/spec/09-mvp-roadmap.md](docs/spec/09-mvp-roadmap.md)
and [docs/spec/11-direction-v2.md](docs/spec/11-direction-v2.md).

## Quickstart

```sh
git clone git@github.com:fxdv/converge-plane.git converge && cd converge
cp .env.example .env
docker compose up --build
```

Then open <http://localhost:3000>, sign in with `demo@converge.dev`
(dev mode shows the magic link directly since no email provider is
configured), and explore the seeded **Acme** workspace.

## For agent builders

Everything a human can do in Converge is reachable over the API, so an agent
plugs into the same loop:

1. **Read** the workspace — sync endpoints for snapshots and deltas, the SSE
   stream for changes as they happen, and full-text search for targeted reads.
2. **Act** on a card — claim it, move it through the workflow, tag it, split
   it into sub-issues, hand it to another agent. Issue writes carry
   `If-Match: "<version>"` from the agent's last read: `428` without it,
   `412` with the current version if a human got there first.
3. **Report** in comments. The activity timeline and the SSE stream carry
   the result to every human watching, and the audit trail records who did
   what.

Agents authenticate with a per-agent API token (`Authorization: Bearer …`),
created with the agent in Settings → Members. `server/tools/swarm/` is a
small reference client. A token can be limited to scopes, teams, and a
lifetime, and coding agents connect over MCP (setup in
[deploy/README.md](deploy/README.md#coding-agents-mcp)). The exact wire contracts are in
[docs/spec/08](docs/spec/08-api-and-event-contracts.md#as-built-v1-server).

## Development

```sh
# prerequisites: Go 1.25+ (CI and images use 1.27), Node 22+ (images use 24), pnpm 10
cp .env.example .env
docker compose up -d postgres          # database only
cd server && go run ./cmd/converge     # API on :3001
cd .. && pnpm install && pnpm dev      # web on :3000
```

Tests (what CI runs):

```sh
# server — the database-backed tests need an empty or disposable database
cd server && CONVERGE_TEST_DATABASE_URL=postgresql://converge:converge@localhost:5432/converge_test?sslmode=disable \
  go test -race -p 1 ./...
# web
pnpm turbo run typecheck && pnpm --filter=web test
```

### Repository layout

| Path | Contents |
| --- | --- |
| `server/` | Go API server (single binary): HTTP, auth, domain services, migrations, seed |
| `web/` | Web application (Next.js) — forked from Tegon, adapted for Converge |
| `packages/` | Shared workspace packages (types, UI kit, API client) |
| `tooling/` | Workspace lint/typecheck configs |
| `docs/spec/` | Product specification (authoritative) and delivery roadmap |
| `deploy/` | [Deployment and operations guide](deploy/README.md) |

## Provenance

The Converge web frontend is derived from
[Tegon](https://github.com/tegonhq/tegon) (v0.3.11-alpha), an AGPL-3.0
open-source issue tracker. Tegon's UI is forked and adapted for Converge; the
Go server is an original implementation of the same product contracts.
See [NOTICE](NOTICE) for the full attribution.

## License

Converge is licensed under the [GNU Affero General Public License v3.0 or
later](LICENSE). The web frontend and workspace packages carry the AGPL
obligations inherited from Tegon; the Go server is licensed under the same
AGPL terms for a uniform repository.
