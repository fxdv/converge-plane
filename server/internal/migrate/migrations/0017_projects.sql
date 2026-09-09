-- 0017_projects.sql — v1.1: Projects (workspace-scoped meaning labels).
-- spec cs:api:projects
--
-- A project groups issues on the board: a grouping, never a state.
-- Membership lives on the issue (project_ids, at most one in v1 — the
-- array shape keeps the v2 multi-project door open) and is changed
-- through the issue patch, so a deleted project clears its members in
-- the same transaction.
--
-- Soft delete: deleted_at set, row retained; the partial unique index
-- (workspace, lower(name)) admits name reuse after a delete.
create table if not exists projects (
    id           uuid primary key default gen_random_uuid(),
    workspace_id uuid not null references workspaces (id) on delete cascade,
    name         text not null check (char_length(name) between 1 and 100),
    color        text not null default '#8884d8',
    description  text,
    start_date   date,
    end_date     date,
    teams        uuid[] not null default '{}',
    lead_id      uuid references accounts (id) on delete set null,
    created_by   uuid references accounts (id) on delete set null,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now(),
    deleted_at   timestamptz
);

-- One live project per (workspace, name), case-insensitive.
create unique index if not exists projects_name_ux
  on projects (workspace_id, lower(name))
  where deleted_at is null;

-- Tenant scans (list, bootstrap).
create index if not exists projects_workspace_idx
  on projects (workspace_id)
  where deleted_at is null;

-- Membership: the issue's project. The check constraint is the v1
-- single-project rule at the database level (the API enforces it too,
-- for a clean 422 instead of an integrity 500).
alter table issues add column if not exists project_ids uuid[] not null default '{}';
alter table issues add constraint issues_single_project check (cardinality(project_ids) <= 1);
create index if not exists issues_project_idx
  on issues (project_ids)
  where cardinality(project_ids) > 0;
