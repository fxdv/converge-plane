-- 0026_phase5_phase6.sql — verified CI, shared rate buckets.
-- spec cs:agents:evidence
-- spec cs:arch:seams
--
-- ci_state is what GitHub's combined commit status said the last time the
-- poller read the pull request. An agent-reported CI link is not this
-- column. success is proof an agent may use to move the issue to Done.
--
-- rate_buckets is the per-account token bucket shared by every API
-- process. The in-memory bucket remains the fallback when this table
-- cannot be read.

alter table issue_pull_requests
  add column if not exists ci_state text
  check (ci_state is null or ci_state in ('pending', 'success', 'failure'));

create table if not exists rate_buckets (
    account_id uuid primary key,
    tokens     double precision not null,
    updated_at timestamptz not null default now()
);
