-- 0002_auth.sql — magic-link auth codes and sync sequences

-- One-time codes for the email magic-link flow. Only the hash is stored.
--
-- account_id is nullable: Supertokens sign-in/up semantics materialize the
-- account only when a code is consumed, while the code is issued (and the
-- link emailed) before any account row exists.
create table auth_codes (
  id            uuid primary key default gen_random_uuid(),
  account_id    uuid references accounts (id) on delete cascade,
  email         citext not null,
  purpose       text not null default 'magic_link' check (purpose = 'magic_link'),
  token_hash    text not null unique,
  expires_at    timestamptz not null,
  consumed_at   timestamptz,
  created_at    timestamptz not null default now()
);
create index auth_codes_expiry_idx on auth_codes (expires_at);

-- Per-workspace monotonically increasing sequence for sync bootstrap/delta.
create table sync_sequences (
  workspace_id  uuid primary key references workspaces (id) on delete cascade,
  last_sequence bigint not null default 0
);
