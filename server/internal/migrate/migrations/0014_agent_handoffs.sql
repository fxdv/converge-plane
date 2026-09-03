-- 0014_agent_handoffs.sql — D1: the agent handoff protocol (spec 12)
--
-- A handoff is an atomic work transition between agents on an issue:
-- new assignee + state move + bounded summary, one board-visible event.
-- The table is the trace (spec 12: "the trace is the product") and the
-- substrate for the swarm panel (D2); issue_history mirrors every
-- handoff as a normal activity row so the timeline shows it everywhere
-- history does.
--
-- Escalation: when the quiet guards fire (loop or operation budget) the
-- issue is paused (issues.agent_paused): no agent may act on it, humans
-- act freely, and a human mutation resumes it (docs/spec/12).

-- Escalation flag. v1 rule: additive-only.
alter table issues
  add column agent_paused boolean not null default false;

-- The handoff summary rides the history row so the client timeline
-- renders it (issue_history gains one nullable column; existing rows
-- keep null).
alter table issue_history
  add column summary text;

create table issue_handoffs (
  id               uuid primary key default gen_random_uuid(),
  workspace_id     uuid not null references workspaces (id) on delete cascade,
  issue_id         uuid not null references issues (id) on delete cascade,
  from_account_id  uuid not null references accounts (id) on delete restrict,
  to_account_id    uuid not null references accounts (id) on delete restrict,
  state_id         uuid references workflow_statuses (id) on delete restrict,
  summary          text not null,
  created_at       timestamptz not null default now()
);

-- The guards and the trace both read per issue, newest first.
create index issue_handoffs_issue_idx on issue_handoffs (issue_id, created_at desc);
-- Tenant-scoped queries (swarm panel, D2).
create index issue_handoffs_workspace_idx on issue_handoffs (workspace_id, created_at desc);
-- Who is busy: per-agent handoff activity (D2 roster "last handoff").
create index issue_handoffs_to_idx on issue_handoffs (to_account_id, created_at desc);
