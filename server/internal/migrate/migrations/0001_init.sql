-- 0001_init.sql — Converge MVP domain schema
--
-- Conventions (see docs/spec/07-domain-and-permissions.md):
--   * Every tenant-owned object resolves to exactly one workspace,
--     directly or through an immutable parent relation.
--   * All timestamps are UTC instants; calendar-only dates (due dates,
--     cycles) use `date` when they land in later migrations.
--   * Opaque UUID primary keys; human keys (issue numbers, slugs,
--     identifiers) are unique aliases, never primary keys.
--   * Mutable rows carry a `version` token for optimistic concurrency.
--   * Destructive semantics are soft (status/state columns); hard deletes
--     exist only for cascade-clean child rows (comments, links, history).

create extension if not exists citext;
create extension if not exists pg_trgm;

-- ---------------------------------------------------------------- accounts

create table accounts (
  id           uuid primary key default gen_random_uuid(),
  email        citext not null unique,
  name         text not null default '',
  avatar_url   text,
  status       text not null default 'active' check (status in ('active', 'suspended')),
  version      integer not null default 0,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

-- Server-side sessions. The raw token is never stored, only its hash.
create table sessions (
  id            uuid primary key default gen_random_uuid(),
  account_id    uuid not null references accounts (id) on delete cascade,
  token_hash    text not null unique,
  user_agent    text,
  ip            inet,
  created_at    timestamptz not null default now(),
  last_seen_at  timestamptz not null default now(),
  expires_at    timestamptz not null,
  revoked_at    timestamptz
);
create index sessions_account_idx on sessions (account_id);
create index sessions_expiry_idx on sessions (expires_at);

-- ------------------------------------------------------------ organization

create table workspaces (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  slug        citext not null unique,
  status      text not null default 'active' check (status in ('active', 'suspended')),
  created_by  uuid references accounts (id),
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create table workspace_members (
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  account_id    uuid not null references accounts (id) on delete cascade,
  role          text not null default 'member' check (role in ('owner', 'admin', 'member')),
  status        text not null default 'active' check (status in ('active', 'suspended')),
  joined_at     timestamptz,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  primary key (workspace_id, account_id)
);
create index workspace_members_account_idx on workspace_members (account_id);
-- Invariant "at least one active owner" is enforced in the service layer
-- (last-owner demotion/removal is rejected); see docs/spec/07.

create table invitations (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  email         citext not null,
  role          text not null default 'member' check (role in ('admin', 'member')),
  token_hash    text not null unique,
  invited_by    uuid not null references accounts (id),
  team_ids      uuid[] not null default '{}',
  expires_at    timestamptz not null,
  consumed_at   timestamptz,
  revoked_at    timestamptz,
  created_at    timestamptz not null default now()
);
create index invitations_workspace_idx on invitations (workspace_id);
-- One live invitation per workspace+email; re-invite replaces the record.
create unique index invitations_live_uniq on invitations (workspace_id, email)
  where consumed_at is null and revoked_at is null;

create table teams (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  name          text not null,
  identifier    citext not null,
  status        text not null default 'active' check (status in ('active', 'archived')),
  position      integer not null default 0,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, identifier)
);
create index teams_workspace_idx on teams (workspace_id);

create table team_members (
  team_id     uuid not null references teams (id) on delete cascade,
  account_id  uuid not null references accounts (id) on delete cascade,
  role        text not null default 'member' check (role in ('manager', 'member')),
  created_at  timestamptz not null default now(),
  primary key (team_id, account_id)
);
create index team_members_account_idx on team_members (account_id);
-- A team member must be an active workspace member; enforced in the service
-- layer (a constraint trigger is a hardening candidate).

create table workflow_statuses (
  id          uuid primary key default gen_random_uuid(),
  team_id     uuid not null references teams (id) on delete cascade,
  name        text not null,
  category    text not null check (category in ('backlog', 'triage', 'todo', 'in_progress', 'done', 'canceled')),
  color       text not null default '#8884d8',
  position    integer not null default 0,
  status      text not null default 'active' check (status in ('active', 'archived')),
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  unique (team_id, name)
);
create index workflow_statuses_team_idx on workflow_statuses (team_id, position);

create table labels (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  name          citext not null,
  color         text not null default '#8884d8',
  status        text not null default 'active' check (status in ('active', 'archived')),
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, name)
);
create index labels_workspace_idx on labels (workspace_id);

-- ------------------------------------------------------------------- issues

create table issue_counters (
  team_id      uuid primary key references teams (id) on delete cascade,
  next_number  integer not null default 1
);

