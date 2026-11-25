-- +goose Up
ALTER TABLE manager_human_notices
 ADD COLUMN card_refresh_token uuid,
 ADD COLUMN card_refresh_until timestamptz,
 ADD COLUMN card_refresh_attempts integer NOT NULL DEFAULT 0 CHECK (card_refresh_attempts BETWEEN 0 AND 3),
 ADD COLUMN card_refresh_result text CHECK (card_refresh_result IN ('updated','failed','unknown'));
CREATE INDEX manager_human_card_refresh_pending ON manager_human_notices(sent_at,id)
 WHERE status='sent' AND provider IN ('feishu','dingtalk') AND receipt->>'human_confirmation_card'='true'
   AND card_refresh_attempts<3 AND (card_refresh_result IS NULL OR card_refresh_result='unknown');

-- +goose Down
DROP INDEX manager_human_card_refresh_pending;
ALTER TABLE manager_human_notices DROP COLUMN card_refresh_result,DROP COLUMN card_refresh_attempts,
 DROP COLUMN card_refresh_until,DROP COLUMN card_refresh_token;
