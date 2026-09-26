-- 0020_session_rotation.sql — sessions become the refresh authority.
--
-- token_hash is now the hash of the session's current refresh token.
-- Every refresh swaps in the next token's hash and keeps the one it
-- replaced (prev_token_hash, rotated_at) for a short grace window, so two
-- tabs refreshing with the same cookie are not mistaken for a stolen
-- token being replayed. Any older refresh token revokes the session.
alter table sessions
  add column if not exists prev_token_hash text,
  add column if not exists rotated_at timestamptz,
  add column if not exists revoked_reason text;

-- Rows written before this migration hash access tokens, which no longer
-- identify a session; retire them. Their tokens carry no session id and
-- are refused, so every browser signs in once after the upgrade.
update sessions
set revoked_at = now(), revoked_reason = 'upgrade'
where revoked_at is null;
