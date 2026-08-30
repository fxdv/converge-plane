-- 0005_auth_codes_metadata.sql — pre-auth session metadata for the
-- Supertokens v16+ passwordless flow.
--
-- The v16 wire protocol (POST /api/auth/signinup/code) hands the client a
-- preAuthSessionId and deviceId that it returns on code/resend and
-- code/consume, so those values must be tracked per code row.

alter table auth_codes add column pre_auth_session_id text;
alter table auth_codes add column device_id text;
create index auth_codes_preauth_idx on auth_codes (pre_auth_session_id);
