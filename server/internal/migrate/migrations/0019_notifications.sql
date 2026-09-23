-- 0019_notifications.sql — SWR-13: the in-app inbox (docs/spec 12).
-- spec cs:swarm:inbox
--
-- A notification is a derived, disposable record: a pointer to the
-- issue's own trace, which is the source of truth. One row is one
-- recipient's one pending (or read) nudge — never a copy of the event.
--
-- Dedup is in the key: (issue, type, actor) bucketed by a 24h epoch, so
-- a 20-comment thread collapses into one pending row (the newest event
-- refreshes it in place) and a new day starts a new one. The partial
-- unique index covers pending rows only: once the recipient reads the
-- row, the next event in the same bucket INSERTs a fresh pending row
-- instead of resurrecting a read one.
create table if not exists notifications (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  issue_id      uuid not null references issues (id) on delete cascade,
  account_id    uuid not null references accounts (id) on delete cascade,
  issue_number  integer not null,
  type          text not null check (type in
                ('assigned', 'reassigned', 'handoff', 'state', 'comment',
                 'pause', 'mention', 'closed')),
  actor_id      uuid,
  actor_name    text,
  dedup_key     text not null,
  created_at    timestamptz not null default now(),
  read_at       timestamptz
);

-- The upsert target: one pending row per (workspace, recipient, dedupKey).
create unique index notifications_pending_dedup_idx
  on notifications (workspace_id, account_id, dedup_key)
  where read_at is null;

-- The inbox read: the recipient's rows for the workspace, newest first.
create index notifications_inbox_idx
  on notifications (account_id, workspace_id, created_at desc);

-- The workspace collector (bootstrap): the tenant's rows, newest first.
create index notifications_workspace_idx
  on notifications (workspace_id, created_at desc);

-- The 90-day retention purge.
create index notifications_retention_idx on notifications (created_at);
