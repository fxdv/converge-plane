-- 0006_sync_outbox.sql — M2: change feed for issue/comment mutations
--
-- Every committed mutation writes one outbox row carrying the exact
-- SyncActionRecord payload (minus sequenceId) delivered to realtime
-- subscribers and served by the delta endpoint. The outbox is the
-- single source of change records; it is the transactional outbox the
-- spec requires (doc 08 boundary principle 3).

create table sync_outbox (
  id            uuid primary key default gen_random_uuid(),
  workspace_id  uuid not null references workspaces (id) on delete cascade,
  sequence_id   bigint not null,
  model_name    text not null,
  model_id      uuid,
  action        text not null check (action in ('CREATE', 'UPDATE', 'DELETE')),
  data          jsonb not null,
  created_at    timestamptz not null default now()
);

create unique index sync_outbox_ws_seq_idx on sync_outbox (workspace_id, sequence_id);
create index sync_outbox_model_idx on sync_outbox (workspace_id, model_name, model_id);

-- Retain a bounded window per workspace (trim on each write; the delta
-- endpoint only needs records newer than the client's watermark).
create or replace function trim_sync_outbox() returns trigger as $$
begin
  delete from sync_outbox
  where workspace_id = new.workspace_id
    and id not in (
      select id from sync_outbox
      where workspace_id = new.workspace_id
      order by sequence_id desc
      limit 2000
    );
  return new;
end;
$$ language plpgsql;

create trigger sync_outbox_trim
after insert on sync_outbox
for each row execute function trim_sync_outbox();

-- search_text: project the rich-text document's plain text. The document
-- is stored as the client's JSON string ({"json":...,"text":...}); legacy
-- rows hold a bare JSON string.
alter table issues drop column search_text;

alter table issues add column search_text text not null generated always as (
  coalesce(title, '') || ' ' || coalesce(
    case jsonb_typeof(description)
      when 'object' then coalesce(description ->> 'text', description ->> 'plain', '')
      when 'string' then description #>> '{}'
      else ''
    end, '')
) stored;
