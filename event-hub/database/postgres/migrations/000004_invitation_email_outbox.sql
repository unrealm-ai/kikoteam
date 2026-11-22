-- +goose Up
-- Outbound invitation credentials are AEAD ciphertext; tokens/recipients are never duplicated in plaintext here.
CREATE TABLE platform_invitation_deliveries (
 invitation_id uuid PRIMARY KEY REFERENCES platform_invitations(id),
 ciphertext bytea NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','unknown','cancelled')),
 claim_id uuid,
 lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((status='sending' AND claim_id IS NOT NULL AND lease_until IS NOT NULL) OR (status<>'sending' AND claim_id IS NULL AND lease_until IS NULL))
);
CREATE INDEX platform_invitation_delivery_pending ON platform_invitation_deliveries(created_at,invitation_id) WHERE status='pending';
CREATE INDEX platform_invitation_delivery_lease ON platform_invitation_deliveries(lease_until) WHERE status='sending';
