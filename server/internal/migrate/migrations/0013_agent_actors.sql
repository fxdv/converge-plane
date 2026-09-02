-- 0013_agent_actors.sql — M6: agents as first-class, swarm-capable actors
--
-- An agent is an account that authenticates exclusively with a long-lived
-- API token (never the magic-link flow) and carries the 'agent' workspace
-- role. Each member of an agent swarm is a distinct account, so issue
-- assignments, comments, issue history and audit rows attribute to the
-- individual agent, and any one of them can be suspended or have its
-- token revoked without touching the rest of the swarm.
--
-- Agent emails are synthetic on the reserved .local domain (RFC 6762:
-- never routed, never delivered) and derive deterministically from the
-- workspace id + agent name, so the accounts.email unique constraint
-- gives global uniqueness without weakening it. The magic-link flow
-- rejects these emails: an agent identity must never sign in as a human.

alter table accounts
  add column kind text not null default 'human' check (kind in ('human', 'agent'));

alter table workspace_members drop constraint workspace_members_role_check;
alter table workspace_members
  add constraint workspace_members_role_check
  check (role in ('owner', 'admin', 'member', 'agent'));

-- Long-lived bearer tokens for machine actors. Only the SHA-256 hash of
-- the token is stored; the plaintext is returned exactly once (at
-- creation or rotation) and never recovered. The pattern follows
-- auth_codes and sessions.
create table api_tokens (
  id            uuid primary key default gen_random_uuid(),
  account_id    uuid not null references accounts (id) on delete cascade,
  name          text not null default 'default',
  token_hash    text not null unique,
  token_prefix  text not null,
  expires_at    timestamptz,
  revoked_at    timestamptz,
  last_used_at  timestamptz,
  created_by    uuid references accounts (id),
  created_at    timestamptz not null default now()
);
create index api_tokens_account_idx on api_tokens (account_id);
