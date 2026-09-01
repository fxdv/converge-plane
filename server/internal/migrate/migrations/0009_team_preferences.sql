-- 0009_team_preferences.sql — M5: persist the client's team preferences
--
-- The client's Team model carries a preferences object (teamType,
-- cyclesEnabled, ...); the settings UI saves it via
-- POST /teams/{id}/preferences. Previously the server echoed a hardcoded
-- default; the column makes the write real while cycles themselves remain
-- a v1.1 feature.

alter table teams add column preferences jsonb not null default '{}'::jsonb;
