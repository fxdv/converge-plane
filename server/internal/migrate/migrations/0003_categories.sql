-- 0003_categories.sql — normalize workflow category values to the
-- API-facing uppercase enum (TRIAGE, BACKLOG, UNSTARTED, STARTED,
-- COMPLETED, CANCELED) so serializers map 1:1 with the client.

alter table workflow_statuses drop constraint if exists workflow_statuses_category_check;
alter table workflow_statuses
  add constraint workflow_statuses_category_check
  check (category in ('TRIAGE', 'BACKLOG', 'UNSTARTED', 'STARTED', 'COMPLETED', 'CANCELED'));

update workflow_statuses set category = upper(category);
