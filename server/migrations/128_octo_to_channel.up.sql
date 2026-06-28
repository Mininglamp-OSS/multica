-- Converge the Octo IM integration onto the generalized channel_* tables
-- (MUL-3620 follow-up). Octo (migration 120) predated the channel framework
-- (migration 124) and shipped its own parallel octo_* tables mirroring the
-- pre-generalization lark_* shape. This migration folds every octo_* row into
-- the channel_* tables with channel_type='octo', then drops the octo_* tables,
-- so a single channel-agnostic engine.Supervisor + engine.Router drives Octo
-- exactly like Feishu and Slack.
--
-- The mapping mirrors migration 124's lark_* -> channel_* INSERT...SELECT:
--   * Octo's flat columns (robot_id, api_url, ws_url, bot_name, owner_uid,
--     bot_token_encrypted) move into channel_installation.config JSONB. The
--     robot_id lands in config->>'app_id' — the routing-key slot the generic
--     GetChannelInstallationByAppID query + idx_channel_installation_type_appid
--     unique index already use (Slack stores its team_id there the same way).
--     Octo's deployment-wide UNIQUE(robot_id) maps to that per-(channel_type,
--     app_id) unique index with no behavioral change.
--   * bot_token_encrypted is BYTEA; channel config stores it base64-encoded
--     (encode(...,'base64')), matching how the Slack/Feishu config blob carries
--     secretbox ciphertext as base64 text. The Go decoder base64-decodes it.
--   * octo_chat_session_binding.octo_channel_type (SMALLINT WuKongIM type) maps
--     to channel_chat_session_binding.chat_type ('p2p'/'group') for the shared
--     column, with the original numeric type preserved in config->>'channel_type'
--     so the outbound path can send with the exact WuKongIM channel type.
--   * octo_outbound_message -> channel_outbound_card_message (octo_message_id ->
--     channel_card_message_id; the WuKongIM message_seq is not carried — the
--     channel table has no seq column and the seq was only ever recorded, never
--     read back).
--   * octo_inbound_dedup / octo_inbound_audit are rolling operational data
--     (purged by the cleanup job); they are NOT migrated — channel_* starts
--     fresh, exactly as a redeploy would.
--
-- channel_* has no foreign keys (MUL-3515 §4); the octo_* composite FKs are
-- dropped with the tables. The issue.origin_type CHECK already includes
-- 'octo_chat' (migration 120) and is left untouched.

-- ---------------------------------------------------------------------------
-- channel_installation  <-  octo_installation
-- ---------------------------------------------------------------------------
INSERT INTO channel_installation (
    id, workspace_id, agent_id, channel_type, config, status,
    ws_lease_token, ws_lease_expires_at, installer_user_id,
    installed_at, created_at, updated_at
)
SELECT
    oi.id,
    oi.workspace_id,
    oi.agent_id,
    'octo',
    jsonb_build_object(
        'app_id',              oi.robot_id,
        'robot_id',            oi.robot_id,
        'api_url',             oi.api_url,
        'ws_url',              oi.ws_url,
        'bot_name',            oi.bot_name,
        'owner_uid',           oi.owner_uid,
        'bot_token_encrypted', encode(oi.bot_token_encrypted, 'base64')
    ),
    oi.status,
    oi.ws_lease_token,
    oi.ws_lease_expires_at,
    oi.installer_user_id,
    oi.installed_at,
    oi.created_at,
    oi.updated_at
FROM octo_installation oi;

-- ---------------------------------------------------------------------------
-- channel_user_binding  <-  octo_user_binding
-- ---------------------------------------------------------------------------
INSERT INTO channel_user_binding (
    id, workspace_id, multica_user_id, installation_id,
    channel_type, channel_user_id, config, bound_at
)
SELECT
    ob.id,
    ob.workspace_id,
    ob.multica_user_id,
    ob.installation_id,
    'octo',
    ob.octo_uid,
    '{}'::jsonb,
    ob.bound_at
FROM octo_user_binding ob;

-- ---------------------------------------------------------------------------
-- channel_chat_session_binding  <-  octo_chat_session_binding
-- ---------------------------------------------------------------------------
INSERT INTO channel_chat_session_binding (
    id, chat_session_id, installation_id, channel_type,
    channel_chat_id, chat_type, config, created_at
)
SELECT
    ocsb.id,
    ocsb.chat_session_id,
    ocsb.installation_id,
    'octo',
    ocsb.octo_channel_id,
    CASE ocsb.octo_channel_type
        WHEN 1 THEN 'p2p'
        ELSE 'group'
    END,
    jsonb_build_object('channel_type', ocsb.octo_channel_type),
    ocsb.created_at
FROM octo_chat_session_binding ocsb;

-- ---------------------------------------------------------------------------
-- channel_outbound_card_message  <-  octo_outbound_message
-- ---------------------------------------------------------------------------
INSERT INTO channel_outbound_card_message (
    id, chat_session_id, task_id, channel_type,
    channel_chat_id, channel_card_message_id, status, last_patched_at, created_at
)
SELECT
    oom.id,
    oom.chat_session_id,
    oom.task_id,
    'octo',
    oom.octo_channel_id,
    oom.octo_message_id,
    oom.status,
    oom.last_edited_at,
    oom.created_at
FROM octo_outbound_message oom;

-- ---------------------------------------------------------------------------
-- channel_binding_token  <-  octo_binding_token
-- ---------------------------------------------------------------------------
INSERT INTO channel_binding_token (
    token_hash, workspace_id, installation_id, channel_type,
    channel_user_id, expires_at, consumed_at, created_at
)
SELECT
    obt.token_hash,
    obt.workspace_id,
    obt.installation_id,
    'octo',
    obt.octo_uid,
    obt.expires_at,
    obt.consumed_at,
    obt.created_at
FROM octo_binding_token obt;

-- ---------------------------------------------------------------------------
-- Drop the parallel octo_* tables (reverse dependency order).
-- ---------------------------------------------------------------------------
DROP TABLE IF EXISTS octo_binding_token;
DROP TABLE IF EXISTS octo_outbound_message;
DROP TABLE IF EXISTS octo_inbound_audit;
DROP TABLE IF EXISTS octo_inbound_dedup;
DROP TABLE IF EXISTS octo_chat_session_binding;
DROP TABLE IF EXISTS octo_user_binding;
DROP TABLE IF EXISTS octo_installation;