create table issues (
  id             uuid primary key default gen_random_uuid(),
  team_id        uuid not null references teams (id) on delete cascade,
  number         integer not null,
  title          text not null check (char_length(title) between 1 and 255),
  description    jsonb not null default 'null',
  status_id      uuid references workflow_statuses (id) on delete restrict,
  priority       integer not null default 2 check (priority between 1 and 4),
  assignee_id    uuid references accounts (id) on delete set null,
  parent_id      uuid references issues (id) on delete set null,
  sort_order     numeric not null default 0,
  status         text not null default 'active' check (status in ('active', 'archived', 'deleted')),
  version        integer not null default 0,
  created_by     uuid references accounts (id),
  completed_at   timestamptz,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  search_text    text not null generated always as
                   (coalesce(title, '') || ' ' || coalesce(description ->> 'plain', '')) stored,
  unique (team_id, number)
);
create index issues_team_status_idx on issues (team_id, status_id) where status = 'active';
create index issues_assignee_idx on issues (assignee_id) where assignee_id is not null;
create index issues_parent_idx on issues (parent_id) where parent_id is not null;
create index issues_created_idx on issues (team_id, created_at desc) where status = 'active';
create index issues_updated_idx on issues (team_id, updated_at desc) where status = 'active';
create index issues_search_idx on issues using gin (search_text gin_trgm_ops);

create table issue_labels (
  issue_id  uuid not null references issues (id) on delete cascade,
  label_id  uuid not null references labels (id) on delete cascade,
  primary key (issue_id, label_id)
);
create index issue_labels_label_idx on issue_labels (label_id);

create table comments (
  id          uuid primary key default gen_random_uuid(),
  issue_id    uuid not null references issues (id) on delete cascade,
  author_id   uuid not null references accounts (id) on delete cascade,
  body        jsonb not null,
  parent_id   uuid references comments (id) on delete cascade,
  status      text not null default 'active' check (status in ('active', 'deleted')),
  version     integer not null default 0,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
create index comments_issue_idx on comments (issue_id, created_at);
create index comments_author_idx on comments (author_id);

-- A reply must live on the same issue as its parent.
create or replace function check_comment_parent_same_issue()
returns trigger language plpgsql as $$
begin
  if new.parent_id is not null then
    if not exists (
      select 1 from comments p
      where p.id = new.parent_id and p.issue_id = new.issue_id
    ) then
      raise exception 'comment parent does not belong to the same issue';
    end if;
  end if;
  return new;
end;
$$;

create trigger comments_parent_same_issue
before insert or update of parent_id on comments
for each row execute function check_comment_parent_same_issue();

-- ------------------------------------------------- activity and audit trail

-- User-visible issue activity (feeds the issue Activity tab).
create table issue_history (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  team_id       uuid references teams (id) on delete cascade,
  issue_id      uuid not null references issues (id) on delete cascade,
  actor_id      uuid references accounts (id),
  actor_type    text not null default 'user' check (actor_type in ('user', 'system')),
  action        text not null,
  field         text,
  from_value    text,
  to_value      text,
  request_id    text,
  created_at    timestamptz not null default now()
);
create index issue_history_issue_idx on issue_history (issue_id, created_at desc);

-- Workspace-level security/audit log (membership changes, destructive
-- operations). Append-only; application must not update or delete rows.
create table audit_events (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  team_id       uuid references teams (id) on delete cascade,
  actor_id      uuid references accounts (id),
  actor_type    text not null default 'user' check (actor_type in ('user', 'system')),
  action        text not null,
  object_type   text not null,
  object_id     uuid,
  before        jsonb,
  after         jsonb,
  request_id    text,
  created_at    timestamptz not null default now()
);
create index audit_events_workspace_idx on audit_events (workspace_id, created_at desc);

-- --------------------------------------------------------- async processing

-- Transactional outbox: written inside domain transactions, leased by the
-- worker for realtime fan-out and future async jobs.
create table outbox_events (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references workspaces (id) on delete cascade,
  type             text not null,
  schema_version   integer not null default 1,
  aggregate_type   text not null,
  aggregate_id     uuid not null,
  aggregate_version integer not null default 0,
  actor_id         uuid,
  payload          jsonb not null default '{}'::jsonb,
  correlation_id   text,
  state            text not null default 'pending' check (state in ('pending', 'leased', 'done', 'dead')),
  attempts         integer not null default 0,
  scheduled_at     timestamptz not null default now(),
  leased_at        timestamptz,
  leased_by        text,
  completed_at     timestamptz,
  created_at       timestamptz not null default now()
);
create index outbox_events_pending_idx on outbox_events (scheduled_at) where state in ('pending', 'leased');
create index outbox_events_workspace_idx on outbox_events (workspace_id, created_at desc);

-- -------------------------------------------------------------------- views

create table saved_views (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  team_id       uuid references teams (id) on delete set null,
  name          text not null check (char_length(name) between 1 and 100),
  description   text,
  definition    jsonb not null default '{}'::jsonb,
  visibility    text not null default 'workspace' check (visibility in ('workspace', 'private')),
  created_by    uuid not null references accounts (id),
  status        text not null default 'active' check (status in ('active', 'archived')),
  position      integer not null default 0,
  version       integer not null default 0,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, name)
);
create index saved_views_workspace_idx on saved_views (workspace_id);

-- ------------------------------------------------------- user preferences

-- Per-user display preferences. A null workspace_id marks a global
-- preference (theme, density); a set workspace_id scopes it to one
-- workspace (per-view layout, grouping, ordering).
create table preferences (
  account_id    uuid not null references accounts (id) on delete cascade,
  workspace_id  uuid references workspaces (id) on delete cascade,
  key           text not null,
  value         jsonb not null default '{}'::jsonb,
  updated_at    timestamptz not null default now(),
  primary key (account_id, workspace_id, key)
);
