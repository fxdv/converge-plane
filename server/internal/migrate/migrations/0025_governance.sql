-- 0025_governance.sql — Phase 3 evidence gate and Phase 2 outbound webhooks.
-- spec cs:agents:evidence
-- spec cs:agents:webhooks
--
-- An agent may not move an issue to Done unless a linked pull request
-- has merged (and none is still open) or a human has approved that move.
-- The approval is cleared when the issue leaves Done, so the next close
-- needs proof again.
--
-- Outbound webhooks are operator endpoints. The signing secret is stored
-- so deliveries can be signed; the API returns it once, at creation or
-- rotation, and never writes it to a log.

alter table issues add column if not exists done_approved_at timestamptz;
alter table issues add column if not exists done_approved_by uuid references accounts (id) on delete set null;

create table if not exists webhook_endpoints (
    id           uuid primary key default gen_random_uuid(),
    workspace_id uuid not null references workspaces (id) on delete cascade,
    url          text not null,
    secret       text not null,
    enabled      boolean not null default true,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);

create index if not exists webhook_endpoints_workspace_idx
    on webhook_endpoints (workspace_id) where enabled;

create table if not exists webhook_events (
    id              uuid primary key default gen_random_uuid(),
    workspace_id    uuid not null references workspaces (id) on delete cascade,
    event           text not null,
    payload         jsonb not null,
    attempts        int not null default 0,
    next_attempt_at timestamptz not null default now(),
    delivered_at    timestamptz,
    last_error      text,
    created_at      timestamptz not null default now()
);

create index if not exists webhook_events_due_idx
    on webhook_events (next_attempt_at)
    where delivered_at is null;
