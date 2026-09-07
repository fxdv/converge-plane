-- 0015_swarm_settings.sql — D4: the swarm plane's fleet settings (spec 12).
--
-- The topology is a deploy-time env (CONVERGE_RUNTIME_TOPOLOGY) that this
-- row overrides per workspace when present: a human decides the swarm's
-- coordination pattern from the Swarm page, and the decision takes
-- effect on the next decision — no restart, no redeploy.
--
-- foreman_account_id is the human's foreman designation (an active agent
-- member); null = auto (the fleet's oldest active agent, the tenure
-- rule). A deleted/suspended designation degrades to auto: the column
-- never blocks the swarm.
create table if not exists swarm_settings (
    workspace_id     uuid primary key references workspaces (id) on delete cascade,
    topology         text not null default 'foreman' check (topology in ('foreman', 'flat')),
    foreman_account_id uuid references accounts (id) on delete set null,
    created_at       timestamptz not null default now(),
    updated_at       timestamptz not null default now()
);

create index if not exists swarm_settings_foreman_idx
  on swarm_settings (foreman_account_id);
