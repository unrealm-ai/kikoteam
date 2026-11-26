-- +goose Up
ALTER TABLE platform_credit_settings
 ADD COLUMN referral_rewards_enabled boolean NOT NULL DEFAULT false,
 ADD COLUMN referral_rewards_version bigint NOT NULL DEFAULT 0 CHECK (referral_rewards_version >= 0);

CREATE TABLE platform_referral_links (
 code uuid PRIMARY KEY,
 inviter_account_id uuid NOT NULL UNIQUE REFERENCES platform_accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz
);

CREATE TABLE platform_referral_attributions (
 new_account_id uuid PRIMARY KEY REFERENCES platform_accounts(id),
 referral_code uuid NOT NULL REFERENCES platform_referral_links(code),
 rewards_version bigint NOT NULL CHECK (rewards_version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 rewarded_at timestamptz
);
CREATE INDEX platform_referral_attributions_code ON platform_referral_attributions(referral_code);

ALTER TABLE platform_credit_ledger DROP CONSTRAINT platform_credit_ledger_check;
ALTER TABLE platform_credit_ledger ADD CONSTRAINT platform_credit_ledger_check CHECK (
 (kind='signup_bonus' AND amount_units >= 0 AND usage_id IS NULL AND agent_id IS NULL)
 OR (kind='admin_grant' AND amount_units > 0 AND operator_id IS NOT NULL AND reason IS NOT NULL AND usage_id IS NULL AND agent_id IS NULL)
 OR (kind='model_charge' AND amount_units <= 0 AND usage_id IS NOT NULL AND agent_id IS NOT NULL)
 OR (kind='referral_bonus' AND amount_units = 100000000 AND usage_id IS NULL AND agent_id IS NULL AND operator_id IS NULL)
);

-- +goose Down
-- The original constraint refuses to downgrade after rewards have been issued.
ALTER TABLE platform_credit_ledger DROP CONSTRAINT platform_credit_ledger_check;
ALTER TABLE platform_credit_ledger ADD CONSTRAINT platform_credit_ledger_check CHECK (
 (kind='signup_bonus' AND amount_units >= 0 AND usage_id IS NULL AND agent_id IS NULL)
 OR (kind='admin_grant' AND amount_units > 0 AND operator_id IS NOT NULL AND reason IS NOT NULL AND usage_id IS NULL AND agent_id IS NULL)
 OR (kind='model_charge' AND amount_units <= 0 AND usage_id IS NOT NULL AND agent_id IS NOT NULL)
);
DROP TABLE platform_referral_attributions;
DROP TABLE platform_referral_links;
ALTER TABLE platform_credit_settings DROP COLUMN referral_rewards_enabled, DROP COLUMN referral_rewards_version;
