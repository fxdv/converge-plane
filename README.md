# Converge

**A fast, self-hostable issue tracker for software teams.**

Converge gives small and medium engineering teams the common issue loop done well:
capture, prioritize, assign, discuss, and complete work — with an
excellent list and Kanban experience, team-scoped organization, keyboard
efficiency, and a deliberately small operating footprint (one Go binary +
PostgreSQL).

## Features

- Issues with team identifiers, statuses, priorities, assignees, labels, and sub-issues
- List and Kanban views with real-time updates (server-sent events)
- Comments and activity history
- Magic-link sign-in; workspace onboarding and roles
- Self-host with `docker compose up --build` — one app plus PostgreSQL

Team/workspace administration (invites, suspension, saved views, full-text
search) is landing in the next release; the schema and client are already in
place. See [docs/spec/09-mvp-roadmap.md](docs/spec/09-mvp-roadmap.md).

## Quickstart

```sh
git clone <this-repo> converge && cd converge
cp .env.example .env
docker compose up --build
```

Then open <http://localhost:3000>, sign in with `demo@converge.dev`
(dev mode shows the magic link directly since no email provider is
configured), and explore the seeded **Acme** workspace.

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
