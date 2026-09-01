-- 0011_membership_invited.sql — M5: invited membership state + view bookmarks
--
-- Invitations materialize as workspace_members rows with status
-- 'invited' before the invitee signs in (the client's UsersOnWorkspace
-- model carries an INVITED state), so the check constraint must admit it.
--
-- saved_views gains is_bookmarked: the client's View model requires a
-- non-null boolean and the bookmark action persists through the update path.

alter table workspace_members drop constraint workspace_members_status_check;
alter table workspace_members
  add constraint workspace_members_status_check
  check (status in ('active', 'invited', 'suspended'));

alter table saved_views add column is_bookmarked boolean not null default false;
