-- 0010_mutable_version.sql — M5: version tokens and description columns
--
-- 0001's convention: "Mutable rows carry a `version` token for
-- optimistic concurrency." teams, labels, workflow_statuses and
-- workspaces were mutable without one; the M5 write paths increment it.
--
-- description lands on labels and workflow_statuses: the client's
-- Label/Workflow models carry the field, and the M5 write paths persist
-- it (null when the client sends none).

alter table workspaces add column version integer not null default 0;
alter table teams add column version integer not null default 0;
alter table labels add column version integer not null default 0;
alter table labels add column description text;
alter table workflow_statuses add column version integer not null default 0;
alter table workflow_statuses add column description text;
