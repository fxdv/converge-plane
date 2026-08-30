# Converge

**A fast, self-hostable issue tracker for software teams.**

Converge gives small and medium engineering teams the common issue loop done well:
capture, prioritize, assign, discuss, find, and complete work — with an
excellent list and Kanban experience, team-scoped organization, keyboard
efficiency, and a deliberately small operating footprint (one Go binary +
PostgreSQL).

## Features

- Issues with team identifiers, statuses, priorities, assignees, and labels
- List and Kanban views with grouping, filtering, and saved views
- Comments, activity history, and sub-issues
- Workspace and team membership with roles, invites, and suspension
- Full-text search (PostgreSQL-backed, no extra services)
- Realtime updates over server-sent events
- Self-host with `docker compose up` — one app plus PostgreSQL

## Quickstart

```sh
git clone <this-repo> converge && cd converge
cp .env.example .env
docker compose up --build
```

Then open <http://localhost:3000> and sign in.

> The sign-in flow, database seeding, and onboarding are being built out in
> the first milestones; see [docs/spec](docs/spec) for the product
> specification and roadmap.

## Development

```sh
# prerequisites: Go 1.24+, Node 20+, pnpm 10
docker compose up -d postgres          # database only
cd server && go run ./cmd/converge       # API on :3001
cd .. && pnpm install && pnpm dev      # web on :3000
```

### Repository layout

| Path | Contents |
| --- | --- |
| `server/` | Go API server (single binary): HTTP, auth, domain services, migrations |
| `web/` | Web application (Next.js) — forked from Tegon, being adapted for Converge |
| `packages/` | Shared workspace packages (types, UI kit, API client) forked from Tegon |
| `tooling/` | Workspace lint/typecheck configs |
| `docs/spec/` | Product specification (authoritative) and delivery roadmap |
| `deploy/` | Deployment and operations material |

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
