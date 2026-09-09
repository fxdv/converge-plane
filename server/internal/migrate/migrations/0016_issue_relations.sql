-- 0016_issue_relations.sql — v1.1: first-class issue relations.
-- spec cs:api:relations
--
-- One row per directed edge, stored from the ACTOR's perspective: the
-- edge belongs to the issue the relation was added from. PARENT /
-- SUB_ISSUE are client enum members but never reach this table —
-- hierarchy rides on issues.parent_id, the single source of truth.
--
-- Reads rewrite the edge from the reader's side (BLOCKS <-> BLOCKED,
-- DUPLICATE <-> DUPLICATE_OF; RELATED / SIMILAR are symmetric) so each
-- endpoint's record carries the perspective the reader sees, while the
-- stored edge keeps one canonical direction.
--
-- Soft delete: deleted_at set, row retained (the edge's trail stays in
-- issue_history); the partial unique index admits re-creation.
create table if not exists issue_relations (
    id               uuid primary key default gen_random_uuid(),
    workspace_id     uuid not null references workspaces (id) on delete cascade,
    team_id          uuid not null references teams (id) on delete cascade,
    issue_id         uuid not null references issues (id) on delete cascade,
    related_issue_id uuid not null references issues (id) on delete cascade,
    type             text not null check (type in ('BLOCKS','BLOCKED','RELATED','DUPLICATE','DUPLICATE_OF','SIMILAR')),
    metadata         jsonb not null default '{}'::jsonb,
    created_by       uuid references accounts (id) on delete set null,
    created_at       timestamptz not null default now(),
    updated_at       timestamptz not null default now(),
    deleted_at       timestamptz
);

-- One live edge per (issue, target, type): duplicate creation fails.
create unique index if not exists issue_relations_edge_ux
  on issue_relations (issue_id, related_issue_id, type)
  where deleted_at is null;

-- Reader-side reads: an edge appears on both endpoints' records.
create index if not exists issue_relations_related_idx
  on issue_relations (related_issue_id)
  where deleted_at is null;

-- Tenant scans (bootstrap, delta joins).
create index if not exists issue_relations_workspace_idx
  on issue_relations (workspace_id)
  where deleted_at is null;
