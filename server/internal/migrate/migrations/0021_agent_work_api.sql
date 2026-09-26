-- 0021_agent_work_api.sql — Phase 2: scoped agent tokens and the work API.
--
-- Scopes and team grants narrow what one token may do. NULL keeps the
-- pre-0021 meaning (the agent's full authority), so tokens issued before
-- this migration keep working unchanged. A token can never exceed its
-- agent: grants only subtract.
alter table api_tokens
  add column if not exists scopes text[],
  add column if not exists team_ids uuid[];

-- Who drives an agent. 'runtime' is the in-process runtime (the pre-0021
-- behavior for every agent); 'external' is a client working through the
-- work API. The runtime never acts for an external agent, and only an
-- external agent may claim work.
alter table accounts
  add column if not exists agent_driver text not null default 'runtime'
    check (agent_driver in ('runtime', 'external'));

-- The claim ledger. One open claim (ended_at is null) per issue at a
-- time; an open claim whose expires_at has passed is dead and is ended
-- by the next claim or the sweep. Rows are never deleted, so the ledger
-- records who worked what, when, and how each lease ended.
create table if not exists issue_claims (
  id            uuid primary key default gen_random_uuid(),
  issue_id      uuid not null references issues (id) on delete cascade,
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  agent_id      uuid not null references accounts (id),
  token_id      uuid references api_tokens (id),
  mode          text not null check (mode in ('assigned', 'pool')),
  ttl_seconds   int not null check (ttl_seconds between 30 and 900),
  claimed_at    timestamptz not null default now(),
  heartbeat_at  timestamptz not null default now(),
  expires_at    timestamptz not null,
  ended_at      timestamptz,
  end_reason    text check (end_reason in (
                  'released', 'expired', 'superseded', 'reassigned',
                  'paused', 'closed', 'revoked')),
  check ((ended_at is null) = (end_reason is null))
);
create unique index if not exists issue_claims_open_issue
  on issue_claims (issue_id) where ended_at is null;
create index if not exists issue_claims_open_expiry
  on issue_claims (expires_at) where ended_at is null;
create index if not exists issue_claims_agent
  on issue_claims (agent_id, claimed_at desc);
