-- 0022_agent_runs.sql — Phase 2: the run ledger.
-- spec cs:agents:runs
--
-- A run is one claim's worth of work by an external agent: it opens with
-- the claim (same id) and ends when the claim ends. The agent reports
-- what the run cost and what it did against it: usage as running totals
-- (a retried report never double-counts), a capped trace of step events,
-- and evidence links. Usage and cost are agent-reported; the ledger
-- records them, it does not verify them.
--
-- claim_id is nullable so a later run source (the in-process runtime)
-- can write runs without claims.
create table if not exists agent_runs (
  id             uuid primary key default gen_random_uuid(),
  workspace_id   uuid not null references workspaces (id) on delete cascade,
  issue_id       uuid not null references issues (id) on delete cascade,
  agent_id       uuid not null references accounts (id),
  claim_id       uuid unique references issue_claims (id) on delete cascade,
  started_at     timestamptz not null default now(),
  ended_at       timestamptz,
  end_reason     text check (end_reason in (
                   'released', 'expired', 'superseded', 'reassigned',
                   'paused', 'closed', 'revoked')),
  outcome        text check (outcome in ('done', 'failed', 'blocked', 'partial')),
  summary        text check (char_length(summary) between 1 and 2000),
  model          text check (char_length(model) between 1 and 100),
  input_tokens   bigint not null default 0 check (input_tokens between 0 and 1000000000000),
  output_tokens  bigint not null default 0 check (output_tokens between 0 and 1000000000000),
  cost_micros    bigint not null default 0 check (cost_micros between 0 and 1000000000000),
  event_count    int not null default 0 check (event_count between 0 and 1000),
  evidence       jsonb not null default '[]'::jsonb
                   check (jsonb_typeof(evidence) = 'array' and jsonb_array_length(evidence) <= 20),
  updated_at     timestamptz not null default now(),
  check ((ended_at is null) = (end_reason is null))
);
create index if not exists agent_runs_issue on agent_runs (issue_id, started_at);
create index if not exists agent_runs_workspace on agent_runs (workspace_id, started_at);
create index if not exists agent_runs_agent on agent_runs (agent_id, started_at desc);

-- Every claim has its run, including the ones taken before this
-- migration.
insert into agent_runs (id, workspace_id, issue_id, agent_id, claim_id, started_at, ended_at, end_reason)
select id, workspace_id, issue_id, agent_id, id, claimed_at, ended_at, end_reason
from issue_claims
on conflict do nothing;

-- The run's trace: append-only, numbered from 1, at most 1000 per run.
create table if not exists agent_run_events (
  run_id   uuid not null references agent_runs (id) on delete cascade,
  seq      int not null check (seq between 1 and 1000),
  at       timestamptz not null default now(),
  kind     text not null check (kind in ('step', 'tool', 'note', 'error')),
  message  text not null check (char_length(message) between 1 and 1000),
  primary key (run_id, seq)
);
