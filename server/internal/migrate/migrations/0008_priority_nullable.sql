-- 0008_priority_nullable.sql — M2: match the real client priority domain
--
-- The client's new-issue template defaults priority to 0 ("no explicit
-- priority") and its Issue model types priority as number | null.
-- Tegon's own schema declares priority as Int? with no range. The 1-4
-- check was an unverified assumption and 422'd legitimate client
-- payloads (and would have rejected them at the DB layer anyway).

alter table issues
  drop constraint issues_priority_check,
  alter column priority drop not null;
