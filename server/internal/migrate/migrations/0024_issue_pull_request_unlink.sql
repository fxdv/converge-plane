-- 0024_issue_pull_request_unlink.sql — people link and unlink PRs.
-- spec cs:agents:prlinks
--
-- A member may link a pull request to an issue by hand, and unlink one.
-- Unlinking keeps the row as a tombstone (unlinked_at set, no longer
-- checked): agents repeat their evidence on every heartbeat, and a
-- deleted row would come straight back. Linking the PR again by hand
-- clears the tombstone.
alter table issue_pull_requests add column if not exists unlinked_at timestamptz;
alter table issue_pull_requests add column if not exists unlinked_by uuid references accounts (id) on delete set null;
