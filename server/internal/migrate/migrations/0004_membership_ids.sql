-- 0004_membership_ids.sql — surrogate ids for membership tables.
--
-- The client's UsersOnWorkspaces model requires a stable record id per
-- membership row, so membership tables get opaque uuids. The previous
-- composite primary keys become unique constraints (same invariants).

-- workspace_members
alter table workspace_members add column id uuid default gen_random_uuid();
alter table workspace_members alter column id set not null;
alter table workspace_members add constraint workspace_members_id_key unique (id);
alter table workspace_members drop constraint workspace_members_pkey;
alter table workspace_members add primary key (id);
alter table workspace_members
  add constraint workspace_members_uniq unique (workspace_id, account_id);

-- team_members
alter table team_members add column id uuid default gen_random_uuid();
alter table team_members alter column id set not null;
alter table team_members add constraint team_members_id_key unique (id);
alter table team_members drop constraint team_members_pkey;
alter table team_members add primary key (id);
alter table team_members
  add constraint team_members_uniq unique (team_id, account_id);
