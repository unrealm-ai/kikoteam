-- +goose Up
-- Repoint credentials, not usage snapshots or settled ledger entries.
INSERT INTO platform_credit_wallets(account_id)
SELECT DISTINCT owner.user_id
FROM platform_agent_model_keys k
JOIN platform_members owner ON owner.workspace_id=k.workspace_id AND owner.role='owner' AND owner.status='active'
ON CONFLICT(account_id) DO NOTHING;

UPDATE platform_agent_model_keys k
SET payer_account_id=owner.user_id, updated_at=now()
FROM platform_members owner
WHERE owner.workspace_id=k.workspace_id AND owner.role='owner' AND owner.status='active'
  AND k.payer_account_id<>owner.user_id;
