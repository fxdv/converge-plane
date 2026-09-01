-- 0012_search_text.sql — M5: search the full title AND description
--
-- The 0001 generated column only pulled `description ->> 'plain'`,
-- which is empty for the shapes the client actually stores (a JSON
-- string scalar carrying the rich-text document, or a wrapper object
-- with `text`). This makes every stored shape searchable.
--
-- A generated column cannot be altered, only dropped and re-added; the
-- pg_trgm GIN index (issues_search_idx) is rebuilt on the new column.

alter table issues drop column if exists search_text;
alter table issues add column search_text text not null generated always as (
  coalesce(title, '') || ' ' || case
    when jsonb_typeof(description) = 'string' then description #>> '{}'
    when jsonb_typeof(description) = 'object' then coalesce(description ->> 'plain', description ->> 'text', '')
    else ''
  end
) stored;

create index if not exists issues_search_idx
  on issues using gin (search_text gin_trgm_ops);
