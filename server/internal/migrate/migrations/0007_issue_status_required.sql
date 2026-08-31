-- 0007_issue_status_required.sql — M2: enforce the client's stateId contract
--
-- The web client's Issue model types stateId as a strict string (never
-- null, never missing), and Tegon's own schema declares statusId as a
-- required column. An issue without a workflow status cannot be rendered
-- by the client, so the data layer must never hold one.

alter table issues
  alter column status_id set not null;
