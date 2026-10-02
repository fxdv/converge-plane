-- 0027_phase8_phase9.sql — shared sign-in limits, one-way GitHub issues.
-- spec cs:arch:seams
-- spec cs:agents:prlinks
--
-- auth_rate_buckets is the sign-in token bucket shared by every API
-- process. The key is "ip:" plus the client address, or "email:" plus
-- the address a code was asked for. The in-memory bucket remains the
-- fallback when this table cannot be read.
--
-- github_issue_links records an open GitHub issue that was copied into
-- the queue. The copy is not written back, and a later close on GitHub
-- does not change the Converge issue.

create table if not exists auth_rate_buckets (
    bucket_key text primary key,
    tokens     double precision not null,
    updated_at timestamptz not null default now()
);

create table if not exists github_issue_links (
    id           uuid primary key default gen_random_uuid(),
    workspace_id uuid not null references workspaces (id) on delete cascade,
    issue_id     uuid not null references issues (id) on delete cascade,
    repo         text not null,
    number       integer not null check (number > 0),
    created_at   timestamptz not null default now(),
    unique (workspace_id, repo, number)
);
