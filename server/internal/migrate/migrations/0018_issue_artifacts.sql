-- 0018_issue_artifacts.sql — SWR-56: the swarm's artifact channel.
-- spec cs:swarm:handoff
--
-- A document the swarm posts to an issue (audits, plans, manifests):
-- long-form by definition, so it is its own record, never a comment
-- (the comment timeline stays step text; the 500-rune comment cap is
-- noise control, not document control). It cascade-cleans with the
-- issue, like comments and history.
--
-- The body is plain text (a fenced markdown/JSON document the client
-- renders preformatted), not a jsonb rich-text envelope. The DB-level
-- cap mirrors the runtime's fence (8 KB, inclusive of the truncation
-- marker — the fence guarantees it): a document that fits the fleet's
-- output budget is never truncated, and nothing larger can enter the
-- table even through a non-runtime path.
create table if not exists issue_artifacts (
  id          uuid primary key default gen_random_uuid(),
  issue_id    uuid not null references issues (id) on delete cascade,
  author_id   uuid not null references accounts (id) on delete cascade,
  title       text not null check (char_length(title) between 1 and 200),
  body        text not null check (octet_length(body) between 1 and 8192),
  status      text not null default 'active' check (status in ('active', 'deleted')),
  version     integer not null default 0,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
create index issue_artifacts_issue_idx on issue_artifacts (issue_id, created_at);
create index issue_artifacts_author_idx on issue_artifacts (author_id);
