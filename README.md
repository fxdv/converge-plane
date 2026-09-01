# Converge

**A fast, self-hostable, hybrid task tracker for human and agentic swarm teams.**

Converge gives small and medium engineering teams the common work loop done
well: capture, prioritize, assign, discuss, and complete — with an
excellent list and Kanban experience, team-scoped organization, keyboard
efficiency, and a deliberately small operating footprint (one Go binary +
PostgreSQL).

It is built for the shape of team engineering is moving toward: work done by
people *and* by agents, in the same place. In a Converge workspace a human
can drag a card into review, and an agent can pull that same card, do the
work, and report back through the same API. There is no parallel "bot tool"
to keep in sync with the board — the issue tracker *is* the coordination
surface for the whole workforce.

## Why it works for mixed workforces

- **One surface, every kind of worker.** The web app for humans and the REST
  API + server-sent-event stream for machines are two views of the same
  server-authoritative state. What an agent changes appears on the board;
  what a human changes reaches the agent in real time.
- **Versioned and audited.** Every mutable object carries a version, so
  concurrent writers fail loudly instead of silently overwriting each other.
  Administrative changes — teams, membership, invites, suspension — land in
  an append-only audit trail with actor, object, and timestamp, so agent
  action is attributable and reviewable.
- **Decomposition that scales to swarms.** Sub-issues, labels, and statuses
  let a lead agent fan work out across a swarm and collect the results,
  while a human keeps one readable view of progress.
- **Scoped by design.** The domain model reserves explicit machine
  principals — service tokens and integration identities with team grants,
  expiry, and least privilege — so an agent acts with exactly the rights its
  task needs and never inherits its creator's full authority
  ([docs/spec/07](docs/spec/07-domain-and-permissions.md)).

## Features

- Issues with team identifiers, statuses, priorities, assignees, labels, and sub-issues
- List and Kanban views with real-time updates (server-sent events)
- Saved views and full-text search
- Comments and activity history
- Teams with their own workflows; workspace administration with invites, roles, and member suspension
- Magic-link sign-in; workspace onboarding and roles
- Self-host with `docker compose up --build` — one app plus PostgreSQL

Coming up next: projects, cycles, notifications, issue relations, and
attachments, followed by a versioned public API, webhooks, and scoped
machine identities for long-running agent work. See
[docs/spec/09-mvp-roadmap.md](docs/spec/09-mvp-roadmap.md).

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

1. **Read** the workspace — sync endpoints for snapshots and deltas, REST
   filters and full-text search for targeted reads.
2. **Act** on a card — claim it, move it through the workflow, tag it, split
   it into sub-issues. Versioned mutations make conflicts explicit.
3. **Report** in comments. The activity timeline and the SSE stream carry
   the result to every human watching, and the audit trail records who did
   what.

Today agents authenticate the same way humans do (magic-link sessions);
scoped service tokens with per-team grants and expiry are the planned
primitive for long-running agent work, landing with the public API.

## Development

```sh
# prerequisites: Go 1.25+, Node 20+, pnpm 10
cp .env.example .env
docker compose up -d postgres          # database only
cd server && go run ./cmd/converge     # API on :3001
cd .. && pnpm install && pnpm dev      # web on :3000
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
