-- 0023_issue_pull_requests.sql — Phase 2: GitHub pull request links.
-- spec cs:agents:prlinks
--
-- An external agent reports the pull request it opened as run evidence.
-- When the repository is on the operator's allowlist, the link becomes a
-- row here and the server polls GitHub for the PR's real state: the
-- evidence says what the agent claims, this table says what GitHub says.
--
-- repo and number are parsed from the reported URL and validated; the
-- URL itself is never fetched. repo is lower-cased (GitHub names are
-- case-insensitive), so one PR is one row per issue however it was
-- spelled.
--
-- next_check_at is when the poller looks next; null means it no longer
-- looks (merged, closed, or given up after repeated failures). Reporting
-- the PR again re-arms it.
create table if not exists issue_pull_requests (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references workspaces (id) on delete cascade,
  issue_id       uuid not null references issues (id) on delete cascade,
  repo           text not null check (repo ~ '^[a-z0-9][a-z0-9-]{0,38}/[a-z0-9._-]{1,100}$'),
  number         integer not null check (number > 0),
  linked_by      uuid references accounts (id) on delete set null,
  run_id         uuid references agent_runs (id) on delete set null,
  state          text not null default 'pending'
                   check (state in ('pending', 'open', 'merged', 'closed', 'unavailable')),
  draft          boolean not null default false,
  title          text check (char_length(title) between 1 and 200),
  merged_at      timestamptz,
  etag           text check (char_length(etag) <= 200),
  checked_at     timestamptz,
  next_check_at  timestamptz default now(),
  failures       integer not null default 0 check (failures >= 0),
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  unique (issue_id, repo, number)
);
create index if not exists issue_pull_requests_due
  on issue_pull_requests (next_check_at) where next_check_at is not null;
create index if not exists issue_pull_requests_workspace
  on issue_pull_requests (workspace_id, created_at);
